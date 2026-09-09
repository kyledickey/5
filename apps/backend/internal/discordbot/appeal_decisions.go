package discordbot

import (
	"context"
	"errors"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"time"
)

// appealDecisionHandler resolves current Discord authority for every queue click.
// The case/appeal transaction arbitrates competing moderators; editing the queue
// message is feedback only and cannot turn a failed decision into a success.
func appealDecisionHandler(services *quack.Services, appeals *quack.AppealService, action string) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to decide it."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal button is broken. Open /appeals to try again."))
		}
		actor := ctx.Interaction.Member.User
		privateQueue := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, err = responder.Followup(ui.Signal("error", "I couldn’t check your Discord permissions. Try again in a moment.", true))
				return err
			}
			var decided *quack.AppealResponse
			switch action {
			case "accept":
				decided, err = appeals.Accept(taskCtx, guild, id.Payload, "This case no longer counts against you.")
			case "reject":
				decided, err = appeals.Reject(taskCtx, guild, id.Payload, "This case will stay on your record.")
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
				}
				_, editErr := responder.Followup(ui.Signal("error", text, true))
				return editErr
			}
			message := views.AppealStaffPage(decided, 1, ui.SessionApplicationID(ctx.Session))
			if privateQueue {
				message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Next pending appeal", discordgo.SecondaryButton, false)))
			}
			_, err = ui.Publish(responder, message)
			return err
		})
	}
}
