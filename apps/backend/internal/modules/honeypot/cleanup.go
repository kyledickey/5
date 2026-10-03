package honeypot

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MessageCleaner deletes an already-qualified trigger without invoking moderation.
// Unknown messages count as successful cleanup, making lease recovery safe to replay.
type MessageCleaner interface {
	DeleteHoneypotMessage(context.Context, string, string) error
}

// ProcessCleanups drains a bounded batch of durable cleanup. Polling retries
// failures and recovers interrupted attempts after lease expiry.
func (s *Service) ProcessCleanups(ctx context.Context, limit int) error {
	if s == nil || s.store == nil {
		return errors.New("honeypot cleanup is not configured")
	}
	cleaner, ok := s.applier.(MessageCleaner)
	if !ok {
		return nil
	}
	var failures []error
	for range min(max(limit, 1), 25) {
		// Lease only the message about to be processed so slow Discord calls cannot
		// consume the leases of later messages in a batch.
		messages, err := s.store.claimCleanups(ctx, 1)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(messages) == 0 {
			break
		}
		message := messages[0]
		attemptCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		deleteErr := cleaner.DeleteHoneypotMessage(attemptCtx, message.ChannelDiscordID, message.MessageDiscordID)
		cancel()
		if err := s.store.finishCleanup(ctx, message, deleteErr == nil); err != nil {
			failures = append(failures, err)
		}
		if deleteErr != nil {
			failures = append(failures, deleteErr)
		}
	}
	return errors.Join(failures...)
}

// MessageCleanup preserves each non-exempt message independently of the debounced
// incident. A saved incident permits deletion; failed or pending cases retain
// their source messages. Completed records also deduplicate old gateway replays.
type MessageCleanup struct {
	ID                  string    `gorm:"type:char(26);primaryKey"`
	GuildID             string    `gorm:"type:char(26);not null;uniqueIndex:idx_honeypot_cleanup_message,priority:1"`
	MessageDiscordID    string    `gorm:"size:32;not null;uniqueIndex:idx_honeypot_cleanup_message,priority:2"`
	ChannelDiscordID    string    `gorm:"size:32;not null"`
	TargetDiscordUserID string    `gorm:"size:32;not null"`
	TriggerID           string    `gorm:"type:char(26);not null;index"`
	AttemptCount        uint      `gorm:"not null"`
	NextAttemptAt       time.Time `gorm:"not null;index"`
	CompletedAt         *time.Time
	CreatedAt           time.Time `gorm:"not null"`
}

// TableName isolates cleanup receipts from incident counts and moderation actions.
func (MessageCleanup) TableName() string { return "honeypot_message_cleanups" }

// scheduleCleanup runs inside the incident claim transaction so every accepted
// burst message has durable cleanup even if the process stops before case creation.
func (s *Store) scheduleCleanup(ctx context.Context, message Message, triggerID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&MessageCleanup{
		ID: ulid.Make().String(), GuildID: message.GuildID, MessageDiscordID: message.MessageDiscordID,
		ChannelDiscordID: message.ChannelDiscordID, TargetDiscordUserID: message.AuthorDiscordUserID,
		TriggerID: triggerID, NextAttemptAt: now, CreatedAt: now,
	}).Error
}

// claimCleanups leases due messages whose incident has finished evidence capture
// and saved its case. Atomic attempt increments fence stale completion/retry writes.
func (s *Store) claimCleanups(ctx context.Context, limit int) ([]MessageCleanup, error) {
	now := time.Now().UTC()
	var candidates []MessageCleanup
	err := s.db.WithContext(ctx).Table("honeypot_message_cleanups AS cleanup").Select("cleanup.*").
		Joins("JOIN honeypot_triggers AS incident ON incident.id = cleanup.trigger_id AND incident.guild_id = cleanup.guild_id").
		Where("cleanup.completed_at IS NULL AND cleanup.next_attempt_at <= ? AND incident.outcome = ? AND incident.case_id <> ''", now, OutcomeCreated).
		Order("cleanup.next_attempt_at ASC").Limit(limit).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	claimed := make([]MessageCleanup, 0, len(candidates))
	for _, candidate := range candidates {
		result := s.db.WithContext(ctx).Model(&MessageCleanup{}).
			Where("id = ? AND completed_at IS NULL AND next_attempt_at <= ? AND attempt_count = ?", candidate.ID, now, candidate.AttemptCount).
			Updates(map[string]any{"next_attempt_at": now.Add(30 * time.Second), "attempt_count": gorm.Expr("attempt_count + 1")})
		if result.Error != nil {
			return claimed, result.Error
		}
		if result.RowsAffected == 1 {
			candidate.AttemptCount++
			claimed = append(claimed, candidate)
		}
	}
	return claimed, nil
}

// finishCleanup records idempotent deletion or a capped retry delay. A stale
// worker cannot overwrite a newer attempt after its lease expired.
func (s *Store) finishCleanup(ctx context.Context, message MessageCleanup, succeeded bool) error {
	now := time.Now().UTC()
	values := map[string]any{"next_attempt_at": now.Add(time.Second * time.Duration(1<<min(message.AttemptCount-1, 6)))}
	if succeeded {
		values["completed_at"] = now
	}
	return s.db.WithContext(ctx).Model(&MessageCleanup{}).
		Where("id = ? AND attempt_count = ? AND completed_at IS NULL", message.ID, message.AttemptCount).Updates(values).Error
}
