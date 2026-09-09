package moduleintegration

import (
	"context"
	"strings"

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
// entry/queue interactions remain usable for the final delivery result.
func closeTicketWithFeedback(ctx context.Context, responder ui.Responder, closer ticketProgressCloser, actor tickets.Actor, ticketID, originChannelID string) error {
	ticket, err := closer.CloseWithProgress(ctx, actor, ticketID, func(ticket *tickets.Ticket) error {
		if ticket.ThreadDiscordChannelID != originChannelID {
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Signal("lock", ticketClosedCopy(ticket), true)))
		return err
	})
	if err != nil {
		_, editErr := responder.EditOriginal(ui.EditMessage(ticketCloseFailureMessage(ticket, err)))
		return editErr
	}
	if ticket.ThreadDiscordChannelID == originChannelID {
		return nil
	}
	message := ui.Signal("lock", strings.Replace(ticketClosedCopy(ticket), "This ticket is closing.", "Ticket closed.", 1), true)
	_, err = responder.EditOriginal(ui.EditMessage(message))
	return err
}

// ticketClosedCopy reports the member delivery result without exposing private content.
func ticketClosedCopy(ticket *tickets.Ticket) string {
	if ticket.CloseNoticeDelivered {
		return "The transcript is saved, and a copy has been DMed to the member. This ticket is closing."
	}
	return "The transcript is saved in the staff queue. I couldn’t confirm a DM to the member. This ticket is closing."
}
