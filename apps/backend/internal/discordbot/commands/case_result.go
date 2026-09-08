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

// updatePublicCaseResult persists public receipt coordinates for restart-safe
// refresh with bot credentials. If storage fails, a bounded interaction worker
// attempts recovery while the caller retains the private committed-case receipt.
func updatePublicCaseResult(ctx context.Context, responder ui.Responder, services *quack.Services, created *quack.CaseResponse, messageID string, channelID string, template *quack.TemplateResponse) error {
	if services == nil || services.Cases == nil || responder == nil || created == nil || created.ID == "" || messageID == "" {
		return nil
	}
	// Explicitly allowlist the initial public display. Never serialize the
	// full case response, staff context, evidence, or template configuration.
	publicCase := &quack.CaseResponse{ID: created.ID, CaseNumber: created.CaseNumber, CreatedAt: created.CreatedAt, TargetDiscordUserID: created.TargetDiscordUserID, Validity: created.Validity, EvidenceIncomplete: created.EvidenceIncomplete}
	if created.SelectedLevel != nil {
		publicCase.SelectedLevel = &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: created.SelectedLevel.Name}}
	}
	var publicTemplate *quack.TemplateResponse
	if template != nil {
		publicTemplate = &quack.TemplateResponse{Name: template.Name, Slug: template.Slug}
	}
	encoded, err := json.Marshal(views.CaseCreated{Case: publicCase, Template: publicTemplate})
	if err != nil {
		return err
	}
	receipt := model.CasePublication{CaseID: created.ID, MessageID: messageID, ChannelID: channelID, PresentationJSON: string(encoded), RetryAt: time.Now().UTC()}
	persistErr := services.Cases.RecordPublicReceipt(ctx, receipt)
	if persistErr == nil {
		return nil
	}

	snapshot := *created
	snapshot.Actions = slices.Clone(created.Actions)
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 14*time.Minute)
		defer cancel()
		refreshPublicCaseResult(ctx, responder, services.Cases.PublicReceiptActionStatuses, &snapshot, messageID, template, 2*time.Second)
	}()
	return persistErr
}

// refreshPublicCaseResult publishes changing action statuses and retries failed
// edits of the same message. The caller owns snapshot; no case action is executed.
func refreshPublicCaseResult(ctx context.Context, responder ui.Responder, listActions func(context.Context, string) ([]quack.CaseActionResponse, error), snapshot *quack.CaseResponse, messageID string, template *quack.TemplateResponse, interval time.Duration) {
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
				_, err := responder.EditFollowup(messageID, ui.EditMessage(views.CaseCreatedMessage(views.CaseCreated{Case: snapshot, Template: template})))
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
