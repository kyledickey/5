package commands

import (
	"context"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleCaseStaffSubcommand provides authorized Discord case browsing and recovery controls.
func handleCaseStaffSubcommand(ctx ui.Context, data discordgo.ApplicationCommandInteractionData) ui.HandlerResult {
	var selected *discordgo.ApplicationCommandInteractionDataOption
	for _, option := range data.Options {
		if option != nil {
			selected = option
			break
		}
	}
	if selected == nil {
		return ui.Immediate(ui.Error("Choose a case operation."))
	}
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return editErr
		}
		var response ui.Message
		switch selected.Name {
		case "evidence":
			detail, addErr := ctx.Services.Cases.AddEvidence(taskCtx, guildContext, optionStringValue(selected.GetOption("case")), evidenceLinksFromOption(selected.GetOption("message_link")), interactionEvidenceFiles(ctx.Interaction, selected.GetOption("file")))
			err = addErr
			if detail != nil {
				response = caseWebLink(views.CaseDetailPage(detail, 1, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "cases", detail.ID)
			}
		case "view":
			detail, getErr := ctx.Services.Cases.GetNativeDetail(taskCtx, guildContext, optionStringValue(selected.GetOption("case")))
			err = getErr
			if detail != nil {
				response = caseWebLink(views.CaseDetailPage(detail, 1, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "cases", detail.ID)
			}
		case "list":
			list, listErr := ctx.Services.Cases.List(taskCtx, guildContext, quack.CaseListInput{Limit: "10"})
			err = listErr
			if list != nil {
				response = caseWebLink(views.CaseListMessage(list, 1, ""), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "cases", "")
			}
		case "user":
			targetID := optionStringValue(selected.GetOption("user"))
			profile, profileErr := ctx.Services.Cases.UserHistory(taskCtx, guildContext, targetID, quack.CaseListInput{Limit: "10"})
			err = profileErr
			if profile != nil {
				response = caseWebLink(views.CaseProfileMessage(profile, 1, targetID), ctx.Services.Config.ApplicationBaseURL, guildContext.Guild.DiscordGuildID, "members", targetID)
			}
		case "failures":
			failed, failedErr := ctx.Services.Actions.ListFailures(taskCtx, guildContext, 10, 0)
			err = failedErr
			if failed != nil {
				response = views.FailedActionMessage(failed, 1)
			}
		case "retry":
			_, err = ctx.Services.Actions.Retry(taskCtx, guildContext, optionStringValue(selected.GetOption("execution")))
			response = ui.Signal("retry", "Retry queued. Quack will check its permissions before trying again.", false)
		case "dismiss":
			_, err = ctx.Services.Actions.Dismiss(taskCtx, guildContext, optionStringValue(selected.GetOption("execution")))
			response = ui.Signal("review", "Failure dismissed. The attempt history is still on the case.", false)
		case "void":
			if confirm := selected.GetOption("confirm"); confirm == nil || !confirm.BoolValue() {
				err = quack.ErrCaseValidation
			} else {
				var item *quack.CaseResponse
				item, err = ctx.Services.Cases.Void(taskCtx, guildContext, optionStringValue(selected.GetOption("case")), optionStringValue(selected.GetOption("reason")), nil)
				if item != nil {
					response = views.CaseVoidedMessage(item)
				}
			}
		case "reverse":
			if confirm := selected.GetOption("confirm"); confirm == nil || !confirm.BoolValue() {
				err = quack.ErrCaseValidation
			} else {
				_, err = ctx.Services.Actions.Reverse(taskCtx, guildContext, optionStringValue(selected.GetOption("case")), optionStringValue(selected.GetOption("execution")), model.ActionType(optionStringValue(selected.GetOption("action"))))
				response = ui.Signal("retry", "Reversal queued. The original action stays in the case history.", false)
			}
		default:
			err = quack.ErrCaseValidation
		}
		if err != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return editErr
		}
		_, editErr := ui.Publish(responder, response)
		return editErr
	})
}

func intPointer(value int) *int { return &value }

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
