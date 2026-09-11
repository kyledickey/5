package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimPendingAppealNotifications leases up to limit (1..100) outbox rows for
// one dispatcher with a two-minute expiry. Eligible rows are pending, claimed
// but expired, or deferred failures older than a minute. Rows still marked
// sending past their lease are first parked as failed with
// delivery_outcome_unknown: the send may have reached Discord, so they are
// never re-sent automatically.
func (s *Store) ClaimPendingAppealNotifications(ctx context.Context, limit int) ([]model.AppealNotification, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("appeal notification claim limit is invalid")
	}
	now := time.Now().UTC()
	token, err := idutil.NewULID()
	if err != nil {
		return nil, fmt.Errorf("create appeal notification lease token: %w", err)
	}
	expiresAt := now.Add(2 * time.Minute)
	var records []AppealNotificationRecord
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// An interrupted send has an unknown external outcome. Preserve it for
		// review instead of automatically creating a duplicate queue message or DM.
		if err := tx.Model(&AppealNotificationRecord{}).
			Where("status = ? AND lease_expires_at <= ?", model.AppealNotificationSending, now).
			Updates(map[string]any{
				"status":           model.AppealNotificationFailed,
				"last_error_code":  "delivery_outcome_unknown",
				"lease_token":      "",
				"lease_expires_at": nil,
				"updated_at":       now,
			}).Error; err != nil {
			return err
		}
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? OR (status = ? AND lease_expires_at <= ?) OR (status = ? AND last_error_code = ? AND updated_at <= ?)",
				model.AppealNotificationPending,
				model.AppealNotificationClaimed, now,
				model.AppealNotificationFailed, "delivery_deferred", now.Add(-time.Minute),
			).
			Order("created_at ASC").Limit(limit).Find(&records)
		if result.Error != nil || len(records) == 0 {
			return result.Error
		}
		ids := make([]string, 0, len(records))
		for index := range records {
			ids = append(ids, records[index].ID)
			records[index].RefreshRequested = false
			records[index].Status = model.AppealNotificationClaimed
			records[index].LeaseToken = token
			records[index].LeaseExpiresAt = &expiresAt
			records[index].UpdatedAt = now
		}
		result = tx.Model(&AppealNotificationRecord{}).Where("id IN ?", ids).Updates(map[string]any{
			"refresh_requested": false,
			"status":            model.AppealNotificationClaimed,
			"lease_token":       token,
			"lease_expires_at":  expiresAt,
			"updated_at":        now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(records)) {
			return model.ErrAppealStateConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]model.AppealNotification, 0, len(records))
	for _, record := range records {
		items = append(items, appealNotificationModel(record))
	}
	return items, nil
}

// CompleteAppealNotification records a sent or failed outcome for a row the
// caller holds in the sending state, fenced on the lease token. Delivery
// coordinates are only overwritten when non-empty. A sent row that had a
// refresh requested meanwhile returns to pending so the queue message is
// edited again.
func (s *Store) CompleteAppealNotification(ctx context.Context, params model.CompleteAppealNotificationParams) error {
	if params.Status != model.AppealNotificationSent && params.Status != model.AppealNotificationFailed {
		return errors.New("appeal notification completion status is invalid")
	}
	result := s.db.WithContext(ctx).Model(&AppealNotificationRecord{}).
		Where("id = ? AND status = ? AND lease_token = ?", params.NotificationID, model.AppealNotificationSending, params.LeaseToken).
		Updates(map[string]any{
			"status": gorm.Expr("CASE WHEN refresh_requested = ? AND ? = ? THEN ? ELSE ? END",
				true, params.Status, model.AppealNotificationSent, model.AppealNotificationPending, params.Status),
			"delivery_message_id": gorm.Expr("CASE WHEN ? <> '' THEN ? ELSE delivery_message_id END", params.DeliveryMessageID, params.DeliveryMessageID),
			"delivery_channel_id": gorm.Expr("CASE WHEN ? <> '' THEN ? ELSE delivery_channel_id END", params.DeliveryChannelID, params.DeliveryChannelID),
			"last_error_code":     params.ErrorCode,
			"lease_token":         "",
			"lease_expires_at":    nil,
			"updated_at":          time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return model.ErrAppealStateConflict
	}
	return nil
}

// BeginAppealNotificationDelivery fences the external send with the current
// unexpired lease. Claimed work can recover safely; sending work cannot.
func (s *Store) BeginAppealNotificationDelivery(ctx context.Context, id, token string) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&AppealNotificationRecord{}).
		Where("id = ? AND status = ? AND lease_token = ? AND lease_expires_at > ?", id, model.AppealNotificationClaimed, token, now).
		Updates(map[string]any{"status": model.AppealNotificationSending, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return model.ErrAppealStateConflict
	}
	return nil
}
