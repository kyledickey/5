package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// auditMirrorDelivery is mutable transport bookkeeping, separate from immutable
// staff history. One receipt per event survives restarts without generating more events.
type auditMirrorDelivery struct {
	AuditEntryID string    `gorm:"type:char(26);primaryKey;index:idx_audit_mirror_due,priority:3"`
	Finished     bool      `gorm:"not null;index:idx_audit_mirror_due,priority:1"`
	RetryAt      time.Time `gorm:"not null;index;index:idx_audit_mirror_due,priority:2"`
}

// SaveAuditMirrorDelivery records either completion or the next retry deadline.
// The single-process mirror serializes sends; a crash after Discord accepts a
// message but before this write can still duplicate that message on restart.
func (s *Store) SaveAuditMirrorDelivery(ctx context.Context, entryID string, finished bool, retryAt time.Time) error {
	if entryID == "" {
		return errors.New("audit entry ID is required")
	}
	receipt := auditMirrorDelivery{AuditEntryID: entryID, Finished: finished, RetryAt: retryAt.UTC()}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "audit_entry_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"finished", "retry_at"}),
	}).Create(&receipt).Error
}

// ListPendingAuditMirrorEntries reads only the indexed due queue, never scans
// completed audit history. Initial events follow creation time; retries follow
// their next deadline. Each poll examines at most four bounded batches, retiring
// orphaned or no-longer-important receipts so they cannot permanently block work.
func (s *Store) ListPendingAuditMirrorEntries(ctx context.Context, limit int) ([]model.AuditLogEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	now := time.Now().UTC()
	for batch := 0; batch < 4; batch++ {
		var receipts []auditMirrorDelivery
		if err := s.db.WithContext(ctx).
			Where("finished = ? AND retry_at <= ?", false, now).
			Order("retry_at ASC, audit_entry_id ASC").
			Limit(limit).
			Find(&receipts).Error; err != nil {
			return nil, fmt.Errorf("list due mirror deliveries: %w", err)
		}
		if len(receipts) == 0 {
			return nil, nil
		}
		ids := make([]string, len(receipts))
		for i, receipt := range receipts {
			ids[i] = receipt.AuditEntryID
		}
		var entries []model.AuditLogEntry
		if err := s.db.WithContext(ctx).Where("id IN ? AND action IN ?", ids, model.ImportantAuditActions()).Find(&entries).Error; err != nil {
			return nil, fmt.Errorf("load pending audit entries: %w", err)
		}
		byID := make(map[string]model.AuditLogEntry, len(entries))
		for _, entry := range entries {
			byID[entry.ID] = entry
		}
		ordered := make([]model.AuditLogEntry, 0, len(entries))
		var obsolete []string
		for _, receipt := range receipts {
			if entry, ok := byID[receipt.AuditEntryID]; ok {
				ordered = append(ordered, entry)
			} else {
				obsolete = append(obsolete, receipt.AuditEntryID)
			}
		}
		if len(obsolete) > 0 {
			if err := s.db.WithContext(ctx).Model(&auditMirrorDelivery{}).
				Where("audit_entry_id IN ? AND finished = ?", obsolete, false).
				Update("finished", true).Error; err != nil {
				return nil, fmt.Errorf("retire obsolete mirror deliveries: %w", err)
			}
		}
		if len(ordered) > 0 {
			return ordered, nil
		}
	}
	return nil, nil
}

// initializeAuditMirrorQueue backfills missing delivery rows once under the
// startup migration lock. INSERT SELECT keeps historical materialization in SQL;
// existing completions and retry deadlines are never overwritten. Readiness and
// inserted rows commit together, making interrupted initialization safe to retry.
func initializeAuditMirrorQueue(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var marker currentSchema
		if err := tx.First(&marker, 1).Error; err != nil {
			return err
		}
		if marker.AuditMirrorQueueReady {
			return nil
		}
		query := `INSERT INTO audit_mirror_deliveries (audit_entry_id, finished, retry_at)
   SELECT audit_log_entries.id, ?, audit_log_entries.created_at FROM audit_log_entries
   LEFT JOIN audit_mirror_deliveries existing ON existing.audit_entry_id = audit_log_entries.id
   WHERE existing.audit_entry_id IS NULL AND audit_log_entries.action IN ?`
		if tx.Dialector.Name() == "mysql" {
			query += " ON DUPLICATE KEY UPDATE audit_entry_id = audit_mirror_deliveries.audit_entry_id"
		} else {
			query += " ON CONFLICT(audit_entry_id) DO NOTHING"
		}
		if err := tx.Exec(query, false, model.ImportantAuditActions()).Error; err != nil {
			return fmt.Errorf("backfill audit mirror queue: %w", err)
		}
		return tx.Model(&currentSchema{}).Where("id = ?", 1).Update("audit_mirror_queue_ready", true).Error
	})
}
