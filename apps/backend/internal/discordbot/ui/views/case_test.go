package views

import (
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestCaseDetailPageIncludesStaffMaterial guards the audience change
// Staff commands show context in the channel chosen by the moderator.
func TestCaseDetailPageIncludesStaffMaterial(t *testing.T) {
	detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case", CaseNumber: 42, TargetDiscordUserID: "member", Reason: "PRIVATE REASON", ContextURL: "PRIVATE URL", Validity: model.CaseValidityVoided, ContextValues: []quack.CaseContextValueResponse{{Value: "PRIVATE CONTEXT"}}}, Evidence: []quack.CaseEvidenceResponse{{Content: "PRIVATE FILE"}}, Events: []quack.CaseEventResponse{{Body: "PRIVATE EVENT"}}}
	detail.TemplateSnapshot = &quack.CaseTemplateSnapshotResponse{}
	detail.TemplateSnapshot.Template.Name = "Spam"
	detail.TemplateSnapshot.Template.ReasonTemplate = "Keep chat readable."
	message := CaseDetailPage(detail, 1, "")
	if message.Ephemeral || !strings.Contains(message.Content, "PRIVATE CONTEXT") || !strings.Contains(message.Content, "PRIVATE REASON") || !strings.Contains(message.Content, "PRIVATE FILE") || !strings.Contains(message.Content, "PRIVATE EVENT") {
		t.Fatal(message.Content)
	}
	if !strings.Contains(strings.Split(message.Content, "\n")[0], "Voided") {
		t.Fatal("void status not immediately visible")
	}
}

// TestCaseHistoryNamesTheRule keeps a rule recognizable without understanding
// internal escalation labels, and uses Discord timestamps beneath each case.
func TestCaseHistoryNamesTheRule(t *testing.T) {
	message := CaseListMessage(&quack.CaseListResponse{Total: 1, Cases: []quack.CaseResponse{{CaseNumber: 4, RuleName: "Spam", TargetDiscordUserID: "member", CreatedAt: time.Unix(1700000000, 0), SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Case 2 onward"}}}}}, 1, "")
	if message.Ephemeral || !strings.Contains(message.Content, "Spam") || strings.Contains(message.Content, "Case 2 onward") || !strings.Contains(message.Content, "\n-# <t:1700000000:R>") {
		t.Fatal(message.Content)
	}
}

// TestReceiptAudienceAndVoidedState shows moderator outcomes in the invoking channel and
// ensures a concurrent void changes both the receipt wording and controls.
func TestReceiptAudienceAndVoidedState(t *testing.T) {
	receipt := &quack.CaseReceiptResponse{Case: &quack.CaseResponse{ID: "case", CaseNumber: 4, TargetDiscordUserID: "member", SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "STAFF LEVEL"}}, EvidenceIncomplete: true}, RuleName: "Spam", MemberReason: "Do not spam", Appealable: true, Notification: &quack.CaseNotificationResponse{Status: model.NotificationFailed}, Actions: []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionFailed}, LastErrorCode: "missing_permission", LastError: "PRIVATE RAW ERROR"}}}
	private := CaseModeratorReceipt(receipt)
	for _, want := range []string{"STAFF LEVEL", "missing permission", "couldn’t be delivered", "can appeal", "evidence"} {
		if !strings.Contains(private.Content, want) {
			t.Fatalf("missing %s: %s", want, private.Content)
		}
	}
	if private.Ephemeral || strings.Contains(private.Content, "PRIVATE RAW ERROR") {
		t.Fatal("private receipt unsafe")
	}
	public := CaseCreatedMessage(CaseCreated{Case: receipt.Case, Template: &quack.TemplateResponse{Name: "Spam"}, MemberReason: receipt.MemberReason})
	for _, hidden := range []string{"STAFF LEVEL", "missing permission", "PRIVATE RAW ERROR", "DM"} {
		if strings.Contains(public.Content, hidden) {
			t.Fatal("public leak", hidden)
		}
	}
	if !strings.Contains(public.Content, "Do not spam") {
		t.Fatal("member reason missing")
	}
	receipt.Case.Validity = model.CaseValidityVoided
	receipt.Appealable = false
	private = CaseModeratorReceipt(receipt)
	if !strings.Contains(private.Content, "was voided") || strings.Contains(private.Content, "added for") {
		t.Fatal("stale void wording")
	}
	row := private.Components[0].(discordgo.ActionsRow)
	if !row.Components[3].(discordgo.Button).Disabled {
		t.Fatal("void control remains enabled")
	}
}
