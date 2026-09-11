package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompleteCaseAction records a worker's outcome for one execution and its
// attempt, fenced on params.LeaseToken when set: a stale token is an error and
// a missing execution without a token is ignored. Locks are taken case-first
// to match VoidCase and ClaimNextCaseAction. A retry reported after the case
// was voided is downgraded to a failure so enforcement never resumes, and any
// reversals owed to the void are queued here in the same transaction.
func (s *Store) CompleteCaseAction(ctx context.Context, params model.CompleteCaseActionParams) error {

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Serialize completion with void/claim using the same case-first order.
		var caseRecord model.Case
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN (SELECT case_id FROM case_action_executions WHERE id = ?)", params.ExecutionID).
			Limit(1).
			Find(&caseRecord).Error; err != nil {
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
		attemptResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("execution_id = ? AND attempt_number = ?", execution.ID, attemptNumber).
			First(&attempt)
		if errors.Is(attemptResult.Error, gorm.ErrRecordNotFound) {
			attempt = model.CaseActionAttempt{
				ExecutionID:   execution.ID,
				AttemptNumber: attemptNumber,
				StartedAt:     startedAt,
				WorkerID:      params.WorkerID,
			}
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
		voidedRetry := caseRecord.Validity == model.CaseValidityVoided &&
			execution.ReversalOfExecutionID == nil &&
			params.ExecutionStatus == model.ActionExecutionRetrying
		if voidedRetry {
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

		if err := requestCasePublicationRefresh(tx, execution.CaseID, now); err != nil {
			return err
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

// SkipCaseActions marks every pending or retrying execution after
// params.AfterPosition as skipped with params.Reason, auditing each one. It is
// used when an earlier action failed without ContinueOnError semantics.
func (s *Store) SkipCaseActions(ctx context.Context, params model.SkipCaseActionsParams) error {

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var executions []model.CaseActionExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("case_id = ? AND position > ? AND status IN ?",
				params.CaseID,
				params.AfterPosition,
				[]model.ActionExecutionStatus{model.ActionExecutionPending, model.ActionExecutionRetrying},
			).
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

		if len(executions) > 0 {
			return requestCasePublicationRefresh(tx, params.CaseID, now)
		}
		return nil
	})
}

// createCaseActionAudit writes the succeeded/retrying/failed audit row for a
// completed execution. A reversal that found nothing to undo is flagged as
// reversal_noop so the mirror can explain it. A missing case is ignored.
func createCaseActionAudit(tx *gorm.DB, execution model.CaseActionExecution, params model.CompleteCaseActionParams, now time.Time) error {
	var caseModel model.Case
	result := tx.Where("id = ?", execution.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for action audit: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	var response struct {
		ReversalNoop bool `json:"reversal_noop"`
	}
	_ = json.Unmarshal([]byte(params.ResponsePayloadJSON), &response) // best-effort: an unparseable payload just means no noop flag
	noop := response.ReversalNoop &&
		params.ExecutionStatus == model.ActionExecutionSucceeded &&
		execution.ReversalOfExecutionID != nil &&
		(execution.ActionType == model.ActionRemoveTimeout || execution.ActionType == model.ActionUnbanUser)
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
			"reversal_noop":  noop,
			"case_id":        caseModel.ID,
			"case_number":    caseModel.CaseNumber,
			"action_type":    execution.ActionType,
			"attempt_number": params.AttemptNumber,
			"retrying":       params.ExecutionStatus == model.ActionExecutionRetrying,
		}),
	}, now)
}

// createSkippedCaseActionAudit writes the case_action.skipped audit row for one
// execution skipped by SkipCaseActions. A missing case is ignored.
func createSkippedCaseActionAudit(tx *gorm.DB, execution model.CaseActionExecution, params model.SkipCaseActionsParams, now time.Time) error {
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
