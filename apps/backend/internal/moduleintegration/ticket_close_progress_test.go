package moduleintegration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// progressResponder emulates Discord rejecting edits after its channel disappears.
type progressResponder struct {
	ui.Responder
	deleted  bool
	inThread bool
	messages []string
}

// EditOriginal records only still-deliverable feedback.
func (r *progressResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	if r.deleted && r.inThread {
		return nil, errors.New("unknown channel")
	}
	if edit.Content != nil {
		r.messages = append(r.messages, *edit.Content)
	}
	return &discordgo.Message{}, nil
}

// progressCloser invokes progress before deletion and can inject deletion failure.
type progressCloser struct {
	responder  *progressResponder
	failDelete bool
}

// CloseWithProgress preserves the tested adapter callback contract.
func (c progressCloser) CloseWithProgress(_ context.Context, _ tickets.Actor, _ string, progress func(*tickets.Ticket) error) (*tickets.Ticket, error) {
	ticket := &tickets.Ticket{ID: "ticket", Status: tickets.StatusResolved, ThreadDiscordChannelID: "thread", TranscriptURL: "saved"}
	if err := progress(ticket); err != nil {
		return ticket, err
	}
	if c.failDelete {
		return ticket, errors.New("cannot delete")
	}
	c.responder.deleted = true
	return ticket, nil
}

// TestCloseFeedbackSurvivesOriginDeletion proves no impossible final edit is made,
// while outside-thread confirmation and failed-deletion retry remain available.
func TestCloseFeedbackSurvivesOriginDeletion(t *testing.T) {
	for _, scenario := range []struct {
		name, origin string
		failure      bool
		count        int
		want         string
	}{
		{"inside", "thread", false, 1, "is closing"},
		{"outside", "entry", false, 1, "Ticket closed"},
		{"delete_failure", "thread", true, 2, "cleanup did not finish"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			responder := &progressResponder{inThread: scenario.origin == "thread"}
			err := closeTicketWithFeedback(context.Background(), responder, progressCloser{responder: responder, failDelete: scenario.failure}, tickets.Actor{}, "ticket", scenario.origin)
			if err != nil {
				t.Fatal(err)
			}
			if len(responder.messages) != scenario.count || !strings.Contains(responder.messages[len(responder.messages)-1], scenario.want) {
				t.Fatal(responder.messages)
			}
			if scenario.failure {
				for _, message := range responder.messages {
					if strings.Contains(message, "Ticket closed.") {
						t.Fatal("false completion", message)
					}
				}
			}
		})
	}
}
