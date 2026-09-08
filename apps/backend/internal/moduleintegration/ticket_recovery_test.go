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

// TestCloseFailureFeedbackDistinguishesConfirmedProgress avoids claiming an
// upload succeeded before its receipt, while withholding controls on denied access.
func TestCloseFailureFeedbackDistinguishesConfirmedProgress(t *testing.T) {
	for _, scenario := range []struct {
		ticket  *tickets.Ticket
		want    string
		buttons int
	}{
		{nil, "permission", 0},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusOpen}, "could not finish closing", 1},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved}, "thread has been kept", 1},
		{&tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, TranscriptURL: "saved"}, "cleanup did not finish", 1},
	} {
		message := ticketCloseFailureMessage(scenario.ticket, tickets.ErrPermissionDenied)
		if !strings.Contains(message.Content, scenario.want) || len(message.Components) != scenario.buttons {
			t.Fatalf("incorrect progress message: %+v", message)
		}
	}
}
