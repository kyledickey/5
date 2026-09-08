package moduleintegration

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// TestExistingTicketFeedbackProvidesRecovery checks each member reservation state
// offers a useful next step without linking a possibly deleted closing thread.
func TestExistingTicketFeedbackProvidesRecovery(t *testing.T) {
	opening := existingTicketMessage(nil)
	if !strings.Contains(opening.Content, "still opening") || len(opening.Components) != 0 {
		t.Fatal("invalid provisional feedback")
	}
	open := existingTicketMessage(&tickets.Ticket{ID: "ticket", Status: tickets.StatusOpen, ThreadDiscordChannelID: "thread"})
	if !strings.Contains(open.Content, "<#thread>") {
		t.Fatal("open ticket lacks link")
	}
	closing := existingTicketMessage(&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, ThreadDiscordChannelID: "thread"})
	if strings.Contains(closing.Content, "<#thread>") || len(closing.Components) != 1 {
		t.Fatal("invalid cleanup feedback")
	}
	button := closing.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != "ticket" || id.Action != "close" || id.Payload != "ticket" {
		t.Fatalf("invalid recovery control: %+v %v", id, err)
	}
}
