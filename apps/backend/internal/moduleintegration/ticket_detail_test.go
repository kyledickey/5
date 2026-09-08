package moduleintegration

import (
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// TestTicketDetailLifecycle prevents stale thread links and impossible closed controls.
func TestTicketDetailLifecycle(t *testing.T) {
	ticket := &tickets.Ticket{ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "deleted-thread", Status: tickets.StatusResolved, TranscriptURL: "https://discord.com/channels/guild/staff/message"}
	transcript := &tickets.Transcript{Content: "private retained content"}
	for _, pending := range []bool{false, true} {
		message := ticketDetailMessage(ticket, nil, tickets.Actor{DiscordUserID: "owner", CanManage: true}, pending, transcript, 0)
		if strings.Contains(message.Content, "deleted-thread") || strings.Contains(message.Content, "https://discord.com") {
			t.Fatal("owner received unusable navigation", message.Content)
		}
		if len(message.Components) != map[bool]int{false: 0, true: 1}[pending] {
			t.Fatal("incorrect cleanup controls", message.Components)
		}
		for _, row := range message.Components {
			for _, component := range row.(discordgo.ActionsRow).Components {
				if component.(discordgo.Button).Label != "Finish closing" {
					t.Fatal("impossible closed action", component)
				}
			}
		}
		if len(message.Files) != 1 {
			t.Fatal("owner transcript missing")
		}
		body, err := io.ReadAll(message.Files[0].Reader)
		if err != nil || string(body) != transcript.Content {
			t.Fatal("transcript changed", err)
		}
	}
	staff := ticketDetailMessage(ticket, nil, tickets.Actor{CanModerate: true}, false, nil, 0)
	if !strings.Contains(staff.Content, ticket.TranscriptURL) {
		t.Fatal("staff queue navigation missing")
	}
	ticket.Status = tickets.StatusOpen
	open := ticketDetailMessage(ticket, nil, tickets.Actor{CanManage: true}, false, nil, 0)
	if !strings.Contains(open.Content, "<#deleted-thread>") || len(open.Components[0].(discordgo.ActionsRow).Components) != 3 {
		t.Fatal("open lifecycle controls missing")
	}
	ticket.Status = tickets.StatusCancelled
	cancelled := ticketDetailMessage(ticket, nil, tickets.Actor{}, false, nil, 0)
	if len(cancelled.Components) != 0 || strings.Contains(cancelled.Content, "<#") {
		t.Fatal("imported closed ticket has active controls")
	}
}

// TestTicketDetailHistoryPages preserves long native history under Discord's limit.
func TestTicketDetailHistoryPages(t *testing.T) {
	events := []tickets.Event{{Body: strings.Repeat("🙂*\n", 2000)}, {Body: "last historical entry"}}
	pages := ticketHistoryPages(events)
	if len(pages) < 2 || !strings.Contains(pages[len(pages)-1], "last historical entry") {
		t.Fatal("history lost")
	}
	ticket := &tickets.Ticket{ID: "00000000-0000-0000-0000-000000000000", OwnerDiscordUserID: "12345678901234567890", Status: tickets.StatusResolved}
	for page := range pages {
		message := ticketDetailMessage(ticket, events, tickets.Actor{}, true, nil, page)
		if n := len(utf16.Encode([]rune(message.Content))); n > 2000 {
			t.Fatalf("page %d has %d units", page, n)
		}
		if len(message.Files) != 0 {
			t.Fatal("native history unexpectedly attached")
		}
	}
	id, page := ticketDetailPayload(ticket.ID + "~3")
	if id != ticket.ID || page != 3 {
		t.Fatal("page payload failed")
	}
	id, page = ticketDetailPayload(ticket.ID)
	if id != ticket.ID || page != 0 {
		t.Fatal("legacy view button failed")
	}
}

// TestTicketHistoryUpdatesOnlyPrivateViews prevents pagination from stacking
// responses or replacing a public queue message with private ticket contents.
func TestTicketHistoryUpdatesOnlyPrivateViews(t *testing.T) {
	for _, scenario := range []struct {
		payload  string
		flags    discordgo.MessageFlags
		response discordgo.InteractionResponseType
	}{
		{"ticket", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredChannelMessageWithSource},
		{"ticket~1", discordgo.MessageFlagsEphemeral, discordgo.InteractionResponseDeferredMessageUpdate},
		{"ticket~1", 0, discordgo.InteractionResponseDeferredChannelMessageWithSource},
	} {
		ctx := ui.Context{Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			GuildID: "guild", Type: discordgo.InteractionMessageComponent,
			Message: &discordgo.Message{Flags: scenario.flags},
			Data:    discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "view", Version: "v1", Payload: scenario.payload})},
		}}}
		result := (&Runtime{}).viewTicketComponent(ctx)
		if result.Response.Type != scenario.response {
			t.Fatalf("payload %s flags %d: got %d", scenario.payload, scenario.flags, result.Response.Type)
		}
	}
}
