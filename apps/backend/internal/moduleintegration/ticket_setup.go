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
// Current Manage Server authority and bot permissions are checked before saving.
func (r *Runtime) SetupTickets(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run ticket setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 {
		return ui.Immediate(ui.Error("Choose the entry and staff queue channels."))
	}
	entryID, queueID := "", ""
	if option := options[0].GetOption("entry"); option != nil {
		entryID, _ = option.Value.(string)
	}
	if option := options[0].GetOption("queue"); option != nil {
		queueID, _ = option.Value.(string)
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if !actor.CanManage {
			_, err := responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to set up tickets."))
			return err
		}
		release, err := lockGuildOperation(taskCtx, &r.ticketSetupLocks, actor.GuildID)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Ticket setup is busy. Try again shortly."))
			return err
		}
		defer release()
		settings, _, err := r.Tickets.Settings(taskCtx, actor)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load ticket settings. Try again."))
			return err
		}
		entryID, err = ui.SetupChannel(taskCtx, r.session, ctx.Interaction.GuildID, entryID, settings.EntryChannelDiscordID, "support", ui.SetupTicketEntry)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		queueID, err = ui.SetupChannel(taskCtx, r.session, ctx.Interaction.GuildID, queueID, settings.QueueChannelDiscordID, "ticket-log", ui.SetupStaffChannel)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		if entryID == queueID {
			_, err = responder.EditOriginal(ui.ErrorEdit("Choose separate entry and staff queue channels."))
			return err
		}
		entryChannel, err := r.session.Channel(entryID, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || entryChannel == nil || entryChannel.GuildID != ctx.Interaction.GuildID || entryChannel.Type != discordgo.ChannelTypeGuildText {
			_, err = responder.EditOriginal(ui.ErrorEdit("The entry must be a text channel in this server that Quack can access."))
			return err
		}
		if err := (&discordadapter.Bot{Session: r.session}).ValidateStaffChannel(taskCtx, ctx.Interaction.GuildID, queueID); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Quack needs to view, send, read history and attach files in the queue channel."))
			return err
		}
		if err := (ticketDiscordClient{session: r.session}).validateTicketBotPermissions(taskCtx, ctx.Interaction.GuildID, entryID, queueID); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = entryID, queueID
		if _, err := r.Tickets.UpdateSettings(taskCtx, actor, true, settings); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not save ticket settings. Try again."))
			return err
		}
		panel, err := (ticketDiscordClient{session: r.session}).publishTicketEntry(taskCtx, settings)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Ticket channels are saved, but Quack could not update the opening buttons. Check access to the old and new entry channels, then run setup again."))
			return err
		}
		if err := r.Tickets.RecordEntryPanel(taskCtx, actor, entryID, panel.ID); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("The opening button was posted, but Quack could not save its message reference. Check the existing panel before running setup again."))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("ticket", fmt.Sprintf("Tickets are ready in <#%s>. Staff notifications and transcripts will go to <#%s>.", entryID, queueID), true)))
		return err
	})
}
