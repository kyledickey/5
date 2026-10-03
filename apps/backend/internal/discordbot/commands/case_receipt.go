package commands

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// initialModeratorReceipt preserves a committed decision if optional progress
// reads fail. Notification remains explicitly unavailable until its state is known.
func initialModeratorReceipt(created *quack.CaseResponse, template *quack.TemplateResponse) *quack.CaseReceiptResponse {
	result := &quack.CaseReceiptResponse{Case: created, Notification: &quack.CaseNotificationResponse{Status: model.NotificationStatus("unavailable")}}
	if template != nil {
		result.RuleName = template.Name
		result.MemberReason = template.ReasonTemplate
		result.Appealable = template.Appealable
	}
	for _, action := range created.Actions {
		result.Actions = append(result.Actions, quack.CaseActionDetailResponse{CaseActionResponse: action})
	}
	return result
}

// refreshPrivateCaseReceipt follows enforcement and DM delivery independently,
// stops on terminal state or cancellation, and backs off after the first 30 seconds.
// Interaction tokens stay in memory and are never persisted. Later changes are
// available through the authorized View case control, not this expired receipt.
func refreshPrivateCaseReceipt(parent context.Context, responder ui.Responder, read func(context.Context, string) (*quack.CaseReceiptResponse, error), caseID string, interval, bound time.Duration) {
	ctx, cancel := context.WithTimeout(parent, bound)
	defer cancel()
	started := time.Now()
	initialTimer := time.NewTimer(interval)
	select {
	case <-ctx.Done():
		initialTimer.Stop()
		return
	case <-initialTimer.C:
	}
	previous := ""
	for {
		if ctx.Err() != nil {
			return
		}
		receipt, err := read(ctx, caseID)
		if err == nil && receipt != nil {
			message := views.CaseModeratorReceipt(receipt)
			encoded, _ := json.Marshal(message)
			if string(encoded) != previous {
				if _, err = responder.EditOriginal(ui.EditMessage(message)); err == nil {
					previous = string(encoded)
				}
			}
			if err == nil && !receipt.Pending() {
				return
			}
		}
		delay := interval
		if time.Since(started) > 30*time.Second && delay < 10*time.Second {
			delay = 10 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// handleCaseViewComponent opens a separate staff detail with current Discord
// authority, including after the creation receipt's short refresh window ends.
func handleCaseViewComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		detail, err := ctx.Services.Cases.GetNativeDetail(taskCtx, guild, parsed.Payload)
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseWebLink(views.CaseDetailPage(detail, 1, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
		return err
	})
}

type updatePublicCaseResultOpts struct {
	ctx                  context.Context
	responder            ui.Responder
	services             *quack.Services
	created              *quack.CaseResponse
	messageID, channelID string
	template             *quack.TemplateResponse
}

// updatePublicCaseResult persists public receipt coordinates for restart-safe
// refresh with bot credentials. If storage fails, a bounded interaction worker
// attempts recovery while the caller retains the private committed-case receipt.
// TODO: make a struct for this
func updatePublicCaseResult(opts updatePublicCaseResultOpts) error {
	if opts.services == nil || opts.services.Cases == nil || opts.responder == nil || opts.created == nil || opts.created.ID == "" || opts.messageID == "" {
		return nil
	}
	// Persist the staff receipt display for recovery without serializing unrelated
	// template configuration or evidence attachments.
	publicCase := &quack.CaseResponse{ModeratorDiscordUserID: opts.created.ModeratorDiscordUserID, ContextValues: opts.created.ContextValues, Reason: opts.created.Reason, ID: opts.created.ID, CaseNumber: opts.created.CaseNumber, CreatedAt: opts.created.CreatedAt, TargetDiscordUserID: opts.created.TargetDiscordUserID, Validity: opts.created.Validity}
	var publicTemplate *quack.TemplateResponse
	if opts.template != nil {
		publicTemplate = &quack.TemplateResponse{Name: opts.template.Name, Slug: opts.template.Slug}
	}
	memberReason := ""
	if opts.template != nil {
		memberReason = opts.template.ReasonTemplate
	}
	encoded, err := json.Marshal(views.CaseCreated{MemberReason: memberReason, Case: publicCase, Template: publicTemplate})
	if err != nil {
		return err
	}
	receipt := model.CasePublication{CaseID: opts.created.ID, MessageID: opts.messageID, ChannelID: opts.channelID, PresentationJSON: string(encoded), RetryAt: time.Now().UTC()}
	persistErr := opts.services.Cases.RecordPublicReceipt(opts.ctx, receipt)
	if persistErr == nil {
		return nil
	}

	snapshot := *opts.created
	snapshot.Actions = slices.Clone(opts.created.Actions)
	go func() {
		ctx, cancel := context.WithTimeout(opts.ctx, 14*time.Minute)
		defer cancel()
		refreshPublicCaseResult(ctx, opts.responder, opts.services.Cases.PublicReceiptActionStatuses, &snapshot, opts.messageID, opts.template, 2*time.Second)
	}()
	return persistErr
}

// refreshPublicCaseResult publishes changing action statuses and retries failed
// edits of the same message. The caller owns snapshot; no case action is executed.
func refreshPublicCaseResult(ctx context.Context, responder ui.Responder, listActions func(context.Context, string) ([]quack.CaseActionResponse, error), snapshot *quack.CaseResponse, messageID string, template *quack.TemplateResponse, interval time.Duration) {
	memberReason := ""
	if template != nil {
		memberReason = template.ReasonTemplate
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	dirty := false
	for {
		if ctx.Err() != nil {
			return
		}
		actions, err := listActions(ctx, snapshot.ID)
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "Could not refresh public case result", "case_id", snapshot.ID, "error_type", "store_read")
			}
		} else {
			byID := make(map[string]model.ActionExecutionStatus, len(actions))
			for _, action := range actions {
				byID[action.ID] = action.Status
			}
			terminal := true
			for i := range snapshot.Actions {
				if status, ok := byID[snapshot.Actions[i].ID]; ok && snapshot.Actions[i].Status != status {
					snapshot.Actions[i].Status = status
					dirty = true
				}
				switch snapshot.Actions[i].Status {
				case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying:
					terminal = false
				}
			}
			if dirty || terminal {
				_, err := responder.EditChannel(ctx, messageID, ui.EditMessage(views.CaseCreatedMessage(views.CaseCreated{MemberReason: memberReason, Case: snapshot, Template: template})))
				if err == nil {
					dirty = false
					if terminal {
						return
					}
				} else {
					slog.WarnContext(ctx, "Could not update public case result", "case_id", snapshot.ID, "error_type", "discord_response")
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
