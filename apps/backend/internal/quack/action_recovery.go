package quack

import (
	"context"
	"errors"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// ListFailures returns the active failed-action review queue.
func (s *ActionService) ListFailures(ctx context.Context, guildContext *GuildStaffContext, limit, offset int) (*model.FailedCaseActionResult, error) {
	if guildContext == nil || guildContext.Guild == nil || !guildContext.Can(model.PermissionActionCaseRead) {
		if s != nil && s.store != nil && guildContext != nil && guildContext.Guild != nil && guildContext.Staff != nil {
			entry := actionControlAudit(ctx, guildContext, string(model.AuditActionActionFailureRead), "list")
			entry.Result = model.AuditResultDenied
			entry.FailureReason = "permission_denied"
			_ = recordAudit(ctx, s.store, entry)
		}
		return nil, ErrCasePermissionDenied
	}
	result, err := s.store.ListFailedCaseActions(ctx, model.FailedCaseActionFilter{GuildID: guildContext.Guild.ID, Limit: limit, Offset: offset})
	entry := actionControlAudit(ctx, guildContext, string(model.AuditActionActionFailureRead), "list")
	if err != nil {
		entry.Result = model.AuditResultFailure
		entry.FailureReason = "query_failed"
	} else {
		entry.Result = model.AuditResultSuccess
	}
	if auditErr := recordAudit(ctx, s.store, entry); auditErr != nil && err == nil {
		return nil, auditErr
	}
	return result, err
}

// Retry performs live preflight before requeueing the immutable failed action.
func (s *ActionService) Retry(ctx context.Context, guildContext *GuildStaffContext, executionID string) (updated *model.CaseActionExecution, err error) {
	defer func() {
		s.auditControlFailure(ctx, guildContext, string(model.AuditActionActionRetry), executionID, err)
	}()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, ErrCasePermissionDenied
	}
	execution, err := s.store.GetCaseActionExecution(ctx, guildContext.Guild.ID, executionID)
	if err != nil {
		return nil, err
	}
	if execution == nil || execution.DismissedAt != nil {
		return nil, ErrCaseNotFound
	}
	if execution.Status != model.ActionExecutionFailed && execution.Status != model.ActionExecutionPending && execution.Status != model.ActionExecutionRetrying {
		return nil, ErrCaseNotFound
	}
	item, err := s.store.GetCaseByID(ctx, execution.CaseID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	if item.Validity == model.CaseValidityVoided && execution.ReversalOfExecutionID == nil {
		return nil, errors.New("a voided case's punishment cannot be retried")
	}
	if s.authorizer == nil {
		return nil, ErrAuthorizationUnavailable
	}
	preflight := s.authorizer.PreflightCase
	if execution.ReversalOfExecutionID != nil {
		preflight = s.authorizer.PreflightReversal
	}
	if err := preflight(ctx, guildContext, item.TargetDiscordUserID, execution.ActionType); err != nil {
		return nil, err
	}
	updated, err = s.store.RetryCaseAction(ctx, model.RetryCaseActionParams{GuildID: item.GuildID, ExecutionID: execution.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID, Audit: actionControlAudit(ctx, guildContext, "case_action.retry", execution.ID)})
	if err == nil && updated != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return updated, err
}

// Dismiss preserves attempts while removing a failure from active staff review.
func (s *ActionService) Dismiss(ctx context.Context, guildContext *GuildStaffContext, executionID string) (updated *model.CaseActionExecution, err error) {
	defer func() {
		s.auditControlFailure(ctx, guildContext, string(model.AuditActionActionDismiss), executionID, err)
	}()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(model.PermissionActionFailureDismiss) {
		return nil, ErrCasePermissionDenied
	}
	return s.store.DismissCaseAction(ctx, model.DismissCaseActionParams{GuildID: guildContext.Guild.ID, ExecutionID: executionID, ActorDiscordUserID: guildContext.Staff.DiscordUserID, Audit: actionControlAudit(ctx, guildContext, "case_action.dismiss", executionID)})
}

// Reverse queues a matching staff-confirmed timeout removal or unban after live permission checks.
func (s *ActionService) Reverse(ctx context.Context, guildContext *GuildStaffContext, caseID, originalExecutionID string, actionType model.ActionType) (*model.CaseActionExecution, error) {
	return s.ReverseForAppeal(ctx, guildContext, caseID, originalExecutionID, actionType, nil)
}

// ReverseForAppeal queues a reversal and, when supplied, verifies its accepted case-linked appeal.
func (s *ActionService) ReverseForAppeal(ctx context.Context, guildContext *GuildStaffContext, caseID, originalExecutionID string, actionType model.ActionType, appealID *string) (queued *model.CaseActionExecution, err error) {
	defer func() {
		s.auditControlFailure(ctx, guildContext, string(model.AuditActionActionReverse), originalExecutionID, err)
	}()
	if s.authorizer == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, ErrCaseNotFound
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrCaseNotFound
	}
	if err := s.authorizer.PreflightReversal(ctx, guildContext, item.TargetDiscordUserID, actionType); err != nil {
		return nil, err
	}
	queued, err = s.store.QueueCaseReversal(ctx, model.QueueCaseReversalParams{GuildID: item.GuildID, CaseID: item.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID, OriginalExecutionID: originalExecutionID, ActionType: actionType, AppealID: appealID, Audit: actionControlAudit(ctx, guildContext, "case_action.reverse", originalExecutionID)})
	if err == nil && queued != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return queued, err
}

func (s *ActionService) auditControlFailure(ctx context.Context, guildContext *GuildStaffContext, action, resourceID string, operationErr error) {
	if operationErr == nil || s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return
	}
	entry := actionControlAudit(ctx, guildContext, action, resourceID)
	entry.Result = model.AuditResultFailure
	if errors.Is(operationErr, ErrCasePermissionDenied) || errors.Is(operationErr, ErrAuthorizationDenied) {
		entry.Result = model.AuditResultDenied
	}
	entry.FailureReason = operationErr.Error()
	_ = recordAudit(ctx, s.store, entry)
}

// actionControlAudit constructs immutable staff recovery evidence with current permission bits.
func actionControlAudit(ctx context.Context, guildContext *GuildStaffContext, action, resourceID string) *model.AuditLogEntry {
	requestID, correlationID := TraceIDsFromContext(ctx)
	return &model.AuditLogEntry{GuildID: guildContext.Guild.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID, ActorPermissionBits: guildContext.PermissionBits, Source: AuditSourceFromContext(ctx), Action: action, ResourceType: "case_action_execution", ResourceID: resourceID, Result: model.AuditResultSuccess, RequestID: requestID, CorrelationID: correlationID, MetadataJSON: "{}"}
}
