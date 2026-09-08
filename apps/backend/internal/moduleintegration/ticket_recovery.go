package moduleintegration

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// existingTicketMessage gives the member a destination or a way to finish cleanup
// without creating another thread. Its caller has resolved the member-owned record.
func existingTicketMessage(ticket *tickets.Ticket) ui.Message {
	if ticket == nil {
		return ui.Signal("ticket", "Your ticket is still opening. Try again in a moment.", true)
	}
	if ticket.Status == tickets.StatusOpen {
		message := ui.Signal("ticket", "You already have a ticket: <#"+ticket.ThreadDiscordChannelID+">. Continue the conversation there.", true)
		message.Components = tickets.TicketComponents(ticket.ID)
		return message
	}
	message := ui.Signal("ticket", "Your previous ticket is still being closed. Finish closing it before opening another.", true)
	id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID})
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "Finish closing", discordgo.SecondaryButton, false))}
	return message
}
