package tickets_test

import (
	"context"
	"errors"
	"reflect"
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

// progressDiscordFake records external ordering independently of persisted state.
type progressDiscordFake struct {
	*discordFake
	order []string
}

// PublishTicketQueue records the transcript publication boundary only.
func (f *progressDiscordFake) PublishTicketQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if transcript != nil {
		f.order = append(f.order, "publish")
	}
	return f.discordFake.PublishTicketQueue(ctx, ticket, settings, transcript)
}

// DeleteTicketChannel records attempts, including recoverable external failures.
func (f *progressDiscordFake) DeleteTicketChannel(ctx context.Context, thread string) error {
	f.order = append(f.order, "delete")
	return f.discordFake.DeleteTicketChannel(ctx, thread)
}

// TestCloseProgressFollowsPublication verifies truthful acknowledgement ordering
// and that publication, acknowledgement, and deletion failures remain failures.
func TestCloseProgressFollowsPublication(t *testing.T) {
	for _, scenario := range []string{"success", "publish_failure", "ack_failure", "delete_failure"} {
		t.Run(scenario, func(t *testing.T) {
			_, service, _ := journalSetup(t)
			client := &progressDiscordFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := adapter.Open(context.Background(), actor)
			if err != nil {
				t.Fatal(err)
			}
			client.failPublish = scenario == "publish_failure"
			if scenario == "delete_failure" {
				client.failArchive = 1
			}
			_, err = adapter.CloseWithProgress(context.Background(), actor, ticket.ID, func(saved *tickets.Ticket) error {
				client.order = append(client.order, "ack")
				detail, _, detailErr := service.Detail(context.Background(), actor, ticket.ID)
				if detailErr != nil || saved.TranscriptURL == "" || detail.TranscriptURL == "" {
					t.Fatal("ack before durable receipt", detailErr)
				}
				if scenario == "ack_failure" {
					return errors.New("response unavailable")
				}
				return nil
			})
			want := []string{"publish", "ack", "delete"}
			if scenario == "publish_failure" {
				want = []string{"publish"}
			}
			if scenario == "ack_failure" {
				want = []string{"publish", "ack"}
			}
			if !reflect.DeepEqual(client.order, want) {
				t.Fatal(client.order, want)
			}
			if (err == nil) != (scenario == "success") {
				t.Fatal("incorrect terminal result", scenario, err)
			}
		})
	}
}

// TestClosurePendingUsesAuthorizedOwnerReservation covers failed deletion and
// successful cleanup for owner/staff without leaking another member's ticket.
func TestClosurePendingUsesAuthorizedOwnerReservation(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	ticket, err := adapter.Open(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil || pending {
		t.Fatal("open ticket closing", pending, err)
	}
	client.failArchive = 1
	if _, err := adapter.Close(ctx, owner, ticket.ID); err == nil {
		t.Fatal("expected deletion failure")
	}
	for _, actor := range []tickets.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || !pending {
			t.Fatal("lost cleanup state", pending, err)
		}
	}
	for _, actor := range []tickets.Actor{{GuildID: "guild-a", DiscordUserID: "other"}, {GuildID: "other-guild", DiscordUserID: "member", CanModerate: true}} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err == nil || pending {
			t.Fatal("leaked cleanup state", pending, err)
		}
	}
	if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, owner); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []tickets.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
			t.Fatal("old ticket uses newer reservation", pending, err)
		}
	}
}
