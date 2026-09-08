package tickets_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// queueFailureFake records initial publication attempts independently of ticket
// provisioning so recovery can be checked without another owner invitation.
type queueFailureFake struct {
	*discordFake
	failure error
	calls   int
}

// PublishTicketQueue models both definite rejection and uncertain network sends.
func (f *queueFailureFake) PublishTicketQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	f.calls++
	if f.failure != nil {
		return nil, f.failure
	}
	return f.discordFake.PublishTicketQueue(ctx, ticket, settings, transcript)
}

// TestTicketQueueRepairRecoversOnlyDefiniteFailures verifies restart-safe send
// admission, repeated repair deduplication, and fresh staff authorization.
func TestTicketQueueRepairRecoversOnlyDefiniteFailures(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "uncertain"}[uncertain], func(t *testing.T) {
			db, service, _ := setup(t)
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })
			ctx := context.Background()
			failure := tickets.ErrQueueNotSent
			if uncertain {
				failure = errors.New("response lost")
			}
			client := &queueFailureFake{discordFake: &discordFake{}, failure: failure}
			adapter := tickets.NewDiscordAdapter(service, client)
			owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
			ticket, err := adapter.Open(ctx, owner)
			if err == nil || ticket == nil {
				t.Fatal(ticket, err)
			}
			client.failure = nil
			adapter = tickets.NewDiscordAdapter(service, client)
			if err := adapter.RepairPermissions(ctx, owner, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
				t.Fatal(err)
			}
			admin := tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true, CanModerate: true}
			for range 2 {
				err := adapter.RepairPermissions(ctx, admin, ticket.ID)
				if uncertain && !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
					t.Fatal(err)
				}
				if !uncertain && err != nil {
					t.Fatal(err)
				}
			}
			want := 2
			if uncertain {
				want = 1
			}
			if client.calls != want || client.channelCalls != 1 {
				t.Fatalf("queue calls=%d channels=%d", client.calls, client.channelCalls)
			}
			detail, _, err := service.Detail(ctx, admin, ticket.ID)
			if err != nil || (detail.LogMessageDiscordID != "") == uncertain {
				t.Fatal(detail, err)
			}
		})
	}
}
