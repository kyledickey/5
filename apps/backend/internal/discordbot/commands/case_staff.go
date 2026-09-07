package commands

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleCaseStaffSubcommand provides privacy-safe Discord case browsing and recovery controls.
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
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return editErr
		}
		var response ui.Message
		switch selected.Name {
		case "view":
			detail, getErr := ctx.Services.Cases.Get(taskCtx, guildContext, optionStringValue(selected.GetOption("case")))
			err = getErr
			if detail != nil {
				response = views.CaseDetailMessage(detail)
			}
		case "list":
			list, listErr := ctx.Services.Cases.List(taskCtx, guildContext, quack.CaseListInput{Limit: "10"})
			err = listErr
			if list != nil {
				response = views.CaseListMessage(list, 1, "")
			}
		case "user":
			targetID := optionStringValue(selected.GetOption("user"))
			profile, profileErr := ctx.Services.Cases.UserHistory(taskCtx, guildContext, targetID, quack.CaseListInput{Limit: "10"})
			err = profileErr
			if profile != nil {
				response = views.CaseListMessage(&quack.CaseListResponse{Cases: profile.Cases, Total: profile.Total, Limit: profile.Limit, Offset: profile.Offset}, 1, targetID)
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
				_, err = ctx.Services.Cases.Void(taskCtx, guildContext, optionStringValue(selected.GetOption("case")), optionStringValue(selected.GetOption("reason")), nil)
				response = ui.Signal("case_void", "Case voided. The correction remains in its history.", false)
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
