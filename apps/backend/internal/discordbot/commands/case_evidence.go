package commands

import (
	"context"
	"strconv"
	"strings"

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
		_, err = responder.EditOriginal(ui.EditMessage(views.CaseEvidencePage(detail, 1, ui.SessionApplicationID(ctx.Session))))
		return err
	})
}

// pageEvidence reloads the case through live staff authorization on every click;
// component payloads carry navigation only, never captured content or authority.
func pageEvidence(delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That evidence page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("That evidence page is no longer available."))
		}
		page, err := strconv.Atoi(parts[0])
		if err != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("That evidence page is no longer available."))
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				return err
			}
			detail, err := ctx.Services.Cases.Get(taskCtx, guild, parts[1])
			if err != nil {
				return err
			}
			_, err = responder.UpdateMessage(ui.EditMessage(views.CaseEvidencePage(detail, page+delta, ui.SessionApplicationID(ctx.Session))))
			return err
		})
	}
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
