package discordbot

import (
	"context"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// appealSubmissionHandler opens one case-linked statement form for its owner.
func appealSubmissionHandler(appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		memberID := appealInteractionMember(ctx.Interaction)
		if memberID == "" {
			return ui.Immediate(ui.Error("This appeal button is unavailable."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("This appeal button is invalid."))
		}
		if err := appeals.CanSubmit(ctx.Context, id.Payload, memberID); err != nil {
			return ui.Immediate(ui.Error(appealSubmissionError(err)))
		}
		return ui.Immediate(ui.Modal("Appeal this case", ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "submit", Version: "v1", Payload: id.Payload}), []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "What would you like staff to reconsider?", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 4000, Placeholder: "Explain what happened or share your apology. You can submit once."})}))
	}
}

// appealSubmissionModal saves a single statement with fresh case ownership and
// eligibility checks. No Discord guild membership is required after a ban.
func appealSubmissionModal(appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		memberID := appealInteractionMember(ctx.Interaction)
		if memberID == "" {
			return ui.Immediate(ui.Error("This appeal form is unavailable."))
		}
		data := ctx.Interaction.ModalSubmitData()
		id, err := ui.DecodeCustomID(data.CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("This appeal form is invalid."))
		}
		var statement string
		for _, component := range data.Components {
			var children []discordgo.MessageComponent
			switch row := component.(type) {
			case discordgo.ActionsRow:
				children = row.Components
			case *discordgo.ActionsRow:
				children = row.Components
			}
			for _, child := range children {
				switch input := child.(type) {
				case discordgo.TextInput:
					if input.CustomID == "reason" {
						statement = input.Value
					}
				case *discordgo.TextInput:
					if input.CustomID == "reason" {
						statement = input.Value
					}
				}
			}
		}
		return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
			_, err := appeals.Submit(taskCtx, id.Payload, memberID, quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: statement}}})
			if err != nil {
				_, editErr := responder.EditOriginal(ui.ErrorEdit(appealSubmissionError(err)))
				return editErr
			}
			_, err = ui.Publish(responder, ui.Signal("appeal", "Your appeal was submitted. We’ll DM you when the moderators decide.", true))
			return err
		})
	}
}

// appealInteractionMember uses Discord's authenticated interaction identity, never
// a member ID supplied in a button or modal payload.
func appealInteractionMember(interaction *discordgo.InteractionCreate) string {
	if interaction == nil || interaction.Interaction == nil {
		return ""
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	return ""
}

// appealSubmissionError gives members actionable feedback without case details.
func appealSubmissionError(err error) string {
	switch {
	case errors.Is(err, quack.ErrAppealConflict):
		return "You already submitted an appeal for this case."
	case errors.Is(err, model.ErrAppealCaseIneligible):
		return "This case cannot be appealed."
	case errors.Is(err, quack.ErrAppealNotFound):
		return "This appeal is not available to you."
	case errors.Is(err, quack.ErrAppealValidation):
		return "Write your appeal in 1–4,000 characters."
	default:
		return "Your appeal could not be saved. Please try again."
	}
}
