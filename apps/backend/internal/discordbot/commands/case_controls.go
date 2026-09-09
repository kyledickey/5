package commands

import (
	"context"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleRetryComponent retries an authorized failed action without replacing its source.
func handleRetryComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "retry")
}

// handleDismissComponent records staff dismissal with private feedback.
func handleDismissComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "dismiss")
}

// actionControlComponent publishes recovery feedback in the invoking channel,
// while existing ephemeral views refresh in place.
// Once mutation succeeds, read or delivery failures must
// not replace its committed outcome with a generic operation failure.
func actionControlComponent(ctx ui.Context, operation string) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That action control is invalid."))
	}
	privateSource := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	acknowledgement := ui.DeferPublic()
	if privateSource {
		acknowledgement = ui.DeferUpdate()
	}
	return ui.Async(acknowledgement, func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		var controlErr error
		if operation == "retry" {
			_, controlErr = ctx.Services.Actions.Retry(taskCtx, guildContext, parsed.Payload)
		} else {
			_, controlErr = ctx.Services.Actions.Dismiss(taskCtx, guildContext, parsed.Payload)
		}
		if controlErr != nil {
			return controlErr
		}
		message := "The action retry is queued."
		if operation == "dismiss" {
			message = "The action failure was dismissed."
		}
		receipt := ui.Conversation("retry", message, "", "Use `/case failures` to review remaining failures.", "", false)
		result, listErr := ctx.Services.Actions.ListFailures(taskCtx, guildContext, 10, 0)
		if listErr == nil {
			receipt = views.FailedActionMessage(result, 1)
			receipt.Content = message + "\n\n" + receipt.Content
		}
		if privateSource {
			if _, err := responder.UpdateMessage(ui.EditMessage(receipt)); err != nil {
				// The action already committed. Keep its success in a private
				// fallback rather than returning a generic operation failure.
				receipt.Ephemeral = true
				_, _ = responder.Followup(receipt)
			}
		} else {
			retainRecoveryReceipt(taskCtx, responder, receipt, false)
		}
		return nil
	})
}

// handleVoidComponent opens the required correction reason form.
func handleVoidComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || strings.TrimSpace(parsed.Payload) == "" {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Void case", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Why are you voiding this case?", Style: discordgo.TextInputParagraph, Required: true, MinLength: 3, MaxLength: 500})}))
}

// handleVoidModal authorizes and saves a void before publishing its success.
func handleVoidModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	reason := modalTextValue(ctx.Interaction.ModalSubmitData(), "reason")
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		item, voidErr := ctx.Services.Cases.Void(taskCtx, guildContext, parsed.Payload, reason, nil)
		if voidErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(voidErr)))
			return editErr
		}
		retainRecoveryReceipt(taskCtx, responder, views.CaseVoidedMessage(item), true)
		return nil
	})
}

// handleReverseComponent opens an explicit reversal confirmation.
func handleReverseComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || len(strings.Split(parsed.Payload, "|")) != 3 {
		return ui.Immediate(ui.Error("That reversal control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "reverse_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Confirm reversal", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "confirm", Label: "Type REVERSE to confirm", Style: discordgo.TextInputShort, Required: true, MinLength: 7, MaxLength: 7})}))
}

// handleReverseModal checks current authority and keeps rejected reversal requests private.
func handleReverseModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	parts := strings.Split(parsed.Payload, "|")
	if err != nil || len(parts) != 3 || modalTextValue(ctx.Interaction.ModalSubmitData(), "confirm") != "REVERSE" {
		return ui.Immediate(ui.Error("Reversal confirmation did not match."))
	}
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		_, reverseErr := ctx.Services.Actions.Reverse(taskCtx, guildContext, parts[0], parts[1], model.ActionType(parts[2]))
		if reverseErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(reverseErr)))
			return editErr
		}
		retainRecoveryReceipt(taskCtx, responder, ui.Conversation("retry", "The reversal is queued.", "", "The original action stays in the case history.", "", false), true)
		return nil
	})
}

// retainRecoveryReceipt updates one committed result without repeating moderation.
// Normal commands defer publicly; private recovery browsers keep their own source.
func retainRecoveryReceipt(ctx context.Context, responder ui.Responder, receipt ui.Message, _ bool) {
	if _, err := establishPrivateCaseReceipt(ctx, responder, receipt); err != nil {
		slog.WarnContext(ctx, "Could not edit committed recovery receipt", "error_type", "discord_response")
		_, _ = responder.Followup(ui.Signal("error", "The change was saved, but I couldn’t update this message. Check `/case view` for the result.", true))
	}
}
