package discordbot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// appealDecisionHandler resolves current Discord authority for every queue click.
// Guilds that require decision reasons get a form instead of one-click execution;
// the form must be the initial interaction response, so the guild setting is read
// before any acknowledgement. The case/appeal transaction arbitrates competing
// moderators; editing the queue message is feedback only and cannot turn a failed
// decision into a success.
func appealDecisionHandler(services *quack.Services, appeals *quack.AppealService, action string) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to decide it."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal button is broken. Open /appeals to try again."))
		}
		required, err := appeals.ReviewReasonRequired(ctx.Context, ctx.Interaction.GuildID)
		if err != nil {
			return ui.Immediate(ui.Error("I couldn’t check the server’s appeal settings. Try again in a moment."))
		}
		if required {
			title := "Accept appeal"
			if action == "reject" {
				title = "Reject appeal"
			}
			modalID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: action + "_reason", Version: "v1", Payload: id.Payload})
			return ui.Immediate(ui.Modal(title, modalID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Reason sent to the member", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 2000, Placeholder: "Explain this decision. The member receives this reason."})}))
		}
		reason := "This case has been voided."
		if action == "reject" {
			reason = "Appeal rejected."
		}
		return ui.Async(ui.DeferUpdate(), appealDecisionTask(services, appeals, action, id.Payload, reason, ctx.Interaction, ui.SessionApplicationID(ctx.Session)))
	}
}

// appealDecisionModal completes a reason-required decision. Live authority is
// refreshed on submission because the moderator's permissions may have changed
// since the form was opened; the entered reason replaces the canned default.
func appealDecisionModal(services *quack.Services, appeals *quack.AppealService, action string) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to decide it."))
		}
		data := ctx.Interaction.ModalSubmitData()
		id, err := ui.DecodeCustomID(data.CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal form is broken. Open /appeals to try again."))
		}
		reason := appealModalTextValue(data.Components, "reason")
		if strings.TrimSpace(reason) == "" {
			return ui.Immediate(ui.Error("Write a reason for this decision. The member receives it."))
		}
		return ui.Async(ui.DeferUpdate(), appealDecisionTask(services, appeals, action, id.Payload, reason, ctx.Interaction, ui.SessionApplicationID(ctx.Session)))
	}
}

// appealDecisionTask executes one decision with live authority and updates the
// queue message with the result.
func appealDecisionTask(services *quack.Services, appeals *quack.AppealService, action, appealID, reason string, interaction *discordgo.InteractionCreate, applicationID string) ui.Task {
	actor := interaction.Member.User
	guildID := interaction.GuildID
	privateQueue := interaction.Message != nil && interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	return func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: guildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
		if err != nil {
			_, err = responder.Followup(ui.Signal("error", "I couldn’t check your Discord permissions. Try again in a moment.", true))
			return err
		}
		var decided *quack.AppealResponse
		switch action {
		case "accept":
			decided, err = appeals.Accept(taskCtx, guild, appealID, reason)
		case "reject":
			decided, err = appeals.Reject(taskCtx, guild, appealID, reason)
		default:
			err = quack.ErrAppealValidation
		}
		if err != nil {
			text := "I couldn’t save your decision. Please try again."
			switch {
			case errors.Is(err, quack.ErrAppealConflict):
				text = "This appeal has already been decided or its case was voided."
			case errors.Is(err, quack.ErrAppealPermissionDenied):
				text = "You need Moderate Members permission to review appeals."
			case errors.Is(err, quack.ErrAppealNotFound):
				text = "I couldn’t find that appeal in this server. Open /appeals to see pending appeals."
			case errors.Is(err, quack.ErrAppealValidation):
				text = "Write a reason between 1 and 2,000 characters."
			}
			_, editErr := responder.Followup(ui.Signal("error", text, true))
			return editErr
		}
		message := views.AppealStaffPage(decided, 1, applicationID)
		if privateQueue {
			message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Next pending appeal", discordgo.SecondaryButton, false)))
		}
		_, err = ui.Publish(responder, message)
		return err
	}
}

// appealModalTextValue reads one text input from a submitted form without
// trusting component ordering.
func appealModalTextValue(components []discordgo.MessageComponent, customID string) string {
	for _, component := range components {
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
				if input.CustomID == customID {
					return input.Value
				}
			case *discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			}
		}
	}
	return ""
}
