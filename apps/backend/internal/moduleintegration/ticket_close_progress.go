package moduleintegration

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// ticketProgressCloser keeps interaction feedback outside the ticket lifecycle,
// while making the publication-before-deletion boundary explicit.
type ticketProgressCloser interface {
	CloseWithProgress(context.Context, tickets.Actor, string, func(*tickets.Ticket) error) (*tickets.Ticket, error)
}

// closeTicketWithFeedback acknowledges saved progress while the source thread
// exists. Successful deletion invalidates interactions originating in that thread;
// entry/queue interactions remain usable for the final authorized detail button.
func closeTicketWithFeedback(ctx context.Context, responder ui.Responder, closer ticketProgressCloser, actor tickets.Actor, ticketID, originChannelID string) error {
	ticket, err := closer.CloseWithProgress(ctx, actor, ticketID, func(ticket *tickets.Ticket) error {
		if ticket.ThreadDiscordChannelID != originChannelID {
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Signal("lock", "The transcript is saved. This ticket is closing and the private thread will now be removed.", true)))
		return err
	})
	if err != nil {
		_, editErr := responder.EditOriginal(ui.EditMessage(ticketCloseFailureMessage(ticket, err)))
		return editErr
	}
	if ticket.ThreadDiscordChannelID == originChannelID {
		return nil
	}
	message := ui.Signal("lock", "Ticket closed. The transcript has been saved and the private thread deleted.", true)
	id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: ticketID})
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "View closed ticket", discordgo.SecondaryButton, false))}
	_, err = responder.EditOriginal(ui.EditMessage(message))
	return err
}
