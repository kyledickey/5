package quack

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// normalizeOptionalTime validates stable RFC3339 staff-search boundaries.
func normalizeOptionalTime(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", validationCaseError("date filter must use RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}

// validActionExecutionStatus reports whether a staff action-result filter is supported.
func validActionExecutionStatus(value model.ActionExecutionStatus) bool {
	switch value {
	case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionSucceeded,
		model.ActionExecutionFailed, model.ActionExecutionRetrying, model.ActionExecutionSkipped,
		model.ActionExecutionCancelled:
		return true
	default:
		return false
	}
}

// validAppealStatus reports whether a staff appeal-status filter is supported.
func validAppealStatus(value model.AppealStatus) bool {
	switch value {
	case model.AppealStatusPending, model.AppealStatusNeedsInformation, model.AppealStatusAccepted,
		model.AppealStatusRejected, model.AppealStatusClosed:
		return true
	default:
		return false
	}
}

// pagination parses the raw limit and offset query values shared by every case
// list endpoint. An empty limit defaults to 50 and is capped at 100; an empty
// offset defaults to 0. Non-numeric, zero, or negative values are validation errors.
func pagination(limitValue, offsetValue string) (int, int, error) {
	limit := 50
	if strings.TrimSpace(limitValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(limitValue))
		if err != nil || parsed <= 0 {
			return 0, 0, validationCaseError("limit must be a positive integer")
		}
		limit = parsed
	}
	if limit > 100 {
		limit = 100
	}

	offset := 0
	if strings.TrimSpace(offsetValue) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(offsetValue))
		if err != nil || parsed < 0 {
			return 0, 0, validationCaseError("offset must be a non-negative integer")
		}
		offset = parsed
	}

	return limit, offset, nil
}

// validCaseValidity reports whether validity is one of the two v5 case states.
func validCaseValidity(validity model.CaseValidity) bool {
	switch validity {
	case model.CaseValidityValid, model.CaseValidityVoided:
		return true
	default:
		return false
	}
}

// caseValiditySummary converts typed validity counts for transport responses.
func caseValiditySummary(source map[model.CaseValidity]int64) map[string]int64 {
	out := make(map[string]int64, len(source))
	for status, count := range source {
		out[string(status)] = count
	}
	return out
}

// validationCaseError wraps a safe case validation message for transport-specific error mapping.
func validationCaseError(message string) error {
	return fmt.Errorf("%w: %s", ErrCaseValidation, message)
}

// validCaseSource recognizes supported creation and historical-import sources.
func validCaseSource(source model.CaseSource) bool {
	switch source {
	case model.CaseSourceDashboard, model.CaseSourceDiscord, model.CaseSourceHoneypot, model.CaseSourceV4Import:
		return true
	default:
		return false
	}
}

// irreversibleAction marks membership removals whose ambiguous failures require staff review.
func irreversibleAction(actionType model.ActionType) bool {
	switch actionType {
	case model.ActionTimeoutUser, model.ActionKickUser, model.ActionBanUser:
		return true
	default:
		return false
	}
}
