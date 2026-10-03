package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// cancelVoidedCaseWork stops unstarted enforcement and notices while preserving
// inverse actions. The caller must hold the case lock until its transaction ends.
func cancelVoidedCaseWork(tx *gorm.DB, caseID string, now time.Time) error {
	unstarted := []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRetrying}
	if err := tx.Model(&model.CaseActionExecution{}).
		Where("case_id = ? AND reversal_of_execution_id IS NULL AND status IN ?", caseID, unstarted).
		Updates(map[string]any{
			"status":          model.ActionExecutionCancelled,
			"last_error_code": "case_voided",
			"last_error":      "case was voided before enforcement",
			"finished_at":     now,
			"next_retry_at":   nil,
		}).Error; err != nil {
		return err
	}
	unsent := []model.NotificationStatus{model.NotificationPending, model.NotificationPrepared, model.NotificationClaimed}
	if err := tx.Model(&model.CaseNotification{}).
		Where("case_id = ? AND status IN ?", caseID, unsent).
		Updates(map[string]any{
			"status":           model.NotificationFailed,
			"last_error_code":  "case_voided",
			"last_error":       "case was voided before notification",
			"lease_token":      "",
			"lease_expires_at": nil,
			"updated_at":       now,
		}).Error; err != nil {
		return err
	}
	return requestCasePublicationRefresh(tx, caseID, now)
}

// queueVoidedCaseReversals durably compensates known successful punishments in the
// same transaction as a void or late enforcement result. Uncertain failures stay
// in the review queue; a kick cannot be undone. The caller holds the case lock.
func queueVoidedCaseReversals(tx *gorm.DB, item model.Case, now time.Time) error {
	if item.Validity != model.CaseValidityVoided {
		return nil
	}
	var actions []model.CaseActionExecution
	reversible := []model.ActionType{model.ActionTimeoutUser, model.ActionBanUser}
	if err := tx.
		Where("case_id = ? AND status = ? AND reversal_of_execution_id IS NULL AND action_type IN ?", item.ID, model.ActionExecutionSucceeded, reversible).
		Find(&actions).Error; err != nil {
		return err
	}
	var appeal model.Appeal
	if err := tx.Where("case_id = ? AND status = ?", item.ID, model.AppealStatusAccepted).Limit(1).Find(&appeal).Error; err != nil {
		return err
	}
	var appealID *string
	if appeal.ID != "" {
		appealID = &appeal.ID
	}
	for _, original := range actions {
		inverse := model.ActionRemoveTimeout
		if original.ActionType == model.ActionBanUser {
			inverse = model.ActionUnbanUser
		}
		var queued model.CaseActionExecution
		params := model.QueueCaseReversalParams{
			GuildID:             item.GuildID,
			CaseID:              item.ID,
			OriginalExecutionID: original.ID,
			ActionType:          inverse,
			ActorDiscordUserID:  item.VoidedByDiscordUserID,
			AppealID:            appealID,
		}
		if err := queueCaseReversal(tx, params, now, &queued); err != nil {
			return err
		}
	}
	return nil
}
