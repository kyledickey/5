package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	actionmods "github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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

// WithDashboardBaseURL configures the secure member entry point used by
// appealable case notifications. It returns the receiver for chaining.
func (s *ActionService) WithDashboardBaseURL(baseURL string) *ActionService {
	s.dashboardBaseURL = strings.TrimSpace(baseURL)
	return s
}

// NewActionService wires the worker to persistence and the Discord enforcement
// client and registers one handler per supported action type. A nil store is a
// programming error and panics; a nil discord client is allowed for tests and
// makes every handler report the action as unsupported.
func NewActionService(store ActionRepository, discord DiscordActionClient) *ActionService {
	if store == nil {
		panic("quack: NewActionService requires a non-nil ActionRepository")
	}
	return &ActionService{
		store:   store,
		discord: discord,
		handlers: map[model.ActionType]actionmods.Executor{
			model.ActionSendDM:        actionmods.SendDM(discord),
			model.ActionTimeoutUser:   actionmods.TimeoutUser(discord),
			model.ActionKickUser:      actionmods.KickUser(discord),
			model.ActionBanUser:       actionmods.BanUser(discord),
			model.ActionRemoveTimeout: actionmods.RemoveTimeout(discord),
			model.ActionUnbanUser:     actionmods.UnbanUser(discord),
		},
	}
}

// WithRecoveryControls configures live authorization and scheduling for manual
// retries and reversals. Either may be nil; without an authorizer those
// operations return ErrAuthorizationUnavailable, and without a scheduler the
// requeued work waits for the durable poller.
func (s *ActionService) WithRecoveryControls(authorizer *GuildService, scheduler CaseWorkScheduler) *ActionService {
	s.authorizer = authorizer
	s.scheduler = scheduler
	return s
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
	requestID, correlationID := TraceIDsFromContext(ctx)
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

// actionWorkerID identifies the worker invocation recorded on durable action attempts.
func actionWorkerID() string {
	return "action-worker:" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}
