package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
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

// reconciliationFake isolates the transport's verified adoption result from the
// service's authority, durable attempt identity and lifecycle responsibilities.
type reconciliationFake struct {
	*recoveryQueueFake
	receipt       *tickets.QueueReceipt
	validationErr error
	validations   int
}

// ValidateTicketQueueMessage returns only the explicitly configured proof result.
func (f *reconciliationFake) ValidateTicketQueueMessage(context.Context, *tickets.Ticket, string) (*tickets.QueueReceipt, error) {
	f.validations++
	return f.receipt, f.validationErr
}

// uncertainQueueFixture leaves a real open ticket and owner reservation behind
// an uncertain initial publication, with no durable Discord message receipt.
func uncertainQueueFixture(t *testing.T) (*gorm.DB, *tickets.Service, *tickets.DiscordAdapter, *reconciliationFake, *tickets.Ticket) {
	t.Helper()
	db, service, _ := journalSetup(t)
	client := &reconciliationFake{recoveryQueueFake: &recoveryQueueFake{discordFake: &discordFake{}, sendErr: errors.New("response lost")}}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"})
	if err == nil || ticket == nil || ticket.QueueDeliveryAttemptID == "" {
		t.Fatal("missing uncertain attempt", ticket, err)
	}
	return db, service, adapter, client, ticket
}

// TestQueueRecoveryAuthorityAndLegacyBootstrap ensures managers can inspect any
// reserved guild ticket while old destination-only fences gain one stable token.
func TestQueueRecoveryAuthorityAndLegacyBootstrap(t *testing.T) {
	db, _, adapter, _, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	for _, actor := range []tickets.Actor{
		{GuildID: "guild-a", DiscordUserID: "owner"},
		{GuildID: "guild-a", DiscordUserID: "moderator", CanModerate: true},
		{GuildID: "guild-b", DiscordUserID: "manager", CanManage: true},
	} {
		if recovery, err := adapter.QueueRecovery(ctx, actor, ticket.ID); err == nil || recovery != nil {
			t.Fatal("unauthorized recovery", recovery, err)
		}
		if _, err := adapter.ReconcileQueue(ctx, actor, tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}); err == nil {
			t.Fatal("unauthorized reconciliation")
		}
	}
	if err := db.Table("tickets").Where("id = ?", ticket.ID).Update("queue_delivery_attempt_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	manager := tickets.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	first, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || first.AttemptID == "" || first.ChannelDiscordID != "queue" {
		t.Fatal(first, err)
	}
	second, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || *first != *second {
		t.Fatal("legacy token changed on inspection", first, second, err)
	}
	if err := db.Table("ticket_member_states").Where("guild_id = ?", "guild-a").Update("open_ticket_id", "new-ticket").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.QueueRecovery(ctx, manager, ticket.ID); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatal("superseded open ticket recovered", err)
	}
}

// TestQueueReconciliationAdoptionPreservesCloseBoundary adopts without a new
// post, then uses normal closure to upload canonical content before deletion.
func TestQueueReconciliationAdoptionPreservesCloseBoundary(t *testing.T) {
	_, service, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	owner := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
	manager := tickets.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	if _, err := service.Resolve(ctx, owner, ticket.ID, "canonical transcript"); err != nil {
		t.Fatal(err)
	}
	client.receipt = &tickets.QueueReceipt{MessageID: "existing", URL: "not-trusted-as-transcript"}
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, MessageURL: "https://discord.com/channels/guild/queue/existing"}
	adopted, err := adapter.ReconcileQueue(ctx, manager, input)
	if err != nil {
		t.Fatal(err)
	}
	if adopted.LogMessageDiscordID != "existing" || adopted.TranscriptURL != "" || adopted.QueueDeliveryAttemptID != "" || client.sends != 1 || client.archiveAttempts != 0 {
		t.Fatal("adoption bypassed lifecycle", adopted, client)
	}
	if pending, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil || !pending {
		t.Fatal("adoption released reservation", pending, err)
	}
	client.sendErr, client.exists = nil, true
	if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.edits != 1 || client.sends != 1 || client.archiveAttempts != 1 {
		t.Fatal("closure did not reuse receipt", client)
	}
	_, events, err := service.Detail(ctx, owner, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == tickets.EventQueueReconciled {
			count++
			if event.ActorDiscordUserID != manager.DiscordUserID {
				t.Fatal(event)
			}
		}
	}
	if count != 1 {
		t.Fatal("missing semantic recovery event", count)
	}
	// Even a corrupted leftover fence cannot resurrect a fully closed reservation.
	if _, err := adapter.QueueRecovery(ctx, manager, ticket.ID); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatal("closed ticket recovery", err)
	}
}

// TestQueueReconciliationConfirmationUsesAttemptIdentity allows one inspected
// retry without a send, and rejects old decisions after a new same-channel attempt.
func TestQueueReconciliationConfirmationUsesAttemptIdentity(t *testing.T) {
	_, service, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}
	cleared, err := adapter.ReconcileQueue(ctx, manager, input)
	if err != nil || cleared.QueueDeliveryAttemptID != "" || cleared.LogChannelDiscordID != "" || client.sends != 1 {
		t.Fatal(cleared, err)
	}
	if active, err := service.ActiveForMember(ctx, manager); err != nil || active == nil || active.ID != ticket.ID {
		t.Fatal("confirmation released member", active, err)
	}
	if err := adapter.RepairPermissions(ctx, manager, ticket.ID); err == nil {
		t.Fatal("expected another uncertain send")
	}
	next, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || next.AttemptID == input.AttemptID || next.ChannelDiscordID != "queue" {
		t.Fatal(next, err)
	}
	if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
		t.Fatal("stale confirmation cleared newer attempt", err)
	}
	if client.sends != 2 {
		t.Fatal("unexpected send", client.sends)
	}
}

// TestQueueReconciliationRejectsInvalidProofAndRollsBack checks invalid input,
// transport denial and event-write failure leave the durable fence untouched.
func TestQueueReconciliationRejectsInvalidProofAndRollsBack(t *testing.T) {
	db, _, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := tickets.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	for _, input := range []tickets.QueueRecoveryInput{
		{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID},
		{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, MessageURL: "link", ConfirmNotDelivered: true},
		{TicketID: ticket.ID, ConfirmNotDelivered: true},
	} {
		if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, tickets.ErrInvalidQueueReceipt) {
			t.Fatal(err)
		}
	}
	client.validationErr = tickets.ErrInvalidQueueReceipt
	if _, err := adapter.ReconcileQueue(ctx, manager, tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, MessageURL: "wrong-message"}); !errors.Is(err, tickets.ErrInvalidQueueReceipt) {
		t.Fatal(err)
	}
	want := errors.New("timeline write failed")
	if err := db.Callback().Create().Before("gorm:create").Register("reject_recovery_event", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_events" {
			tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ReconcileQueue(ctx, manager, tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}); !errors.Is(err, want) {
		t.Fatal(err)
	}
	current, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || current.AttemptID != ticket.QueueDeliveryAttemptID {
		t.Fatal("failed audit lost fence", current, err)
	}
}

// TestQueueReconciliationConcurrentConfirmation admits only one decision from
// concurrent clicks and emits exactly one corresponding immutable event.
func TestQueueReconciliationConcurrentConfirmation(t *testing.T) {
	_, service, adapter, _, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := tickets.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := adapter.ReconcileQueue(ctx, manager, input); errorsCh <- err }()
	}
	workers.Wait()
	close(errorsCh)
	succeeded := 0
	for err := range errorsCh {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatal("concurrent confirmation results", succeeded)
	}
	_, events, err := service.Detail(ctx, manager, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == tickets.EventQueueReconciled {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate recovery events", count)
	}
}

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
