package tickets_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

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
