package quack

import (
	"encoding/json"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// RecordedTimeoutUntil exposes only the expiry confirmed by a successful
// Discord attempt. It never estimates an expiry from queue or completion time,
// and returns nil when no successful attempt recorded a parseable expiry.
func RecordedTimeoutUntil(actionID string, attempts []model.CaseActionAttempt) *time.Time {
	for i := len(attempts) - 1; i >= 0; i-- {
		attempt := attempts[i]
		if attempt.ExecutionID != actionID || attempt.Status != model.ActionAttemptSucceeded {
			continue
		}
		var payload struct {
			Until string `json:"timeout_until"`
		}
		if json.Unmarshal([]byte(attempt.ResponsePayloadJSON), &payload) != nil {
			continue
		}
		if until, err := time.Parse(time.RFC3339, payload.Until); err == nil {
			return &until
		}
	}
	return nil
}
