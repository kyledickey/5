package discordbot

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// appealStatementPage opens shared review messages in the staff channel and rechecks live
// authority before every read. It never edits the shared queue's reading position.
func appealStatementPage(services *quack.Services, appeals *quack.AppealService, delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in your server's review queue."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		parts := strings.SplitN(id.Payload, "|", 2)
		if err != nil || len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("I couldn’t open that page. Run /appeals to start again."))
		}
		page, err := strconv.Atoi(parts[0])
		if err != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("I couldn’t open that page. Run /appeals to start again."))
		}
		ack := ui.DeferPublic()
		if ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0 {
			ack = ui.DeferUpdate()
		}
		return ui.Async(ack, func(taskCtx context.Context, responder ui.Responder) error {
			actor := ctx.Interaction.Member.User
			guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return err
			}
			appeal, err := appeals.GetStaff(taskCtx, guild, parts[1])
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t open that appeal. Check that you have Moderate Members permission, then try /appeals."))
				return err
			}
			message := views.AppealStaffPage(appeal, page+delta, ui.SessionApplicationID(ctx.Session))
			message.Ephemeral = false
			message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Pending appeals", discordgo.SecondaryButton, false)))
			_, err = responder.EditOriginal(ui.EditMessage(message))
			return err
		})
	}
}
