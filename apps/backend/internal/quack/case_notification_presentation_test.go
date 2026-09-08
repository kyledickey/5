package quack

import (
	"encoding/json"
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
	request := caseNotificationRequest(item, guild, nil, []model.CaseActionExecution{action}, []model.CaseActionAttempt{attempt})
	raw, _ := json.Marshal(request)
	body := string(raw)
	if request.RuleName != "Repeated spam" || !request.IncludeAppealInstructions || request.CaseNumber != 12 || request.Outcomes[0].TimeoutUntil == nil || !request.Outcomes[0].TimeoutUntil.Equal(until) {
		t.Fatal(request, body)
	}
	action.Status = model.ActionExecutionFailed
	request = caseNotificationRequest(item, guild, nil, []model.CaseActionExecution{action}, []model.CaseActionAttempt{attempt})
	if request.Outcomes[0].TimeoutUntil != nil {
		t.Fatal("failed action got expiry")
	}
	request = caseNotificationRequest(item, guild, nil, []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}}, nil)
	if request.Outcomes[0].TimeoutUntil != nil {
		t.Fatal("expiry invented")
	}

}

// TestNotificationNeverIncludesStaffContext protects the member boundary even
// when a case carries sensitive material from a moderator's evidence workflow.
func TestNotificationNeverIncludesStaffContext(t *testing.T) {
	item := model.Case{CaseNumber: 7, ModeratorDiscordUserID: "private-moderator", Reason: "Please keep chat on topic.", ContextValuesJSON: `[{"key":"note","label":"Staff note","value":"confidential investigation"}]`, TemplateSnapshotJSON: `{"template":{"id":"rule","name":"Off topic","appealable":true}}`}
	request := caseNotificationRequest(item, &model.Guild{Name: "The Pond"}, nil, nil, nil)
	raw, _ := json.Marshal(request)
	body := string(raw)
	for _, secret := range []string{"private-moderator", "Staff note", "confidential investigation"} {
		if strings.Contains(body, secret) {
			t.Fatalf("member DM exposed %q: %s", secret, body)
		}
	}
	for _, required := range []string{"Off topic", item.Reason} {
		if !strings.Contains(body, required) {
			t.Fatalf("member DM omitted %q: %s", required, body)
		}
	}
}

// TestNotificationPartialSnapshotPreservesControl keeps historical button
// eligibility independent of the stricter rule-copy snapshot projection.
func TestNotificationPartialSnapshotPreservesControl(t *testing.T) {
	request := caseNotificationRequest(model.Case{TemplateSnapshotJSON: `{"template":{"appealable":true}}`}, nil, nil, nil, nil)
	if !request.AppealControl || request.IncludeAppealInstructions {
		t.Fatal("partial snapshot semantics changed", request)
	}
}
