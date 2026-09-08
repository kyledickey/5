package store

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SaveCasePublication records a delivered public message. Repeating the write is
// safe, and preserves its original immutable presentation and refresh progress.
// Discord delivery and this write cannot share a transaction: a process crash
// after send but before save can leave an untracked public message.
func (s *Store) SaveCasePublication(ctx context.Context, receipt model.CasePublication) error {
	if receipt.MessageID == "" || receipt.ChannelID == "" || receipt.CaseID == "" {
		return errors.New("case publication identifiers are required")
	}
	receipt.RefreshRequested = true
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error
}

// ListDueCasePublications resumes receipt refreshes after downtime. Terminal
// receipts sleep until a source transaction requests another refresh.
func (s *Store) ListDueCasePublications(ctx context.Context, now time.Time, limit int) ([]model.CasePublication, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []model.CasePublication
	err := s.db.WithContext(ctx).Where("refresh_requested = ? AND retry_at <= ?", true, now.UTC()).Order("retry_at ASC, message_id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// CompleteCasePublicationRefresh stores the last successfully rendered digest
// and next attempt only for the revision read by the worker. A newer source
// change retains its request even when an old edit or failed attempt finishes.
func (s *Store) CompleteCasePublicationRefresh(ctx context.Context, messageID string, revision uint64, digest string, retryAt time.Time, requested bool) error {
	result := s.db.WithContext(ctx).Model(&model.CasePublication{}).Where("message_id = ? AND revision = ?", messageID, revision).Updates(map[string]any{"last_digest": digest, "retry_at": retryAt.UTC(), "refresh_requested": requested})
	if result.Error != nil || result.RowsAffected > 0 {
		return result.Error
	}
	// A stale Discord edit may finish after a newer worker already went idle.
	// Advance revision so another in-flight completion cannot clear this repair.
	// Request repair without copying the obsolete digest or retry deadline. Never
	// recreate a receipt that was retired for an explicitly missing message.
	return s.db.WithContext(ctx).Model(&model.CasePublication{}).Where("message_id = ? AND revision <> ?", messageID, revision).Updates(map[string]any{"refresh_requested": true, "last_digest": "", "retry_at": time.Now().UTC(), "revision": gorm.Expr("revision + 1")}).Error
}

// DeleteCasePublication retires only a definitively missing message or case.
func (s *Store) DeleteCasePublication(ctx context.Context, messageID string) error {
	return s.db.WithContext(ctx).Where("message_id = ?", messageID).Delete(&model.CasePublication{}).Error
}

// CasePublicationEvidenceIncomplete reads only the public capture-health signal.
// Staff evidence content and attachment coordinates never cross this boundary.
func (s *Store) CasePublicationEvidenceIncomplete(ctx context.Context, caseID string) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.CaseEvidenceSnapshot{}).Where("case_id = ? AND (capture_warning <> '' OR capture_outcome = ?)", caseID, "unavailable").Count(&count).Error
	return count > 0, err
}

// requestCasePublicationRefresh participates in the caller's source transaction.
// Missing receipts need no marker: registration always starts requested. Keeping
// this write atomic prevents a committed moderation change losing its refresh on
// process failure; revision fencing prevents stale completion clearing newer work.
// Clear the digest too: a stale in-flight Discord edit may change the message
// after the source returns to its former value, so that value must be resent.
func requestCasePublicationRefresh(tx *gorm.DB, caseID string, now time.Time) error {
	return tx.Model(&model.CasePublication{}).Where("case_id = ?", caseID).Updates(map[string]any{"refresh_requested": true, "last_digest": "", "revision": gorm.Expr("revision + 1"), "retry_at": now.UTC()}).Error
}
