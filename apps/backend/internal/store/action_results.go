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

// CompleteCaseAction persists the terminal or retry outcome for case action.
func (s *Store) CompleteCaseAction(ctx context.Context, params CompleteCaseActionParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize completion with void/claim using the same case-first order.
		var caseRecord model.Case
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN (SELECT case_id FROM case_action_executions WHERE id = ?)", params.ExecutionID).Limit(1).Find(&caseRecord).Error; err != nil {
			return err
		}
		var execution model.CaseActionExecution
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", params.ExecutionID)
		if params.LeaseToken != "" {
			query = query.Where("lease_token = ?", params.LeaseToken)
		}
		result := query.
			Limit(1).
			Find(&execution)
		if result.Error != nil {
			return fmt.Errorf("get case action execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			if params.LeaseToken != "" {
				return errors.New("case action lease is stale")
			}
			return nil
		}

		if params.RequestPayloadJSON == "" {
			params.RequestPayloadJSON = "{}"
		}
		if params.ResponsePayloadJSON == "" {
			params.ResponsePayloadJSON = "{}"
		}

		startedAt := now
		if execution.StartedAt != nil {
			startedAt = *execution.StartedAt
		}
		attemptNumber := params.AttemptNumber
		if attemptNumber == 0 {
			attemptNumber = execution.AttemptCount
		}
		var attempt model.CaseActionAttempt
		attemptResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("execution_id = ? AND attempt_number = ?", execution.ID, attemptNumber).First(&attempt)
		if errors.Is(attemptResult.Error, gorm.ErrRecordNotFound) {
			attempt = model.CaseActionAttempt{ExecutionID: execution.ID, AttemptNumber: attemptNumber, StartedAt: startedAt, WorkerID: params.WorkerID}
			if err := prepareULIDModel(&attempt.ULIDModel, now); err != nil {
				return err
			}
		} else if attemptResult.Error != nil {
			return attemptResult.Error
		}
		attempt.Status = params.AttemptStatus
		attempt.FinishedAt = &now
		attempt.DurationMS = now.Sub(attempt.StartedAt).Milliseconds()
		attempt.ErrorCode = params.ErrorCode
		attempt.ErrorMessage = params.ErrorMessage
		attempt.RequestPayloadJSON = params.RequestPayloadJSON
		attempt.ResponsePayloadJSON = params.ResponsePayloadJSON
		attempt.UpdatedAt = now
		if err := tx.Select("*").Save(&attempt).Error; err != nil {
			return fmt.Errorf("complete case action attempt: %w", err)
		}

		// A failure finishing after a void must not resurrect automatic enforcement.
		if caseRecord.Validity == model.CaseValidityVoided && execution.ReversalOfExecutionID == nil && params.ExecutionStatus == model.ActionExecutionRetrying {
			params.ExecutionStatus = model.ActionExecutionFailed
			params.NextRetryAt = nil
			params.EventBody = "Enforcement failed after the case was voided; review the outcome"
		}
		execution.Status = params.ExecutionStatus
		execution.LastErrorCode = params.ErrorCode
		execution.LastError = params.ErrorMessage
		execution.FinishedAt = &now
		execution.NextRetryAt = params.NextRetryAt
		execution.LeaseToken = ""
		execution.LeaseExpiresAt = nil
		execution.UpdatedAt = now
		if params.ExecutionStatus == model.ActionExecutionRetrying {
			execution.FinishedAt = nil
		}
		if err := tx.Select("*").Save(&execution).Error; err != nil {
			return fmt.Errorf("update case action execution: %w", err)
		}

		if params.EventType != "" {
			event := model.CaseEvent{
				CaseID:       execution.CaseID,
				EventType:    params.EventType,
				ActorType:    "system",
				Visibility:   model.EventVisibilityPublic,
				Body:         params.EventBody,
				MetadataJSON: params.EventMetadataJSON,
			}
			if event.MetadataJSON == "" {
				event.MetadataJSON = "{}"
			}
			if err := appendCaseEvent(tx, &event, now); err != nil {
				return err
			}
		}

		if err := queueVoidedCaseReversals(tx, caseRecord, now); err != nil {
			return err
		}
		if err := createCaseActionAudit(tx, execution, params, now); err != nil {
			return err
		}

		return nil
	})
}

// SkipCaseActions marks case actions as skipped when policy prevents further execution.
func (s *Store) SkipCaseActions(ctx context.Context, params SkipCaseActionsParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var executions []model.CaseActionExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("case_id = ? AND position > ? AND status IN ?", params.CaseID, params.AfterPosition, []model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRetrying}).
			Order("position ASC").
			Find(&executions).Error; err != nil {
			return fmt.Errorf("list case actions to skip: %w", err)
		}

		for i := range executions {
			executions[i].Status = model.ActionExecutionSkipped
			executions[i].LastErrorCode = "blocked_by_previous_action"
			executions[i].LastError = params.Reason
			executions[i].FinishedAt = &now
			executions[i].NextRetryAt = nil
			executions[i].UpdatedAt = now
			if err := tx.Select("*").Save(&executions[i]).Error; err != nil {
				return fmt.Errorf("skip case action execution: %w", err)
			}
			if err := createSkippedCaseActionAudit(tx, executions[i], params, now); err != nil {
				return err
			}
		}

		return nil
	})
}

// createCaseActionAudit creates case action audit while preserving validation, authorization, and persistence invariants.
func createCaseActionAudit(tx *gorm.DB, execution model.CaseActionExecution, params CompleteCaseActionParams, now time.Time) error {
	var caseModel model.Case
	result := tx.Where("id = ?", execution.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for action audit: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	action := "case_action.succeeded"
	resultValue := model.AuditResultSuccess
	switch params.ExecutionStatus {
	case model.ActionExecutionRetrying:
		action = "case_action.retrying"
		resultValue = model.AuditResultFailure
	case model.ActionExecutionFailed:
		action = "case_action.failed"
		resultValue = model.AuditResultFailure
	}

	return createAuditLogEntry(tx, &model.AuditLogEntry{
		GuildID:       caseModel.GuildID,
		Source:        model.AuditSourceSystem,
		Action:        action,
		ResourceType:  "case_action_execution",
		ResourceID:    execution.ID,
		Result:        resultValue,
		FailureReason: params.ErrorMessage,
		CorrelationID: firstNonEmpty(params.CorrelationID, execution.CorrelationID, caseModel.CorrelationID),
		RequestID:     params.RequestID,
		MetadataJSON: marshalJSONObject(map[string]any{
			"case_id":        caseModel.ID,
			"case_number":    caseModel.CaseNumber,
			"action_type":    execution.ActionType,
			"attempt_number": params.AttemptNumber,
			"retrying":       params.ExecutionStatus == model.ActionExecutionRetrying,
		}),
	}, now)
}

// createSkippedCaseActionAudit creates skipped case action audit while preserving validation, authorization, and persistence invariants.
func createSkippedCaseActionAudit(tx *gorm.DB, execution model.CaseActionExecution, params SkipCaseActionsParams, now time.Time) error {
	var caseModel model.Case
	result := tx.Where("id = ?", execution.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for skipped action audit: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	return createAuditLogEntry(tx, &model.AuditLogEntry{
		GuildID:       caseModel.GuildID,
		Source:        model.AuditSourceSystem,
		Action:        "case_action.skipped",
		ResourceType:  "case_action_execution",
		ResourceID:    execution.ID,
		Result:        model.AuditResultFailure,
		FailureReason: params.Reason,
		CorrelationID: firstNonEmpty(params.CorrelationID, execution.CorrelationID, caseModel.CorrelationID),
		RequestID:     params.RequestID,
		MetadataJSON: marshalJSONObject(map[string]any{
			"case_id":                caseModel.ID,
			"case_number":            caseModel.CaseNumber,
			"action_type":            execution.ActionType,
			"blocked_after_position": params.AfterPosition,
		}),
	}, now)
}
