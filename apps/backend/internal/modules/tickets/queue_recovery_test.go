package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

// recoveryQueueFake distinguishes edits, admitted sends, and receipt reads so
// uncertainty can be exercised without treating an existing post as a new send.
type recoveryQueueFake struct {
	*discordFake
	exists           bool
	readErr, sendErr error
	missingEdit      bool
	sends, edits     int
}

// TicketQueueMessageExists models definite absence separately from read failure.
func (f *recoveryQueueFake) TicketQueueMessageExists(context.Context, string, string) (bool, error) {
	return f.exists, f.readErr
}

// PublishTicketQueue never silently replaces a missing edit; only an empty
// receipt represents a new send that the module must already have fenced.
func (f *recoveryQueueFake) PublishTicketQueue(_ context.Context, ticket *tickets.Ticket, _ tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if ticket.LogMessageDiscordID != "" {
		f.edits++
		if f.missingEdit {
			return nil, tickets.ErrQueueMessageMissing
		}
		return &tickets.QueueReceipt{MessageID: ticket.LogMessageDiscordID, URL: "saved"}, nil
	}
	f.sends++
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.exists = true
	return &tickets.QueueReceipt{MessageID: fmt.Sprintf("queue-%d", f.sends), URL: "saved"}, nil
}

// TestMissingQueueRepairFencesUncertainReplacement verifies both lost responses
// and failed receipt persistence remain blocked across an adapter restart.
func TestMissingQueueRepairFencesUncertainReplacement(t *testing.T) {
	for _, failure := range []string{"none", "rejected", "response", "receipt"} {
		t.Run(failure, func(t *testing.T) {
			db, service, _ := journalSetup(t)
			ctx := context.Background()
			client := &recoveryQueueFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
			ticket, err := adapter.Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			client.exists = false
			if failure == "response" {
				client.sendErr = errors.New("response lost")
			}
			if failure == "rejected" {
				client.sendErr = tickets.ErrQueueNotSent
			}
			if failure == "receipt" {
				if err := db.Callback().Update().Before("gorm:update").Register("fail_replacement_receipt", func(tx *gorm.DB) {
					if values, ok := tx.Statement.Dest.(map[string]any); ok && values["log_message_discord_id"] == "queue-2" {
						tx.AddError(errors.New("receipt write failed"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			err = adapter.RepairPermissions(ctx, actor, ticket.ID)
			if (err == nil) != (failure == "none") {
				t.Fatal("replacement result", err)
			}
			client.sendErr = nil
			// A fresh adapter must rely on SQL admission, not an in-memory retry flag.
			adapter = tickets.NewDiscordAdapter(service, client)
			err = adapter.RepairPermissions(ctx, actor, ticket.ID)
			if failure == "response" || failure == "receipt" {
				if !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
					t.Fatal("uncertain replacement retried", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantSends := 2
			if failure == "rejected" {
				wantSends = 3
			}
			if client.sends != wantSends {
				t.Fatal("duplicate replacement", client.sends)
			}
		})
	}
}

// TestCloseRechecksReceiptAfterDeleteFailure preserves the source on an uncertain
// read and republishes a definitely missing transcript before retrying deletion.
func TestCloseRechecksReceiptAfterDeleteFailure(t *testing.T) {
	_, service, _ := journalSetup(t)
	ctx := context.Background()
	client := &recoveryQueueFake{discordFake: &discordFake{failArchive: 1}}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected delete failure")
	}
	client.readErr = errors.New("queue read unavailable")
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil || client.archiveAttempts != 1 {
		t.Fatal("deleted after uncertain receipt", err)
	}
	client.readErr, client.exists, client.sendErr = nil, false, tickets.ErrQueueNotSent
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil || client.archiveAttempts != 1 {
		t.Fatal("deleted before replacement", err)
	}
	current, _, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil || current.TranscriptURL != "" {
		t.Fatal("missing transcript still trusted", current, err)
	}
	if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || !pending {
		t.Fatal("reservation released", pending, err)
	}
	client.sendErr = nil
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.archiveAttempts != 2 || client.sends != 3 {
		t.Fatal("replacement/deletion attempts", client.archiveAttempts, client.sends)
	}
	if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
		t.Fatal("cleanup pending", pending, err)
	}
}

// TestCloseReplacementSendIsFenced covers both a deleted edit target and a queue
// configuration change; neither may resend after an uncertain replacement.
func TestCloseReplacementSendIsFenced(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(fmt.Sprint(moved), func(t *testing.T) {
			_, service, _ := journalSetup(t)
			ctx := context.Background()
			client := &recoveryQueueFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
			ticket, err := adapter.Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			if moved {
				settings := tickets.Defaults()
				settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = "entry", "new-queue"
				if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
					t.Fatal(err)
				}
			} else {
				client.missingEdit = true
			}
			client.sendErr = errors.New("response lost")
			if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
				t.Fatal("uncertain send succeeded")
			}
			adapter = tickets.NewDiscordAdapter(service, client)
			if _, err := adapter.Close(ctx, actor, ticket.ID); !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
				t.Fatal("replacement retried", err)
			}
			if client.sends != 2 || client.archiveAttempts != 0 {
				t.Fatal("unsafe retry", client.sends, client.archiveAttempts)
			}
		})
	}
}
