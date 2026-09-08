package moduleintegration

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// ticketDetailPayload keeps old view buttons valid while carrying a history page.
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
