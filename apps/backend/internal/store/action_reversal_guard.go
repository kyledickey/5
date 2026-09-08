package store

import (
	"context"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// LoadCaseReversalProvenance reads the original outcome and conservatively finds
// competing same-kind enforcement for the same guild/member. Failed newer work
// can have an uncertain external outcome and therefore also requires review.
// This is an observation, not a lock held across Discord network operations.
func (s *Store) LoadCaseReversalProvenance(ctx context.Context, guildID, caseID, executionID string) (*model.CaseActionExecution, string, bool, error) {
	var item model.Case
	if err := s.db.WithContext(ctx).Where("id = ? AND guild_id = ?", caseID, guildID).First(&item).Error; err != nil {
		return nil, "", false, err
	}
	var original model.CaseActionExecution
	if err := s.db.WithContext(ctx).Where("id = ? AND case_id = ?", executionID, caseID).First(&original).Error; err != nil {
		return nil, "", false, err
	}
	var attempt model.CaseActionAttempt
	err := s.db.WithContext(ctx).Where("execution_id = ? AND status = ?", executionID, model.ActionAttemptSucceeded).Order("attempt_number DESC").Limit(1).Find(&attempt).Error
	if err != nil {
		return nil, "", false, err
	}
	if original.Status != model.ActionExecutionSucceeded {
		return &original, attempt.ResponsePayloadJSON, false, nil
	}
	since := original.CreatedAt
	if original.StartedAt != nil {
		since = *original.StartedAt
	}
	active := []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying}
	possible := []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying, model.ActionExecutionSucceeded, model.ActionExecutionFailed}
	var competing model.CaseActionExecution
	err = s.db.WithContext(ctx).Select("id").Where("id <> ? AND action_type = ? AND reversal_of_execution_id IS NULL AND case_id IN (SELECT id FROM cases WHERE guild_id = ? AND target_discord_user_id = ?)", executionID, original.ActionType, guildID, item.TargetDiscordUserID).Where("status IN ? AND (status IN ? OR created_at >= ? OR started_at >= ?)", possible, active, since, since).Limit(1).Find(&competing).Error
	return &original, attempt.ResponsePayloadJSON, competing.ID != "", err
}
