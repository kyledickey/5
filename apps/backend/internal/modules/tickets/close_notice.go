package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrCloseNoticeNotSent marks a definite rejection, so a later close may retry.
var ErrCloseNoticeNotSent = errors.New("ticket close notice was not sent")

// closeNoticeState fences ambiguous sends across retries and process restarts.
// It lives in existing ticket metadata so older installations need no schema change.
type closeNoticeState struct {
	State     string `json:"state"`
	MessageID string `json:"message_id,omitempty"`
}

// updateCloseNotice locks the ticket while admitting delivery or saving its receipt.
// Other metadata keys are retained; an in-flight attempt only permits reconciliation.
func (s *Store) updateCloseNotice(ctx context.Context, ticket *Ticket, result *closeNoticeState) (closeNoticeState, error) {
	var notice closeNoticeState
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ticketRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID).First(&record).Error; err != nil {
			return err
		}
		metadata := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(record.MetadataJSON), &metadata); err != nil {
			return err
		}
		if metadata == nil {
			metadata = map[string]json.RawMessage{}
		}
		if raw := metadata["close_notice"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &notice); err != nil {
				return err
			}
		}
		saved := notice
		if result != nil && notice.State != "sent" {
			saved = *result
		} else if notice.State == "" || notice.State == "rejected" {
			saved.State = "pending"
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		metadata["close_notice"] = raw
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		return tx.Model(&record).Update("metadata_json", string(encoded)).Error
	})
	return notice, err
}

// deliverCloseNotice attempts one DM containing the close notice and transcript.
// Delivery failures do not trap members in unclosable tickets. Ambiguous sends
// retain their durable fence and may only be reconciled, never blindly repeated.
func (a *DiscordAdapter) deliverCloseNotice(ctx context.Context, actor Actor, ticket *Ticket) error {
	state, err := a.service.store.updateCloseNotice(ctx, ticket, nil)
	if err != nil {
		return err
	}
	if state.State == "sent" {
		ticket.CloseNoticeDelivered = true
		return nil
	}
	transcript, err := a.service.Transcript(ctx, actor, ticket.ID)
	if err != nil {
		return err
	}
	messageID, sendErr := a.client.DeliverTicketCloseNotice(ctx, ticket, transcript, state.State == "pending")
	result := closeNoticeState{State: "pending"}
	if sendErr == nil && messageID != "" {
		result.State, result.MessageID = "sent", messageID
	} else if errors.Is(sendErr, ErrCloseNoticeNotSent) {
		result.State = "rejected"
	}
	// A timed-out request must still retain its receipt or rejection classification.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := a.service.store.updateCloseNotice(saveCtx, ticket, &result); err != nil {
		return err
	}
	ticket.CloseNoticeDelivered = result.State == "sent"
	return nil
}
