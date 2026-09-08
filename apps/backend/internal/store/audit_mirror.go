package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm/clause"
)

// auditMirrorDelivery is mutable transport bookkeeping, separate from immutable
// staff history. One receipt per event survives restarts without generating more events.
type auditMirrorDelivery struct {
	AuditEntryID string    `gorm:"type:char(26);primaryKey"`
	Finished     bool      `gorm:"not null"`
	RetryAt      time.Time `gorm:"not null;index"`
}

// SaveAuditMirrorDelivery records either completion or the next retry deadline.
// The single-process mirror serializes sends; a crash after Discord accepts a
// message but before this write can still duplicate that message on restart.
func (s *Store) SaveAuditMirrorDelivery(ctx context.Context, entryID string, finished bool, retryAt time.Time) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	if entryID == "" {
		return errors.New("audit entry ID is required")
	}
	receipt := auditMirrorDelivery{AuditEntryID: entryID, Finished: finished, RetryAt: retryAt.UTC()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "audit_entry_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"finished", "retry_at"}),
	}).Create(&receipt).Error
}

// ListPendingAuditMirrorEntries finds undelivered events whose retry delay has
// elapsed. Delivery receipts never participate in audit history or staff statistics.
func (s *Store) ListPendingAuditMirrorEntries(ctx context.Context, limit int) ([]model.AuditLogEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var entries []model.AuditLogEntry
	err := s.db.WithContext(ctx).
		Where("action IN ?", model.ImportantAuditActions()).
		Where("NOT EXISTS (SELECT 1 FROM audit_mirror_deliveries delivery WHERE delivery.audit_entry_id = audit_log_entries.id AND (delivery.finished = ? OR delivery.retry_at > ?))", true, time.Now().UTC()).
		Order("created_at ASC, id ASC").Limit(limit).Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("list pending audit mirror entries: %w", err)
	}
	return entries, nil
}
