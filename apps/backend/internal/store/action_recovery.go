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

// ListFailedCaseActions returns the active staff-review queue with stable newest-first ordering.
func (s *Store) ListFailedCaseActions(ctx context.Context, filter model.FailedCaseActionFilter) (*model.FailedCaseActionResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := s.db.WithContext(ctx).Model(&model.CaseActionExecution{}).Where("status = ? AND dismissed_at IS NULL AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", model.ActionExecutionFailed, filter.GuildID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []model.CaseActionExecution
	if err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, err
	}
	return &model.FailedCaseActionResult{Executions: items, Total: total}, nil
}

// RetryCaseAction requeues the same action after live service authorization.
// Reversals retain the new requester for a second permission check by the worker.
func (s *Store) RetryCaseAction(ctx context.Context, params model.RetryCaseActionParams) (*model.CaseActionExecution, error) {
	return s.controlCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, item *model.CaseActionExecution, now time.Time) error {
		var caseRecord model.Case
		if err := tx.Where("id = ?", item.CaseID).First(&caseRecord).Error; err != nil {
			return err
		}
		if caseRecord.Validity == model.CaseValidityVoided && item.ReversalOfExecutionID == nil {
			return errors.New("a voided case's punishment cannot be retried")
		}
		if item.Status == model.ActionExecutionPending || item.Status == model.ActionExecutionRetrying {
			return nil
		}
		if item.Status != model.ActionExecutionFailed {
			return errors.New("action is not failed")
		}
		if item.ReversalOfExecutionID != nil {
			item.ConfigSnapshotJSON = marshalJSONObject(map[string]any{"requested_by": params.ActorDiscordUserID})
		}
		item.Status = model.ActionExecutionPending
		item.NextRetryAt = nil
		item.StartedAt = nil
		item.FinishedAt = nil
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		item.DismissedAt = nil
		item.DismissedByDiscordUserID = ""
		item.LastErrorCode = ""
		item.LastError = ""
		if err := tx.Select("*").Save(item).Error; err != nil {
			return err
		}
		event := model.CaseEvent{CaseID: item.CaseID, EventType: model.CaseEventActionRetried, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: model.EventVisibilityStaff, Body: "Action retry requested", MetadataJSON: marshalJSONObject(map[string]any{"execution_id": item.ID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return createOptionalActionControlAudit(tx, params.Audit, item.ID, now)
	}, params.Audit)
}

// DismissCaseAction removes a failed action from the active queue without deleting its attempts.
func (s *Store) DismissCaseAction(ctx context.Context, params model.DismissCaseActionParams) (*model.CaseActionExecution, error) {
	return s.controlCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, item *model.CaseActionExecution, now time.Time) error {
		if item.DismissedAt != nil {
			return nil
		}
		if item.Status != model.ActionExecutionFailed {
			return errors.New("action is not failed")
		}
		item.DismissedAt = &now
		item.DismissedByDiscordUserID = params.ActorDiscordUserID
		if err := tx.Select("*").Save(item).Error; err != nil {
			return err
		}
		event := model.CaseEvent{CaseID: item.CaseID, EventType: model.CaseEventActionDismissed, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: model.EventVisibilityStaff, Body: "Action failure dismissed", MetadataJSON: marshalJSONObject(map[string]any{"execution_id": item.ID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return createOptionalActionControlAudit(tx, params.Audit, item.ID, now)
	}, params.Audit)
}

// controlCaseAction serializes one guild-scoped action control mutation.
func (s *Store) controlCaseAction(ctx context.Context, guildID, executionID string, mutate func(*gorm.DB, *model.CaseActionExecution, time.Time) error, audit *model.AuditLogEntry) (*model.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var item model.CaseActionExecution
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Match void and claim lock order so a retry cannot cross a case void.
		var caseRecord model.Case
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND id IN (SELECT case_id FROM case_action_executions WHERE id = ?)", guildID, executionID).First(&caseRecord).Error; err != nil {
			return err
		}
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", executionID, guildID).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		return mutate(tx, &item, now)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// createOptionalActionControlAudit appends caller-provided audit evidence inside the control transaction.
func createOptionalActionControlAudit(tx *gorm.DB, audit *model.AuditLogEntry, resourceID string, now time.Time) error {
	if audit == nil {
		return nil
	}
	entry := *audit
	entry.ResourceID = resourceID
	return createAuditLogEntry(tx, &entry, now)
}

// QueueCaseReversal appends one explicit reversal execution linked to the original succeeded enforcement.
func (s *Store) QueueCaseReversal(ctx context.Context, params model.QueueCaseReversalParams) (*model.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var reversal model.CaseActionExecution
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var caseRecord model.Case
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", params.CaseID, params.GuildID).First(&caseRecord).Error; err != nil {
			return err
		}
		return queueCaseReversal(tx, params, now, &reversal)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &reversal, nil
}

// queueCaseReversal appends one inverse inside a transaction that already holds
// the case lock. Its stable key prevents duplicate reversal work across callers.
func queueCaseReversal(tx *gorm.DB, params model.QueueCaseReversalParams, now time.Time, reversal *model.CaseActionExecution) error {
	var original model.CaseActionExecution
	result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND case_id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", params.OriginalExecutionID, params.CaseID, params.GuildID).First(&original)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return gorm.ErrRecordNotFound
	}
	if result.Error != nil {
		return result.Error
	}
	if original.Status != model.ActionExecutionSucceeded {
		return errors.New("only a succeeded action can be reversed")
	}
	valid := (original.ActionType == model.ActionTimeoutUser && params.ActionType == model.ActionRemoveTimeout) || (original.ActionType == model.ActionBanUser && params.ActionType == model.ActionUnbanUser)
	if !valid {
		return errors.New("reversal does not match original action")
	}
	if params.AppealID != nil {
		var count int64
		if err := tx.Model(&model.Appeal{}).Where("id = ? AND case_id = ? AND status = ?", *params.AppealID, params.CaseID, model.AppealStatusAccepted).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("reversal appeal is not accepted for this case")
		}
	}
	var maxPosition int
	if err := tx.Model(&model.CaseActionExecution{}).Where("case_id = ?", params.CaseID).Select("COALESCE(MAX(position), -1)").Scan(&maxPosition).Error; err != nil {
		return fmt.Errorf("find reversal position: %w", err)
	}
	originalID := original.ID
	*reversal = model.CaseActionExecution{CaseID: params.CaseID, Position: maxPosition + 1, ActionType: params.ActionType, Status: model.ActionExecutionPending, IdempotencyKey: fmt.Sprintf("case:%s:reversal:%s:%s", params.CaseID, original.ID, params.ActionType), ConfigSnapshotJSON: marshalJSONObject(map[string]any{"requested_by": params.ActorDiscordUserID}), SafeForRetry: false, Irreversible: true, ReversalOfExecutionID: &originalID, ReversalAppealID: params.AppealID}
	var existing model.CaseActionExecution
	existingResult := tx.Where("idempotency_key = ?", reversal.IdempotencyKey).First(&existing)
	if existingResult.Error == nil {
		*reversal = existing
		return nil
	} else if !errors.Is(existingResult.Error, gorm.ErrRecordNotFound) {
		return existingResult.Error
	}
	if err := prepareULIDModel(&reversal.ULIDModel, now); err != nil {
		return err
	}
	if err := tx.Select("*").Create(reversal).Error; err != nil {
		return fmt.Errorf("queue reversal: %w", err)
	}
	event := model.CaseEvent{CaseID: params.CaseID, EventType: model.CaseEventReversalQueued, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: model.EventVisibilityStaff, Body: "Action reversal queued", MetadataJSON: marshalJSONObject(map[string]any{"original_execution_id": original.ID, "reversal_execution_id": reversal.ID})}
	if err := appendCaseEvent(tx, &event, now); err != nil {
		return err
	}
	return createOptionalActionControlAudit(tx, params.Audit, reversal.ID, now)
}

// PrepareCaseNotification durably records a DM channel opened before kick or ban enforcement.
func (s *Store) PrepareCaseNotification(ctx context.Context, caseID, channelID, errorMessage string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	updates := map[string]any{"prepared_channel_discord_id": channelID, "updated_at": time.Now().UTC()}
	if channelID != "" {
		updates["status"] = model.NotificationPrepared
	} else {
		updates["last_error_code"] = "dm_prepare_failed"
		updates["last_error"] = errorMessage
	}
	return s.db.WithContext(ctx).Model(&model.CaseNotification{}).Where("case_id = ? AND status = ?", caseID, model.NotificationPending).Updates(updates).Error
}

// ClaimCaseNotification claims at most one delivery after enforcement reaches a terminal outcome.
func (s *Store) ClaimCaseNotification(ctx context.Context, params model.ClaimCaseNotificationParams) (*model.CaseNotification, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var claimed *model.CaseNotification
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&model.CaseActionExecution{}).Where("case_id = ? AND status IN ?", params.CaseID, []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		var item model.CaseNotification
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("case_id = ? AND (status IN ? OR (status = ? AND lease_expires_at <= ?))", params.CaseID, []model.NotificationStatus{model.NotificationPending, model.NotificationPrepared}, model.NotificationClaimed, now).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		token, err := idutil.NewULID()
		if err != nil {
			return err
		}
		expires := now.Add(2 * time.Minute)
		item.Status = model.NotificationClaimed
		item.AttemptCount++
		item.LeaseToken = token
		item.LeaseExpiresAt = &expires
		item.UpdatedAt = now
		if err := tx.Select("*").Save(&item).Error; err != nil {
			return err
		}
		claimed = &item
		return nil
	})
	return claimed, err
}

// BeginCaseNotificationDelivery crosses the final durable fence immediately before the external send.
func (s *Store) BeginCaseNotificationDelivery(ctx context.Context, notificationID, leaseToken string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	result := s.db.WithContext(ctx).Model(&model.CaseNotification{}).Where("id = ? AND lease_token = ? AND status = ?", notificationID, leaseToken, model.NotificationClaimed).Updates(map[string]any{"status": model.NotificationSending, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("case notification lease is stale")
	}
	return nil
}

// CompleteCaseNotification applies a fenced terminal result; stale workers cannot overwrite a later decision.
func (s *Store) CompleteCaseNotification(ctx context.Context, params model.CompleteCaseNotificationParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.CaseNotification
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND lease_token = ? AND status = ?", params.NotificationID, params.LeaseToken, model.NotificationSending).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return errors.New("case notification lease is stale")
		}
		if result.Error != nil {
			return result.Error
		}
		item.Status = params.Status
		item.PreparedChannelDiscordID = params.PreparedChannelDiscordID
		item.RenderedMessage = params.RenderedMessage
		item.DeliveryMessageDiscordID = params.DeliveryMessageDiscordID
		item.LastErrorCode = params.ErrorCode
		item.LastError = params.ErrorMessage
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		item.UpdatedAt = now
		if params.Status == model.NotificationSent {
			item.SentAt = &now
		}
		if err := tx.Select("*").Save(&item).Error; err != nil {
			return err
		}
		event := model.CaseEvent{CaseID: item.CaseID, EventType: params.EventType, ActorType: "system", Visibility: model.EventVisibilityPublic, Body: "Member notification delivery updated", MetadataJSON: marshalJSONObject(map[string]any{"status": params.Status})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		var caseModel model.Case
		if err := tx.Where("id = ?", item.CaseID).First(&caseModel).Error; err != nil {
			return err
		}
		auditResult := model.AuditResultSuccess
		action := "case_notification.sent"
		if params.Status != model.NotificationSent {
			auditResult = model.AuditResultFailure
			action = "case_notification.failed"
		}
		return createAuditLogEntry(tx, &model.AuditLogEntry{GuildID: caseModel.GuildID, Source: model.AuditSourceSystem, Action: action, ResourceType: "case_notification", ResourceID: item.ID, Result: auditResult, FailureReason: params.ErrorMessage, CorrelationID: caseModel.CorrelationID, MetadataJSON: marshalJSONObject(map[string]any{"case_id": caseModel.ID, "status": params.Status})}, now)
	})
}
