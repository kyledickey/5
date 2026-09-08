package commands

import (
	"context"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// handleCaseEvidenceComponent keeps captured content in a private staff response.
func handleCaseEvidenceComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		detail, err := ctx.Services.Cases.Get(taskCtx, guild, parsed.Payload)
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(views.CaseEvidenceMessage(detail)))
		return err
	})
}

// handleCaseUserComponent opens member history from a case without exposing it to the channel.
func handleCaseUserComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That user button is invalid."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		profile, err := ctx.Services.Cases.UserHistory(taskCtx, guild, parsed.Payload, quack.CaseListInput{Limit: "10"})
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(views.CaseListMessage(&quack.CaseListResponse{Cases: profile.Cases, Total: profile.Total, Limit: profile.Limit, Offset: profile.Offset}, 1, parsed.Payload)))
		return err
	})
}
