package moduleintegration

import (
	"errors"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	discordcommands "github.com/quackdiscord/bot/internal/discordbot/commands"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// RegisterComponents installs ticket buttons and the reply modal into the
// process's single interaction dispatcher.
func (r *Runtime) RegisterComponents(registry *interactions.ComponentRegistry) error {
	if r == nil || r.TicketDiscord == nil || r.Tickets == nil {
		return errors.New("ticket Discord runtime is not configured")
	}
	handlers := tickets.ComponentHandlers{
		Open: r.openTicketComponent, Queue: r.ticketQueueComponent,
		View:  r.viewTicketComponent,
		Close: r.closeTicketComponent,
	}
	if err := tickets.RegisterComponents(registry, handlers); err != nil {
		return err
	}
	if err := discordcommands.RegisterAppealQueueComponents(registry); err != nil {
		return err
	}
	if err := discordcommands.RegisterCaseComponents(registry); err != nil {
		return err
	}
	if err := discordadapter.RegisterAppealComponents(registry, r.services, r.Appeals); err != nil {
		return err
	}
	if err := registry.RegisterComponent("ticket", "repair", r.repairTicketComponent); err != nil {
		return err
	}
	return nil
}

// RegisterGatewayHandlers subscribes optional modules to gateway events without
// placing their failures on the moderation action queue.
func (r *Runtime) RegisterGatewayHandlers(session *discordgo.Session) error {
	if r == nil || r.LoggingQueue == nil || r.HoneypotRuntime == nil || r.HoneypotDiscord == nil || session == nil {
		return errors.New("optional module gateway runtime is not configured")
	}
	session.AddHandler(r.onMessageCreate)
	session.AddHandler(r.onMessageUpdate)
	session.AddHandler(r.onMessageDelete)
	session.AddHandler(r.onMessageDeleteBulk)
	session.AddHandler(r.onGuildMemberAdd)
	session.AddHandler(r.onGuildMemberRemove)
	session.AddHandler(r.onTicketGuildCreate)
	session.AddHandler(r.onTicketMemberUpdate)
	session.AddHandler(r.onTicketRoleUpdate)
	session.AddHandler(r.onTicketRoleDelete)
	session.AddHandler(r.onModerationAuditEntry)
	session.AddHandler(r.onGuildUpdate)
	session.AddHandler(r.onGuildDelete)
	session.AddHandler(r.onChannelCreate)
	session.AddHandler(r.onChannelUpdate)
	session.AddHandler(r.onChannelDelete)
	return nil
}
