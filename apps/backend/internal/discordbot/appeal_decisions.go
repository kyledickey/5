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
			return ui.Immediate(ui.Error("Use this control in the server's appeal queue."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal control is invalid."))
		}
		actor := ctx.Interaction.Member.User
		privateQueue := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
		ack := ui.DeferEphemeral()
		if privateQueue {
			ack = ui.DeferUpdate()
		}
		return ui.Async(ack, func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("Could not verify your current moderation permissions."))
				return err
			}
			var decided *quack.AppealResponse
			switch action {
			case "accept":
				decided, err = appeals.Accept(taskCtx, guild, id.Payload, "The case was reconsidered.")
			case "reject":
				decided, err = appeals.Reject(taskCtx, guild, id.Payload, "The original decision still stands.")
			default:
				err = quack.ErrAppealValidation
			}
			if err != nil {
				text := "This appeal could not be updated. Please try again."
				switch {
				case errors.Is(err, quack.ErrAppealConflict):
					text = "This appeal has already been decided or its case was voided."
				case errors.Is(err, quack.ErrAppealPermissionDenied):
					text = "You need Moderate Members permission to review appeals."
				case errors.Is(err, quack.ErrAppealNotFound):
					text = "That appeal is not available in this server."
				}
				_, editErr := responder.EditOriginal(ui.ErrorEdit(text))
				return editErr
			}
			text := "Appeal rejected. The case and punishment remain unchanged."
			if action == "accept" {
				text = "Appeal accepted. The case was voided and any ban or timeout removal is queued."
			}
			if privateQueue {
				message := views.AppealStaffPage(decided, 1, ui.SessionApplicationID(ctx.Session))
				message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Next pending appeal", discordgo.SecondaryButton, false)))
				_, err = ui.Publish(responder, message)
				return err
			}
			if ctx.Session != nil && ctx.Interaction.Message != nil {
				message := views.AppealStaffPage(decided, 1, ui.SessionApplicationID(ctx.Session)).ForApplication(ui.SessionApplicationID(ctx.Session))
				emptyEmbeds := []*discordgo.MessageEmbed{}
				if _, editErr := ctx.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: ctx.Interaction.Message.ID, Channel: ctx.Interaction.ChannelID, Content: &message.Content, Components: &message.Components, Embeds: &emptyEmbeds, Files: message.Files, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); editErr != nil {
					text += " The queue message could not be refreshed; the decision is saved."
				}
			}
			_, err = ui.Publish(responder, ui.Signal("appeal", text, true))
			return err
		})
	}
}
