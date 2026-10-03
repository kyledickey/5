package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// ActionRepository is the persistence ActionService needs: leased execution
// claims, notification delivery state, and staff retry, dismiss, and reversal.
type ActionRepository interface {
	BeginCaseNotificationDelivery(context.Context, string, string) error
	ClaimCaseNotification(context.Context, model.ClaimCaseNotificationParams) (*model.CaseNotification, error)
	ClaimNextCaseAction(context.Context, model.ClaimCaseActionParams) (*model.ClaimedCaseAction, error)
	CompleteCaseAction(context.Context, model.CompleteCaseActionParams) error
	CompleteCaseNotification(context.Context, model.CompleteCaseNotificationParams) error
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	DismissCaseAction(context.Context, model.DismissCaseActionParams) (*model.CaseActionExecution, error)
	GetCaseActionExecution(context.Context, string, string) (*model.CaseActionExecution, error)
	GetCaseByID(context.Context, string) (*model.Case, error)
	GetCaseByIDOrNumber(context.Context, string, string) (*model.Case, error)
	GetCaseNotification(context.Context, string) (*model.CaseNotification, error)
	GetGuildByID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	ListCaseActionAttempts(context.Context, []string) ([]model.CaseActionAttempt, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	ListFailedCaseActions(context.Context, model.FailedCaseActionFilter) (*model.FailedCaseActionResult, error)
	PrepareCaseNotification(context.Context, string, string, string) error
	QueueCaseReversal(context.Context, model.QueueCaseReversalParams) (*model.CaseActionExecution, error)
	RetryCaseAction(context.Context, model.RetryCaseActionParams) (*model.CaseActionExecution, error)
}

// ActionService is the durable action worker. It claims leased action
// executions for a case, runs the matching Discord handler, records each
// attempt with its retry decision, and finally delivers the member
// notification. discord may be nil (every action then fails as unsupported);
// authorizer and scheduler are optional and only enable manual retry/reversal.
type ActionService struct {
	store            ActionRepository
	discord          DiscordActionClient
	handlers         map[model.ActionType]actionmods.Executor
	authorizer       *GuildService
	scheduler        CaseWorkScheduler
	dashboardBaseURL string
}

// NewActionService wires the worker and registers one handler per supported
// action type. A nil discord client makes every handler report the action as
// unsupported. authorizer and scheduler may be nil: manual retry and reversal
// then return ErrAuthorizationUnavailable, and requeued work waits for the
// durable poller instead of being submitted immediately. dashboardBaseURL is
// the secure member entry point appealable case notifications link to.
func NewActionService(
	store ActionRepository,
	discord DiscordActionClient,
	authorizer *GuildService,
	scheduler CaseWorkScheduler,
	dashboardBaseURL string,
) *ActionService {
	return &ActionService{
		store:      store,
		discord:    discord,
		authorizer: authorizer,
		scheduler:  scheduler,
		handlers: map[model.ActionType]actionmods.Executor{
			model.ActionSendDM:        actionmods.SendDM(discord),
			model.ActionTimeoutUser:   actionmods.TimeoutUser(discord),
			model.ActionKickUser:      actionmods.KickUser(discord),
			model.ActionBanUser:       actionmods.BanUser(discord),
			model.ActionRemoveTimeout: actionmods.RemoveTimeout(discord),
			model.ActionUnbanUser:     actionmods.UnbanUser(discord),
		},
		dashboardBaseURL: strings.TrimSpace(dashboardBaseURL),
	}
}

// ProcessCaseActions drains the case's runnable actions one lease at a time:
// each claim executes and is completed before the next is claimed, so a case
// never has two attempts in flight. Before a kick or ban it opens the member
// DM channel, and once no claimable action remains it delivers the pending
// notification. It returns the first persistence error; handler failures are
// recorded on the attempt rather than returned.
func (s *ActionService) ProcessCaseActions(ctx context.Context, caseID string) error {
	ctx = ensureTraceContext(ctx)
	workerID := actionWorkerID()
	for {
		claimed, err := s.store.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{
			CaseID:   strings.TrimSpace(caseID),
			WorkerID: workerID,
		})
		if err != nil {
			return err
		}
		if claimed == nil {
			return s.processNotification(ctx, workerID, strings.TrimSpace(caseID))
		}
		if claimed.Execution.ActionType == model.ActionKickUser || claimed.Execution.ActionType == model.ActionBanUser {
			s.prepareNotification(ctx, claimed.Case)
		}

		if err := s.processClaimedAction(ctx, workerID, *claimed); err != nil {
			return err
		}
	}
}

// processClaimedAction runs one leased attempt and records its outcome. It
// resolves the guild, routes reversals through the provenance guard after
// re-checking the requesting moderator's live permission, and otherwise calls
// the handler for the action type. The result is classified as succeeded,
// retrying (only when the handler says the failure is retryable, the outcome is
// certain, the execution is marked safe, and retries remain) or failed, then
// persisted with the attempt payloads, the case event, and the next retry time.
func (s *ActionService) processClaimedAction(ctx context.Context, workerID string, claimed model.ClaimedCaseAction) error {
	handler, ok := s.handlers[claimed.Execution.ActionType]
	if !ok || handler == nil {
		handler = actionmods.Func(actionmods.Unsupported)
	}

	config := parseConfigMap(claimed.Execution.ConfigSnapshotJSON)
	guild, guildErr := s.store.GetGuildByID(ctx, claimed.Case.GuildID)
	discordGuildID := ""
	if guild != nil {
		discordGuildID = guild.DiscordGuildID
	}
	actionContext := actionmods.Context{
		Case:           claimed.Case,
		Execution:      claimed.Execution,
		Config:         config,
		DiscordGuildID: discordGuildID,
	}
	logger := slog.With("case_id", claimed.Case.ID, "guild_id", claimed.Case.GuildID,
		"execution_id", claimed.Execution.ID, "action", claimed.Execution.ActionType,
		"attempt", claimed.Execution.AttemptCount)
	logger.InfoContext(ctx, "Action attempt started")
	isReversalType := claimed.Execution.ActionType == model.ActionRemoveTimeout ||
		claimed.Execution.ActionType == model.ActionUnbanUser
	var result actionmods.Result
	switch {
	case guildErr != nil:
		// No request reached Discord, so retrying this dependency failure is safe.
		result = actionmods.RetryableError("guild_lookup_failed", "Guild information is temporarily unavailable")
	case discordGuildID == "":
		result = actionmods.PermanentError("guild_not_found", "The case guild is unavailable")
	case isReversalType && claimed.Execution.ReversalOfExecutionID == nil:
		result = actionmods.PermanentError(
			"reversal_provenance_unavailable",
			"The reversal has no original punishment reference. Review it manually.",
		)
	case claimed.Execution.ReversalOfExecutionID != nil:
		result = s.executeReversal(ctx, guild, config, actionContext)
	default:
		result = s.executeAction(ctx, handler, actionContext)
	}
	requestPayload := map[string]any{
		"case_id":      claimed.Case.ID,
		"execution_id": claimed.Execution.ID,
		"action_type":  claimed.Execution.ActionType,
		"config":       config,
	}
	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	if correlationID == "" {
		correlationID = claimed.Case.CorrelationID
	}

	attemptStatus := model.ActionAttemptSucceeded
	executionStatus := model.ActionExecutionSucceeded
	eventType := model.CaseEventActionSucceeded
	eventBody := "Discord enforcement succeeded"
	var nextRetryAt *time.Time
	if noop, _ := result.Response["reversal_noop"].(bool); noop && result.Error == "" {
		eventBody = "Punishment was already absent; no reversal request was sent"
	}

	if result.Error != "" {
		attemptStatus = model.ActionAttemptFailed
		eventType = model.CaseEventActionFailed
		eventBody = "Discord enforcement failed and requires staff review"
		executionStatus = model.ActionExecutionFailed
		if shouldRetryAction(claimed.Execution, result) {
			next := nextRetryTime(claimed.Execution)
			nextRetryAt = &next
			executionStatus = model.ActionExecutionRetrying
			eventBody = "Discord enforcement is waiting for a safe automatic retry"
		}
	}

	if result.Response == nil {
		result.Response = map[string]any{}
	}
	if result.Error != "" {
		result.Response["error"] = result.Error
	}

	err := s.store.CompleteCaseAction(ctx, model.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		LeaseToken:          claimed.Execution.LeaseToken,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            workerID,
		AttemptStatus:       attemptStatus,
		ExecutionStatus:     executionStatus,
		ErrorCode:           result.ErrorCode,
		ErrorMessage:        result.Error,
		RequestPayloadJSON:  mustMarshalJSONObject(requestPayload),
		ResponsePayloadJSON: mustMarshalJSONObject(result.Response),
		NextRetryAt:         nextRetryAt,
		EventType:           eventType,
		EventBody:           eventBody,
		EventMetadataJSON: mustMarshalJSONObject(map[string]any{
			"execution_id": claimed.Execution.ID,
			"action_type":  claimed.Execution.ActionType,
			"retrying":     executionStatus == model.ActionExecutionRetrying,
		}),
		CorrelationID: correlationID,
		RequestID:     requestID,
	})
	if err != nil {
		return fmt.Errorf("record action result: %w", err)
	}
	level := slog.LevelInfo
	if result.Error != "" {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "Action attempt recorded", "status", executionStatus,
		"error_code", result.ErrorCode, "outcome_uncertain", result.OutcomeUncertain,
		"next_retry_at", nextRetryAt)
	return nil
}

// executeReversal re-checks that the moderator recorded in the execution config
// as requested_by may still undo this punishment, then runs the guarded
// reversal. Missing authorizer, missing requester, or a failed live check all
// fail permanently so a moderator with permission can retry explicitly.
func (s *ActionService) executeReversal(
	ctx context.Context,
	guild *model.Guild,
	config map[string]any,
	action actionmods.Context,
) actionmods.Result {
	actorID, _ := config["requested_by"].(string)
	if s.authorizer == nil || actorID == "" {
		return actionmods.PermanentError(
			"reversal_authorization_unavailable",
			"Could not verify permission to undo this punishment. A moderator can retry it.",
		)
	}
	staffContext := &GuildStaffContext{Guild: guild, ActorDiscordUserID: actorID}
	if err := s.authorizer.PreflightReversal(ctx, staffContext, action.Case.TargetDiscordUserID, action.Execution.ActionType); err != nil {
		return actionmods.PermanentError(
			"reversal_permission_denied",
			"Could not verify permission to undo this punishment. A moderator with the required permission can retry it.",
		)
	}
	return s.executeAction(ctx, actionmods.Func(s.executeGuardedReversal), action)
}

// executeAction runs one handler under a 30 second timeout so a hung Discord
// request cannot hold the action lease indefinitely.
func (s *ActionService) executeAction(ctx context.Context, handler actionmods.Executor, action actionmods.Context) actionmods.Result {
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return handler.Execute(requestCtx, action)
}

// shouldRetryAction allows another attempt only for a known safe failure within the configured retry budget.
func shouldRetryAction(execution model.CaseActionExecution, result actionmods.Result) bool {
	if !result.Retryable || result.OutcomeUncertain || !execution.SafeForRetry {
		return false
	}
	return execution.AttemptCount <= execution.MaxRetries
}

// nextRetryTime applies the persisted retry delay, using one second for older records without a delay.
func nextRetryTime(execution model.CaseActionExecution) time.Time {
	backoff := execution.RetryBackoffMS
	if backoff <= 0 {
		backoff = 1000
	}
	return time.Now().UTC().Add(time.Duration(backoff) * time.Millisecond)
}

// parseConfigMap decodes a persisted action configuration, returning an empty object for absent or malformed JSON.
func parseConfigMap(body string) map[string]any {
	if strings.TrimSpace(body) == "" {
		return map[string]any{}
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(body), &config); err != nil || config == nil {
		return map[string]any{}
	}
	return config
}

// mustMarshalJSONObject encodes internal audit metadata, falling back to an empty object when encoding fails.
func mustMarshalJSONObject(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func actionWorkerID() string {
	return "action-worker:" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

// ListFailures returns the active failed-action review queue for the guild.
// Denied and failed reads are audited; a lost audit on an otherwise successful
// read fails the request.
func (s *ActionService) ListFailures(
	ctx context.Context,
	guildContext *GuildStaffContext,
	limit, offset int,
) (*model.FailedCaseActionResult, error) {
	if guildContext == nil || guildContext.Guild == nil || !guildContext.Can(model.PermissionActionCaseRead) {
		if guildContext != nil && guildContext.Guild != nil && guildContext.Staff != nil {
			entry := actionControlAudit(ctx, guildContext, string(model.AuditActionActionFailureRead), "list")
			entry.Result = model.AuditResultDenied
			entry.FailureReason = "permission_denied"
			// best-effort: the denial is already being returned to the caller
			_ = recordAudit(ctx, s.store, entry)
		}
		return nil, ErrCasePermissionDenied
	}
	result, err := s.store.ListFailedCaseActions(ctx, model.FailedCaseActionFilter{
		GuildID: guildContext.Guild.ID,
		Limit:   limit,
		Offset:  offset,
	})
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
// Only failed, pending, or retrying executions that are not dismissed qualify;
// a voided case's original punishment cannot be retried, though its reversal can.
func (s *ActionService) Retry(
	ctx context.Context,
	guildContext *GuildStaffContext,
	executionID string,
) (updated *model.CaseActionExecution, err error) {
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
	if execution.Status != model.ActionExecutionFailed &&
		execution.Status != model.ActionExecutionPending &&
		execution.Status != model.ActionExecutionRetrying {
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
	updated, err = s.store.RetryCaseAction(ctx, model.RetryCaseActionParams{
		GuildID:            item.GuildID,
		ExecutionID:        execution.ID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Audit:              actionControlAudit(ctx, guildContext, "case_action.retry", execution.ID),
	})
	if err == nil && updated != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return updated, err
}

// Dismiss preserves attempts while removing a failure from active staff review.
func (s *ActionService) Dismiss(
	ctx context.Context,
	guildContext *GuildStaffContext,
	executionID string,
) (updated *model.CaseActionExecution, err error) {
	defer func() {
		s.auditControlFailure(ctx, guildContext, string(model.AuditActionActionDismiss), executionID, err)
	}()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil ||
		!guildContext.Can(model.PermissionActionFailureDismiss) {
		return nil, ErrCasePermissionDenied
	}
	return s.store.DismissCaseAction(ctx, model.DismissCaseActionParams{
		GuildID:            guildContext.Guild.ID,
		ExecutionID:        executionID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Audit:              actionControlAudit(ctx, guildContext, "case_action.dismiss", executionID),
	})
}

// Reverse queues a matching staff-confirmed timeout removal or unban after live permission checks.
func (s *ActionService) Reverse(
	ctx context.Context,
	guildContext *GuildStaffContext,
	caseID, originalExecutionID string,
	actionType model.ActionType,
) (*model.CaseActionExecution, error) {
	return s.ReverseForAppeal(ctx, guildContext, caseID, originalExecutionID, actionType, nil)
}

// ReverseForAppeal queues a reversal and, when supplied, verifies its accepted
// case-linked appeal. caseID may be a case ID or a case number within the guild.
func (s *ActionService) ReverseForAppeal(
	ctx context.Context,
	guildContext *GuildStaffContext,
	caseID, originalExecutionID string,
	actionType model.ActionType,
	appealID *string,
) (queued *model.CaseActionExecution, err error) {
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
	queued, err = s.store.QueueCaseReversal(ctx, model.QueueCaseReversalParams{
		GuildID:             item.GuildID,
		CaseID:              item.ID,
		ActorDiscordUserID:  guildContext.Staff.DiscordUserID,
		OriginalExecutionID: originalExecutionID,
		ActionType:          actionType,
		AppealID:            appealID,
		Audit:               actionControlAudit(ctx, guildContext, "case_action.reverse", originalExecutionID),
	})
	if err == nil && queued != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return queued, err
}

// auditControlFailure records a denied or failed recovery control operation
// after the fact. It is called from a defer with the named error result, so a
// nil error or an incomplete guild context records nothing.
func (s *ActionService) auditControlFailure(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceID string,
	operationErr error,
) {
	if operationErr == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return
	}
	entry := actionControlAudit(ctx, guildContext, action, resourceID)
	entry.Result = model.AuditResultFailure
	if errors.Is(operationErr, ErrCasePermissionDenied) || errors.Is(operationErr, ErrAuthorizationDenied) {
		entry.Result = model.AuditResultDenied
	}
	entry.FailureReason = operationErr.Error()
	// best-effort: the failure is already being returned to the caller
	_ = recordAudit(ctx, s.store, entry)
}

// actionControlAudit builds a successful audit row for a staff recovery control
// operation on one action execution, capturing the actor's current permission bits.
func actionControlAudit(ctx context.Context, guildContext *GuildStaffContext, action, resourceID string) *model.AuditLogEntry {
	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	return &model.AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  guildContext.Staff.DiscordUserID,
		ActorPermissionBits: guildContext.PermissionBits,
		Source:              AuditSourceFromContext(ctx),
		Action:              action,
		ResourceType:        "case_action_execution",
		ResourceID:          resourceID,
		Result:              model.AuditResultSuccess,
		RequestID:           requestID,
		CorrelationID:       correlationID,
		MetadataJSON:        "{}",
	}
}

// reversalProvenanceReader exposes original enforcement evidence and competing
// punishments without making transport adapters query moderation storage.
type reversalProvenanceReader interface {
	LoadCaseReversalProvenance(context.Context, string, string, string) (*model.CaseActionExecution, string, bool, error)
}

// guardedReversalClient inspects live punishment ownership immediately before
// removal. Missing support must never fall back to an unconditional reversal.
type guardedReversalClient interface {
	RemoveOwnedTimeout(context.Context, string, string, string, string) (map[string]any, error)
	RemoveOwnedBan(context.Context, string, string, string, string) (map[string]any, error)
}

// executeGuardedReversal preserves a later punishment by checking durable origin
// and live Discord state. Discord has no conditional remove API, so an external
// moderator changing punishment between inspection and removal remains a race.
func (s *ActionService) executeGuardedReversal(ctx context.Context, action actionmods.Context) actionmods.Result {
	reader, ok := s.store.(reversalProvenanceReader)
	client, hasClient := s.discord.(guardedReversalClient)
	if !ok || !hasClient || action.Execution.ReversalOfExecutionID == nil {
		return actionmods.PermanentError(
			"reversal_provenance_unavailable",
			"Could not verify which punishment belongs to this case. Review it manually.",
		)
	}
	original, payload, newer, err := reader.LoadCaseReversalProvenance(
		ctx,
		action.Case.GuildID,
		action.Case.ID,
		*action.Execution.ReversalOfExecutionID,
	)
	if err != nil {
		return actionmods.PermanentError(
			"reversal_provenance_unavailable",
			"Could not read the original punishment. Review it before retrying.",
		)
	}
	if original == nil ||
		original.CaseID != action.Case.ID ||
		original.Status != model.ActionExecutionSucceeded ||
		original.ReversalOfExecutionID != nil {
		return actionmods.PermanentError(
			"reversal_provenance_unavailable",
			"The original successful punishment could not be verified. Review it manually.",
		)
	}
	if newer {
		return actionmods.PermanentError(
			"reversal_ownership_conflict",
			"Another punishment or unresolved attempt affects this member. Review it manually; nothing was removed.",
		)
	}
	reason := actionmods.AuditReason(action)
	var response map[string]any
	switch {
	case action.Execution.ActionType == model.ActionRemoveTimeout && original.ActionType == model.ActionTimeoutUser:
		var recorded struct {
			Until string `json:"timeout_until"`
		}
		if json.Unmarshal([]byte(payload), &recorded) != nil || recorded.Until == "" {
			return actionmods.PermanentError(
				"reversal_provenance_unavailable",
				"The original timeout expiry was not recorded. Review it manually.",
			)
		}
		response, err = client.RemoveOwnedTimeout(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, recorded.Until, reason)
	case action.Execution.ActionType == model.ActionUnbanUser && original.ActionType == model.ActionBanUser:
		response, err = client.RemoveOwnedBan(ctx, action.DiscordGuildID, action.Case.TargetDiscordUserID, reason, reason)
	default:
		return actionmods.PermanentError(
			"reversal_provenance_unavailable",
			"The reversal does not match the original punishment.",
		)
	}
	if err != nil {
		return actionmods.ResultFromError(err)
	}
	return actionmods.Result{Response: response}
}
