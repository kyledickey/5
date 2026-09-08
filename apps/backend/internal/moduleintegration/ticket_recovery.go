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

// ticketCloseFailureMessage describes only confirmed progress. An authorized
// record enables retry; missing/forbidden records expose neither links nor controls.
func ticketCloseFailureMessage(ticket *tickets.Ticket, err error) ui.Message {
	if ticket == nil {
		return ui.Signal("error", ticketErrorMessage(err), true)
	}
	text := "The ticket could not finish closing. Try again; if it keeps failing, ask a server administrator to check Quack's permissions."
	if ticket.Status == tickets.StatusResolved {
		if ticket.TranscriptURL == "" {
			text = "The transcript was captured, but could not be saved to the staff queue. The thread has been kept. Try again after an administrator checks the queue channel and Quack's permissions."
		} else {
			text = "The transcript is saved, but cleanup did not finish. Try again to finish closing the ticket."
		}
	}
	message := ui.Signal("error", text, true)
	id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID})
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "Retry close", discordgo.SecondaryButton, false))}
	return message
}
