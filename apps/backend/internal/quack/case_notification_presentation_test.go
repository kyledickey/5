package quack

import (
	"fmt"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
	"testing"
	"time"
)

// TestNotificationUsesRecordedExpiryAndHonestOutcomes prevents DMs from claiming an unperformed punishment.
func TestNotificationUsesRecordedExpiryAndHonestOutcomes(t *testing.T) {
	item := model.Case{CaseNumber: 12, Reason: "Please stop repeating messages.", TemplateSnapshotJSON: `{"template":{"id":"rule","name":"Repeated spam","appealable":true}}`}
	guild := &model.Guild{Name: "The Pond"}
	action := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "execution"}, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}
	until := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	attempt := model.CaseActionAttempt{ExecutionID: action.ID, Status: model.ActionAttemptSucceeded, ResponsePayloadJSON: `{"timeout_until":"2026-09-05T09:00:00Z"}`}
	body := renderCaseNotification(item, guild, nil, []model.CaseActionExecution{action}, attempt)
	for _, want := range []string{"You’ve been timed out", "**The Pond**", "**Repeated spam**", "Case #12", "<t:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in %s", want, body)
		}
	}
	if !strings.Contains(body, fmt.Sprintf("<t:%d:f>", until.Unix())) {
		t.Fatalf("recorded expiry absent: %s", body)
	}
	action.Status = model.ActionExecutionFailed
	body = renderCaseNotification(item, guild, nil, []model.CaseActionExecution{action}, attempt)
	if strings.Contains(body, "You’ve been timed out") || strings.Contains(body, "You can chat again") {
		t.Fatalf("failed action shown as performed: %s", body)
	}
	body = renderCaseNotification(item, guild, nil, []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}})
	if strings.Contains(body, "You can chat again") {
		t.Fatal("expiry was invented without a recorded response")
	}
}
