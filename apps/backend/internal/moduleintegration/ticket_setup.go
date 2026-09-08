package moduleintegration

import (
	"context"
	"fmt"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// SetupTickets configures both destinations and publishes the member entry panel.
// Current Manage Server authority and destination privacy are checked before saving.
func (r *Runtime) SetupTickets(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run ticket setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 {
		return ui.Immediate(ui.Error("Choose the entry and staff queue channels."))
	}
	entry, queue := options[0].GetOption("entry"), options[0].GetOption("queue")
	if entry == nil || queue == nil {
		return ui.Immediate(ui.Error("Choose the entry and staff queue channels."))
	}
	entryID, entryOK := entry.Value.(string)
	queueID, queueOK := queue.Value.(string)
	if !entryOK || !queueOK || entryID == "" || queueID == "" || entryID == queueID {
		return ui.Immediate(ui.Error("Choose a member entry channel and a separate private staff queue."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if !actor.CanManage {
			_, err := responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to set up tickets."))
			return err
		}
		entryChannel, err := r.session.Channel(entryID, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || entryChannel == nil || entryChannel.GuildID != ctx.Interaction.GuildID || entryChannel.Type != discordgo.ChannelTypeGuildText {
			_, err = responder.EditOriginal(ui.ErrorEdit("The entry must be a text channel in this server that Quack can access."))
			return err
		}
		if err := (&discordadapter.Bot{Session: r.session}).ValidateStaffChannel(taskCtx, ctx.Interaction.GuildID, queueID); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("The queue must be a private text channel in this server, visible only to moderators and Quack."))
			return err
		}
		settings, _, err := r.Tickets.Settings(taskCtx, actor)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load ticket settings. Try again."))
			return err
		}
		settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = entryID, queueID
		if _, err := r.Tickets.UpdateSettings(taskCtx, actor, true, settings); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not save ticket settings. Try again."))
			return err
		}
		message := ui.Signal("ticket", "Need to talk to a moderator? Open a private ticket below.", false)
		message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "open", Version: "v1"}), "Open ticket", discordgo.PrimaryButton, false))}
		_, err = r.session.ChannelMessageSendComplex(entryID, message.SendParams(ui.SessionApplicationID(r.session)), discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Ticket channels are saved, but Quack could not publish the opening button. Check Send Messages permission in the entry channel and run setup again."))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("ticket", fmt.Sprintf("Tickets are ready in <#%s>. Staff notifications and transcripts will go to <#%s>.", entryID, queueID), true)))
		return err
	})
}
