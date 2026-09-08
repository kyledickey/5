package commands

import (
	"context"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

func pageCases(delta int, user bool) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		page, _ := strconv.Atoi(parts[0])
		page += delta
		if page < 1 {
			page = 1
		}
		targetID := ""
		if len(parts) == 2 {
			targetID = parts[1]
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if resolveErr != nil {
				return resolveErr
			}
			input := quack.CaseListInput{Limit: "10", Offset: strconv.Itoa((page - 1) * 10)}
			var list *quack.CaseListResponse
			var response ui.Message
			if user {
				profile, listErr := ctx.Services.Cases.UserHistory(taskCtx, guildContext, targetID, input)
				if listErr != nil {
					return listErr
				}
				response = caseWebLink(views.CaseProfileMessage(profile, page, targetID), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "members", targetID)
			} else {
				list, err = ctx.Services.Cases.List(taskCtx, guildContext, input)
				if err != nil {
					return err
				}
				response = caseWebLink(views.CaseListMessage(list, page, targetID), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "cases", "")
			}
			_, editErr := responder.UpdateMessage(ui.EditMessage(response))
			return editErr
		})
	}
}

func pageFailures(delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That failure page is no longer available."))
		}
		page, _ := strconv.Atoi(parsed.Payload)
		page += delta
		if page < 1 {
			page = 1
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if resolveErr != nil {
				return resolveErr
			}
			result, listErr := ctx.Services.Actions.ListFailures(taskCtx, guildContext, 10, (page-1)*10)
			if listErr != nil {
				return listErr
			}
			_, editErr := responder.UpdateMessage(ui.EditMessage(views.FailedActionMessage(result, page)))
			return editErr
		})
	}
}
