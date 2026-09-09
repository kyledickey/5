package tickets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// noticeDiscordFake models a successful, rejected, or ambiguously accepted DM.
type noticeDiscordFake struct {
	*discordFake
	mode              string
	sends, reconciles int
}

// DeliverTicketCloseNotice distinguishes reconciliation from a new member DM.
func (f *noticeDiscordFake) DeliverTicketCloseNotice(_ context.Context, _ *tickets.Ticket, transcript *tickets.Transcript, reconcileOnly bool) (string, error) {
	if transcript == nil || transcript.Content == "" {
		return "", errors.New("transcript missing")
	}
	if reconcileOnly {
		f.reconciles++
		return "member-receipt", nil
	}
	f.sends++
	if f.mode == "blocked" {
		return "", tickets.ErrCloseNoticeNotSent
	}
	if f.mode == "uncertain" {
		return "", errors.New("connection lost after send")
	}
	return "member-receipt", nil
}

// TestCloseNoticeRetriesNeverDuplicateAcceptedDM verifies durable receipts and
// uncertain-send reconciliation across adapter recreation, plus blocked DM cleanup.
func TestCloseNoticeRetriesNeverDuplicateAcceptedDM(t *testing.T) {
	for _, mode := range []string{"success", "uncertain", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			_, service, _ := journalSetup(t)
			client := &noticeDiscordFake{discordFake: &discordFake{}, mode: mode}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := adapter.Open(context.Background(), actor)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "blocked" {
				client.failArchive = 1
			}
			closed, err := adapter.Close(context.Background(), actor, ticket.ID)
			if mode == "blocked" {
				if err != nil || closed.CloseNoticeDelivered {
					t.Fatalf("blocked DM prevented closure: %+v %v", closed, err)
				}
				if active, err := service.ActiveForMember(context.Background(), actor); err != nil || active != nil {
					t.Fatalf("member reservation not released: %v", err)
				}
				client.mode = "success"
				closed, err = tickets.NewDiscordAdapter(service, client).Close(context.Background(), actor, ticket.ID)
				if err != nil || !closed.CloseNoticeDelivered || client.sends != 2 {
					t.Fatalf("definite rejection could not retry: %+v %v", closed, err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected deletion failure")
			}
			closed, err = tickets.NewDiscordAdapter(service, client).Close(context.Background(), actor, ticket.ID)
			if err != nil || !closed.CloseNoticeDelivered {
				t.Fatalf("retry did not finish: %+v %v", closed, err)
			}
			if client.sends != 1 {
				t.Fatalf("duplicate member DMs: %d", client.sends)
			}
			if mode == "uncertain" && client.reconciles != 1 {
				t.Fatal("uncertain delivery was not reconciled")
			}
		})
	}
}
