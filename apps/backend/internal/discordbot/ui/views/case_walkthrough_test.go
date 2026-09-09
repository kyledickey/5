package views

import (
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
	"testing"
	"time"
)

// TestPublicCaseDetailIncludesStaffMaterial guards the audience change
// Staff commands show context in the channel chosen by the moderator.
func TestPublicCaseDetailIncludesStaffMaterial(t *testing.T) {
	detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case", CaseNumber: 42, TargetDiscordUserID: "member", Reason: "PRIVATE REASON", ContextURL: "PRIVATE URL", Validity: model.CaseValidityVoided, ContextValues: []quack.CaseContextValueResponse{{Value: "PRIVATE CONTEXT"}}}, Evidence: []quack.CaseEvidenceResponse{{Content: "PRIVATE FILE"}}, Events: []quack.CaseEventResponse{{Body: "PRIVATE EVENT"}}}
	detail.TemplateSnapshot = &quack.CaseTemplateSnapshotResponse{}
	detail.TemplateSnapshot.Template.Name = "Spam"
	detail.TemplateSnapshot.Template.ReasonTemplate = "Keep chat readable."
	message := PublicCaseDetail(detail)
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
