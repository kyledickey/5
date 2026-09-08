package views

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
	"testing"
)

// TestReceiptAudienceAndVoidedState keeps moderator-only outcomes private and
// ensures a concurrent void changes both the receipt wording and controls.
func TestReceiptAudienceAndVoidedState(t *testing.T) {
	receipt := &quack.CaseReceiptResponse{Case: &quack.CaseResponse{ID: "case", CaseNumber: 4, TargetDiscordUserID: "member", SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "STAFF LEVEL"}}, EvidenceIncomplete: true}, RuleName: "Spam", MemberReason: "Do not spam", Appealable: true, Notification: &quack.CaseNotificationResponse{Status: model.NotificationFailed}, Actions: []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionFailed}, LastErrorCode: "missing_permission", LastError: "PRIVATE RAW ERROR"}}}
	private := CaseModeratorReceipt(receipt)
	for _, want := range []string{"STAFF LEVEL", "missing permission", "couldn’t be delivered", "can appeal", "evidence"} {
		if !strings.Contains(private.Content, want) {
			t.Fatalf("missing %s: %s", want, private.Content)
		}
	}
	if !private.Ephemeral || strings.Contains(private.Content, "PRIVATE RAW ERROR") {
		t.Fatal("private receipt unsafe")
	}
	public := CaseCreatedMessage(CaseCreated{Case: receipt.Case, Template: &quack.TemplateResponse{Name: "Spam"}, MemberReason: receipt.MemberReason})
	for _, hidden := range []string{"STAFF LEVEL", "missing permission", "PRIVATE RAW ERROR", "evidence", "DM"} {
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
