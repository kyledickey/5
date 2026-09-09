package moduleintegration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// warningPolicyStub supplies current outcomes without Discord or database calls.
type warningPolicyStub struct {
	actions []model.ActionType
	err     error
}

// UnattendedTemplateActions returns the configured test policy.
func (s warningPolicyStub) UnattendedTemplateActions(context.Context, string, string) ([]model.ActionType, error) {
	return s.actions, s.err
}

// TestGeneratedHoneypotWarningMatchesPolicy prevents claiming a ban for a timeout
// or for a level that merely records a case, including escalating policies.
func TestGeneratedHoneypotWarningMatchesPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []model.ActionType
		want    string
	}{
		{"ban", []model.ActionType{model.ActionBanUser}, "will ban you from this server"},
		{"timeout", []model.ActionType{model.ActionTimeoutUser}, "will time you out"},
		{"kick", []model.ActionType{model.ActionKickUser}, "will kick you from this server"},
		{"warning", []model.ActionType{model.ActionSendDM}, "will send you a warning by DM"},
		{"case only", []model.ActionType{""}, "will record a moderation case"},
		{"escalation", []model.ActionType{model.ActionTimeoutUser, model.ActionBanUser}, "can time you out or ban you from this server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, saved := range []string{"", legacyHoneypotWarning} {
				got, err := resolveHoneypotWarning(context.Background(), warningPolicyStub{actions: tc.actions}, "guild", honeypot.Settings{WarningText: saved})
				if err != nil || !strings.Contains(got, tc.want) {
					t.Fatalf("got %q, %v", got, err)
				}
			}
		})
	}
}

// TestCustomHoneypotWarningSurvivesPolicyReadFailure preserves explicit admin copy
// and refuses to invent a default punishment when current policy cannot be read.
func TestCustomHoneypotWarningSurvivesPolicyReadFailure(t *testing.T) {
	policy := warningPolicyStub{err: errors.New("unavailable")}
	custom := "# Custom warning\nPlease stay out."
	got, err := resolveHoneypotWarning(context.Background(), policy, "guild", honeypot.Settings{WarningText: custom})
	if err != nil || got != custom {
		t.Fatalf("custom copy changed: %q, %v", got, err)
	}
	if _, err := resolveHoneypotWarning(context.Background(), policy, "guild", honeypot.Settings{}); err == nil {
		t.Fatal("invented a punishment without current policy")
	}
}
