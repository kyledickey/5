package quack

import (
	"context"
	"encoding/json"

	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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
