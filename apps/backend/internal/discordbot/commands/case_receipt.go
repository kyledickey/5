package commands

import (
	"context"
	"encoding/json"
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

// handleCaseViewComponent opens a separate private detail with current Discord
// authority, including after the creation receipt's short refresh window ends.
func handleCaseViewComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
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
