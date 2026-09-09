package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInvalidQueueReceipt rejects incomplete recovery decisions and messages that
// the Discord transport cannot verify as this ticket's bot-authored queue post.
var ErrInvalidQueueReceipt = errors.New("ticket queue recovery requires a verified message or explicit nondelivery confirmation")

// QueueRecovery identifies one uncertain send for a manager's private inspection.
// AttemptID must be returned unchanged; channel identity alone cannot distinguish
// a stale confirmation from a later retry to the same destination.
type QueueRecovery struct {
	TicketID         string
	ChannelDiscordID string
	AttemptID        string
}

// QueueRecoveryInput supplies exactly one explicit recovery decision. A message
// link adopts existing delivery; ConfirmNotDelivered permits a later ordinary
// repair or close to send again without releasing the member's reservation.
type QueueRecoveryInput struct {
	TicketID            string
	AttemptID           string
	MessageURL          string
	ConfirmNotDelivered bool
}

// QueueRecovery returns the current uncertain send to a guild manager. Older
// destination-only fences receive an attempt identity without being released.
// Sharing the close lock prevents inspection of this adapter's in-flight send.
func (a *DiscordAdapter) QueueRecovery(ctx context.Context, actor Actor, ticketID string) (*QueueRecovery, error) {
	if !actor.CanManage {
		return nil, ErrPermissionDenied
	}
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+ticketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, err := a.service.store.queueRecoveryTicket(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	return &QueueRecovery{TicketID: ticket.ID, ChannelDiscordID: ticket.LogChannelDiscordID, AttemptID: ticket.QueueDeliveryAttemptID}, nil
}

// ReconcileQueue records a manager's inspected delivery decision for exactly one
// uncertain attempt. It never sends, deletes the source, or releases its owner.
// Adoption leaves the transcript URL empty so normal closure still publishes the
// canonical retained transcript before source cleanup.
func (a *DiscordAdapter) ReconcileQueue(ctx context.Context, actor Actor, input QueueRecoveryInput) (*Ticket, error) {
	if !actor.CanManage {
		return nil, ErrPermissionDenied
	}
	input.MessageURL = strings.TrimSpace(input.MessageURL)
	if input.TicketID == "" || input.AttemptID == "" || (input.MessageURL != "") == input.ConfirmNotDelivered {
		return nil, ErrInvalidQueueReceipt
	}
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+input.TicketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, err := a.service.store.queueRecoveryTicket(ctx, actor.GuildID, input.TicketID)
	if err != nil {
		return nil, err
	}
	if ticket.QueueDeliveryAttemptID != input.AttemptID {
		return nil, ErrQueueDeliveryUnknown
	}
	var receipt *QueueReceipt
	if input.MessageURL != "" {
		receipt, err = a.client.ValidateTicketQueueMessage(ctx, ticket, input.MessageURL)
		if err != nil {
			return nil, err
		}
		if receipt == nil || receipt.MessageID == "" {
			return nil, ErrInvalidQueueReceipt
		}
	}
	ticket, err = a.service.store.reconcileQueue(ctx, actor, input, receipt, a.service.now())
	if err != nil {
		a.service.audit(ctx, actor, "ticket.queue.reconciled", input.TicketID, "failure", err)
		return nil, err
	}
	// The immutable ticket event commits with the decision. The existing module
	// auditor is a separate port and retains its usual post-commit best effort.
	a.service.audit(ctx, actor, "ticket.queue.reconciled", input.TicketID, "success", nil)
	return ticket, nil
}

// lockedQueueRecoveryTicket checks both lifecycle and the owner reservation in
// the mutation transaction, preventing recovery of a completed or superseded ticket.
func lockedQueueRecoveryTicket(tx *gorm.DB, guildID, ticketID string) (*ticketRecord, error) {
	var record ticketRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND id = ?", guildID, ticketID).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if record.Status != StatusOpen && record.Status != StatusResolved {
		return nil, ErrInvalidTransition
	}
	var state memberStateRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND owner_discord_user_id = ?", guildID, record.OwnerDiscordUserID).Limit(1).Find(&state).Error; err != nil {
		return nil, err
	}
	if state.OpenTicketID != ticketID {
		return nil, ErrInvalidTransition
	}
	return &record, nil
}

// queueRecoveryTicket bootstraps legacy fences under a row lock so repeated
// inspection keeps a stable token and never admits another send by itself.
func (s *Store) queueRecoveryTicket(ctx context.Context, guildID, ticketID string) (*Ticket, error) {
	var out Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockedQueueRecoveryTicket(tx, guildID, ticketID)
		if err != nil {
			return err
		}
		if record.LogChannelDiscordID == "" || record.LogMessageDiscordID != "" {
			return ErrInvalidTransition
		}
		if record.QueueDeliveryAttemptID == "" {
			record.QueueDeliveryAttemptID = ulid.Make().String()
			if err := tx.Model(record).Update("queue_delivery_attempt_id", record.QueueDeliveryAttemptID).Error; err != nil {
				return err
			}
		}
		out = ticketFromRecord(*record)
		return nil
	})
	return &out, err
}

// reconcileQueue commits one token-matched decision and its semantic timeline
// event atomically. Even a stale same-channel confirmation cannot clear a newer send.
func (s *Store) reconcileQueue(ctx context.Context, actor Actor, input QueueRecoveryInput, receipt *QueueReceipt, now time.Time) (*Ticket, error) {
	var out Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockedQueueRecoveryTicket(tx, actor.GuildID, input.TicketID)
		if err != nil {
			return err
		}
		if record.QueueDeliveryAttemptID != input.AttemptID || record.LogChannelDiscordID == "" || record.LogMessageDiscordID != "" {
			return ErrQueueDeliveryUnknown
		}
		channelID, messageID, decision := record.LogChannelDiscordID, "", "confirmed_not_delivered"
		if receipt != nil {
			messageID, decision = receipt.MessageID, "adopted_existing_message"
		} else {
			channelID = ""
		}
		result := tx.Model(&ticketRecord{}).Where("id = ? AND guild_id = ? AND queue_delivery_attempt_id = ?", record.ID, record.GuildID, input.AttemptID).
			Updates(map[string]any{"queue_delivery_attempt_id": "", "log_channel_discord_id": channelID, "log_message_discord_id": messageID, "transcript_url": "", "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrQueueDeliveryUnknown
		}
		metadata, _ := json.Marshal(map[string]string{"decision": decision, "attempt_id": input.AttemptID, "channel_id": record.LogChannelDiscordID, "message_id": messageID})
		if err := appendEvent(tx, *record, EventQueueReconciled, actor.DiscordUserID, "Staff queue delivery reconciled", string(metadata), now); err != nil {
			return err
		}
		record.QueueDeliveryAttemptID, record.LogChannelDiscordID, record.LogMessageDiscordID, record.TranscriptURL = "", channelID, messageID, ""
		record.UpdatedAt = now
		out = ticketFromRecord(*record)
		return nil
	})
	return &out, err
}
