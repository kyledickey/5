package quack

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// ErrAppealNotificationIntent rejects unsupported or corrupt durable decision
// payloads without reconstructing a notice from current settings or legacy copy.
var ErrAppealNotificationIntent = errors.New("appeal notification intent is invalid")

// AppealMemberNotification carries validated immutable facts to the renderer.
// LegacyBody is used only when an existing outbox row has no decision payload.
type AppealMemberNotification struct {
	Intent     *model.AppealDecisionIntent
	LegacyBody string
}

// appealMemberNotification validates the versioned persistence boundary before
// the adapter sees intent. Invalid nonempty payloads never fall back to Body.
func appealMemberNotification(item model.AppealNotification) (AppealMemberNotification, error) {
	if item.DecisionIntentJSON == "" {
		return AppealMemberNotification{LegacyBody: item.Body}, nil
	}
	var intent model.AppealDecisionIntent
	if json.Unmarshal([]byte(item.DecisionIntentJSON), &intent) != nil || intent.Version != 1 || !utf8.ValidString(intent.Reason) || strings.TrimSpace(intent.Reason) == "" || len([]rune(intent.Reason)) > 2000 {
		return AppealMemberNotification{}, ErrAppealNotificationIntent
	}
	switch intent.Status {
	case model.AppealStatusAccepted, model.AppealStatusRejected, model.AppealStatusNeedsInformation, model.AppealStatusClosed:
	default:
		return AppealMemberNotification{}, ErrAppealNotificationIntent
	}
	if intent.RejoinURL != "" {
		if intent.Status != model.AppealStatusAccepted {
			return AppealMemberNotification{}, ErrAppealNotificationIntent
		}
		normalized, err := normalizeAppealRejoinURL(intent.RejoinURL)
		if err != nil || normalized != intent.RejoinURL {
			return AppealMemberNotification{}, ErrAppealNotificationIntent
		}
	}
	return AppealMemberNotification{Intent: &intent}, nil
}
