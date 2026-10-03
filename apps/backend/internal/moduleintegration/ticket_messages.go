package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// ticketDetailPayload keeps old view buttons valid while carrying a history page:
// a payload without the "~page" suffix still decodes to page 0.
func ticketDetailPayload(payload string) (string, int) {
	id, raw, found := strings.Cut(payload, "~")
	if !found {
		return id, 0
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 0 {
		return id, 0
	}
	return id, page
}

// ticketHistoryPages splits rendered history at Discord page boundaries while
// preserving complete replies and timestamps whenever they fit on one page.
func ticketHistoryPages(events []tickets.Event) []string {
	blocks := make([]string, 0, len(events))
	for _, event := range events {
		body := event.Body
		if body == "" {
			body = string(event.Type)
		}
		blocks = append(blocks, ui.Quote(ui.PlainText(body))+"\n-# "+ui.RelativeTime(event.CreatedAt))
	}
	if len(blocks) == 0 {
		return nil
	}
	return ui.TextPages(strings.Join(blocks, "\n\n"), 1200)
}

// ticketDetailMessage renders only lifecycle actions that remain meaningful.
// Its caller authorizes detail and transcript access before passing private data;
// queue links are reserved for staff, while owners receive retained transcripts.
func ticketDetailMessage(ticket *tickets.Ticket, events []tickets.Event, actor tickets.Actor, pending bool, transcript *tickets.Transcript, page int) ui.Message {
	text := fmt.Sprintf("The ticket for <@%s> is **closed**. Closed tickets cannot be reopened.", ticket.OwnerDiscordUserID)
	components := []discordgo.MessageComponent{}
	if ticket.Status == tickets.StatusOpen {
		text = fmt.Sprintf("The ticket for <@%s> is **open**.\nOpen the conversation: <#%s>.", ticket.OwnerDiscordUserID, ticket.ThreadDiscordChannelID)
		components = ticketControls(ticket.ID, actor.CanManage)
	} else if pending {
		text += "\nClosing is still in progress. Finish closing to save the transcript to the staff queue and complete thread cleanup."
		id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID})
		components = append(components, ui.Row(ui.Button(id, "Finish closing", discordgo.SecondaryButton, false)))
	}
	if ticket.Status == tickets.StatusResolved && !pending && actor.CanManage && !ticket.CloseNoticeDelivered {
		text += "\nThe member’s DM isn’t confirmed. You can check delivery and retry if Discord rejected it."
		components = append(components, ui.Row(queueRecoveryButton("close", ticket.ID, "Retry member DM", discordgo.SecondaryButton)))
	}

	if ticket.Status != tickets.StatusOpen {
		if transcript != nil {
			text += "\nThe retained transcript is attached."
		} else {
			text += "\nNo retained transcript is available here."
		}
		if actor.CanModerate && ticket.TranscriptURL != "" {
			text += "\n[View transcript in the staff queue](" + ticket.TranscriptURL + ")."
		}
	}
	if actor.CanManage && (ticket.Status == tickets.StatusOpen || pending) && ticket.LogChannelDiscordID != "" && ticket.LogMessageDiscordID == "" {
		text += "\nThe staff queue post is unconfirmed. Check its delivery before retrying."
		components = append(components, ui.Row(queueRecoveryButton("queuefix", ticket.ID, "Recover queue post", discordgo.SecondaryButton)))
	}
	pages := ticketHistoryPages(events)
	if len(pages) > 0 {
		page = max(0, min(page, len(pages)-1))
		text += fmt.Sprintf("\n\n**Ticket history · %d/%d**\n\n%s", page+1, len(pages), pages[page])
		if len(pages) > 1 {
			button := func(target int, label string, disabled bool) discordgo.Button {
				id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: ticket.ID + "~" + strconv.Itoa(max(0, target))})
				return ui.Button(id, label, discordgo.SecondaryButton, disabled)
			}
			components = append(components, ui.Row(button(page-1, "Previous", page == 0), button(page+1, "Next", page == len(pages)-1)))
		}
	}
	message := ui.Signal("ticket", text, true)
	message.Components = components
	if transcript != nil {
		message.Files = []*discordgo.File{{Name: "ticket-" + ticket.ID + ".txt", ContentType: "text/plain; charset=utf-8", Reader: strings.NewReader(transcript.Content)}}
	}
	return message
}

// existingTicketMessage gives the member a destination or a way to finish cleanup
// without creating another thread. Its caller has resolved the member-owned record.
func existingTicketMessage(ticket *tickets.Ticket) ui.Message {
	if ticket == nil {
		return ui.Signal("ticket", "Your ticket is still opening. Try again in a moment.", true)
	}
	if ticket.Status == tickets.StatusOpen {
		message := ui.Signal("ticket", "You already have a ticket: <#"+ticket.ThreadDiscordChannelID+">. Continue the conversation there. If access or the staff queue post is missing, ask a server administrator to use Repair ticket on this ticket.", true)
		message.Components = ticketCloseComponents(ticket.ID)
		return message
	}
	message := ui.Signal("ticket", "Your previous ticket is still being closed. Finish closing it before opening another.", true)
	id := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "close", Version: "v1", Payload: ticket.ID})
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "Finish closing", discordgo.SecondaryButton, false))}
	return message
}

// ticketCloseFailureMessage describes only confirmed progress. An authorized
// record enables retry; missing/forbidden records expose neither links nor controls.
// Uncertain queue delivery requires inspection instead of another close attempt.
func ticketCloseFailureMessage(ticket *tickets.Ticket, err error) ui.Message {
	if ticket == nil {
		return ui.Signal("error", ticketErrorMessage(err), true)
	}
	if errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
		message := ui.Signal("error", ticketErrorMessage(err), true)
		message.Components = []discordgo.MessageComponent{ui.Row(queueRecoveryButton("view", ticket.ID, "Recovery", discordgo.SecondaryButton))}
		return message
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

func ticketClosedCopy(ticket *tickets.Ticket) string {
	if ticket.CloseNoticeDelivered {
		return "The transcript is saved, and a copy has been DMed to the member. This ticket is closing."
	}
	return "The transcript is saved in the staff queue. I couldn’t confirm a DM to the member. This ticket is closing."
}
