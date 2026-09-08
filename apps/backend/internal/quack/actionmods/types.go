package actionmods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// DiscordClient defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
type DiscordClient interface {
	SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error)
}

// EnforcementClient is implemented by Discord adapters that can perform real moderation and reversal operations.
type EnforcementClient interface {
	TimeoutMember(context.Context, string, string, int, string) (map[string]any, error)
	KickMember(context.Context, string, string, string) (map[string]any, error)
	BanMember(context.Context, string, string, int, string) (map[string]any, error)
	RemoveMemberTimeout(context.Context, string, string, string) (map[string]any, error)
	UnbanMember(context.Context, string, string, string) (map[string]any, error)
}

// Context supplies the persisted case and execution snapshots for one attempt.
// Config is decoded from that execution, so later template edits do not change
// the action being retried. DiscordGuildID is the external guild identifier.
type Context struct {
	Case           model.Case
	Execution      model.CaseActionExecution
	Config         map[string]any
	DiscordGuildID string
}

// Result describes an action attempt in implementation-neutral terms so the action service can persist retries, failures, and external response data uniformly.
type Result struct {
	Retryable        bool
	ErrorCode        string
	Error            string
	Response         map[string]any
	OutcomeUncertain bool
}

// Executor runs one action module without exposing Discord or persistence details to the orchestration service.
type Executor interface {
	Execute(ctx context.Context, action Context) Result
}

// Func adapts a function to Executor, keeping small action modules declarative.
type Func func(ctx context.Context, action Context) Result

// Execute invokes the wrapped action function; retry policy remains the responsibility of the caller.
func (f Func) Execute(ctx context.Context, action Context) Result {
	return f(ctx, action)
}

// DiscordError carries the adapter's retry classification into the action worker.
// OutcomeUncertain means Discord may already have applied the operation; retry
// eligibility alone must not be treated as proof that repeating it is safe.
type DiscordError struct {
	Code             string
	Message          string
	Retryable        bool
	OutcomeUncertain bool
}

// Error returns the failure explanation, falling back to its code.
func (e DiscordError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// ResultFromError preserves classified adapter failures. Cancellation and unknown
// errors are uncertain, non-retryable results because they do not establish
// whether Discord applied the request. A nil error produces a successful result.
func ResultFromError(err error) Result {
	if err == nil {
		return Result{}
	}
	var actionErr DiscordError
	if errors.As(err, &actionErr) {
		if actionErr.Retryable {
			result := RetryableError(actionErr.Code, actionErr.Error())
			result.OutcomeUncertain = actionErr.OutcomeUncertain
			return result
		}
		result := PermanentError(actionErr.Code, actionErr.Error())
		result.OutcomeUncertain = actionErr.OutcomeUncertain
		return result
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return Result{ErrorCode: "context_cancelled", Error: "Discord request was interrupted", OutcomeUncertain: true}
	}
	return Result{ErrorCode: "discord_error", Error: "Discord request failed", OutcomeUncertain: true}
}

// PermanentError creates a failure that must not be retried automatically.
func PermanentError(code, message string) Result {
	return Result{ErrorCode: code, Error: message}
}

// RetryableError marks a known failure eligible for the worker's retry policy.
// Callers must separately mark uncertain outcomes when an external effect may
// have occurred; the worker also considers the action's safety and retry limit.
func RetryableError(code, message string) Result {
	return Result{Retryable: true, ErrorCode: code, Error: message}
}

// Unsupported rejects an unregistered action without attempting a Discord effect.
func Unsupported(ctx context.Context, action Context) Result {
	_ = ctx
	return PermanentError("unsupported_action", fmt.Sprintf("action type %s is not supported", action.Execution.ActionType))
}

// ConfigString reads a trimmed action setting. Non-string values use their Go
// text representation; a missing key returns an empty string.
func ConfigString(config map[string]any, key string) string {
	value, ok := config[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

// ConfigInt reads integer-valued JSON settings without accepting fractional values.
func ConfigInt(config map[string]any, key string) int {
	value, ok := config[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		bound := math.Ldexp(1, strconv.IntSize-1)
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < -bound || typed >= bound {
			return 0
		}
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	default:
		parsed, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(typed)))
		return parsed
	}
}

// AuditReason returns a bounded Discord audit-log reason containing the immutable case reference and official reason.
func AuditReason(action Context) string {
	value := fmt.Sprintf("Quack case #%d: %s", action.Case.CaseNumber, strings.TrimSpace(action.Case.Reason))
	runes := []rune(value)
	if len(runes) > 512 {
		return string(runes[:512])
	}
	return value
}
