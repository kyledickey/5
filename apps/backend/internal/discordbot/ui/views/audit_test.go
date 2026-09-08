package views

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestAuditFailureShowsCaseAndPermissionCheckedRetry(t *testing.T) {
	notice := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "quack-system", Action: "case_action.failed", Result: model.AuditResultFailure, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "123", TemplateName: "Spam", ActionType: model.ActionBanUser, RetryExecutionID: "execution", CorrelationID: "internal-correlation", MetadataJSON: `{"private":"not-for-display"}`})
	for _, want := range []string{"Quack", "Case #42", "<@123>", "Spam", "Ban"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("missing %s: %s", want, notice.Content)
		}
	}
	for _, hidden := range []string{"<@quack-system>", "internal-correlation", "not-for-display"} {
		if strings.Contains(notice.Content, hidden) {
			t.Fatalf("leaked %s", hidden)
		}
	}
	if len(notice.Components) != 1 {
		t.Fatal("missing retry button")
	}
	button := notice.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Namespace != "case" || id.Action != "retry" || id.Payload != "execution" {
		t.Fatalf("wrong recovery route: %+v %v", id, err)
	}
}

// TestCaseCreatedMirrorNamesDecisionWithoutClaimingCompletion keeps the compact
// creation entry useful even before Discord has attempted the selected action.
func TestCaseCreatedMirrorNamesDecisionWithoutClaimingCompletion(t *testing.T) {
	notice := AuditMirrorMessage(quack.AuditMirrorMessage{ActorDiscordUserID: "moderator", Action: "case.create", Result: model.AuditResultSuccess, CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", TemplateName: "Spam", SelectedLevelName: "Third case", SelectedOutcome: "Timeout (24h)", MetadataJSON: `{"private":"hidden"}`})
	for _, want := range []string{"<@moderator>", "<@member>", "Case #42", "Spam", "Selected level: **Third case**", "Selected outcome: **Timeout (24h)**"} {
		if !strings.Contains(notice.Content, want) {
			t.Fatalf("missing %q: %s", want, notice.Content)
		}
	}
	for _, forbidden := range []string{"completed", "hidden", "queued", "succeeded"} {
		if strings.Contains(notice.Content, forbidden) {
			t.Fatalf("creation event claims completion or exposes metadata: %s", notice.Content)
		}
	}
	completed := AuditMirrorMessage(quack.AuditMirrorMessage{Action: "case_action.succeeded", ActionType: model.ActionTimeoutUser, Result: model.AuditResultSuccess, CaseID: "case", CaseNumber: 42, SelectedLevelName: "Third case", SelectedOutcome: "Timeout (24h)"})
	if !strings.Contains(completed.Content, "completed **Timeout**") || strings.Contains(completed.Content, "Selected outcome") {
		t.Fatalf("execution result was conflated with policy selection: %s", completed.Content)
	}
}
