package store

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
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
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&receipt).Error
}

// ListDueCasePublications resumes receipt refreshes after downtime. Terminal
// receipts remain eligible so later reversals and manual recovery are reflected.
func (s *Store) ListDueCasePublications(ctx context.Context, now time.Time, limit int) ([]model.CasePublication, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []model.CasePublication
	err := s.db.WithContext(ctx).Where("retry_at <= ?", now.UTC()).Order("retry_at ASC, message_id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

// CompleteCasePublicationRefresh stores the last successfully rendered digest
// and next attempt. An unsuccessful edit retains its prior digest for retry.
func (s *Store) CompleteCasePublicationRefresh(ctx context.Context, messageID, digest string, retryAt time.Time) error {
	return s.db.WithContext(ctx).Model(&model.CasePublication{}).Where("message_id = ?", messageID).Updates(map[string]any{"last_digest": digest, "retry_at": retryAt.UTC()}).Error
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
