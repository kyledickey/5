package views

import (
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestCaseDetailSeparatesStateContextEvidenceAndRecovery(t *testing.T) {
	detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-1", CaseNumber: 7, TargetDiscordUserID: "target", Reason: "Official reason", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, ContextValues: []quack.CaseContextValueResponse{{Key: "details", Label: "Details", Value: "Visible context"}}, SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: "Timeout"}}}, Actions: []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ID: "action-1", ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionFailed}, LastErrorCode: "permission_denied"}}, Evidence: []quack.CaseEvidenceResponse{{MessageURL: "https://discord.com/channels/1/2/3", CaptureOutcome: "captured"}}, Events: []quack.CaseEventResponse{{EventType: model.CaseEventCreated, Body: "Case created"}}}
	message := CaseDetailMessage(detail)
	if message.Ephemeral || len(message.Embeds) != 0 || len(message.Components) != 2 {
		t.Fatalf("unexpected detail view: %+v", message)
	}
	for _, required := range []string{"Case for <@target>.", "> Official reason", "Timeout couldn’t be completed", "permission denied", "> Details — Visible context", "[View message](https://discord.com/channels/1/2/3)", "Case created", "-# Case #7"} {
		if !strings.Contains(message.Content, required) {
			t.Fatalf("missing %q from staff conversation: %s", required, message.Content)
		}
	}
	row := message.Components[0].(discordgo.ActionsRow)
	if len(row.Components) != 4 {
		t.Fatalf("expected context, evidence, user, and void controls: %+v", row)
	}
}

// TestEvidencePagesStayNative verifies a long capture remains navigable without
// the generic message.txt fallback, including the bot's expanded custom icons.
func TestEvidencePagesStayNative(t *testing.T) {
	detail := &quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case-1", CaseNumber: 7}, Evidence: []quack.CaseEvidenceResponse{{Content: strings.Repeat("🦆 evidence text\n", 450), MessageURL: "https://discord.com/channels/1/2/3"}}}
	for page := 1; ; page++ {
		message := CaseEvidencePage(detail, page, "819019613371236432").ForApplication("819019613371236432")
		if !message.Ephemeral || len(message.Files) != 0 || len(utf16.Encode([]rune(message.Content))) > 2000 || !strings.Contains(message.Content, "/case evidence case:7 file:") {
			t.Fatalf("page %d is not a complete native evidence page: %+v", page, message)
		}
		row := message.Components[0].(discordgo.ActionsRow)
		if row.Components[0].(discordgo.Button).Disabled != (page == 1) {
			t.Fatal("previous-page state is incorrect")
		}
		if row.Components[1].(discordgo.Button).Disabled {
			if page < 2 {
				t.Fatal("long evidence did not paginate")
			}
			break
		}
		if page > 100 {
			t.Fatal("evidence pagination has no terminal page")
		}
	}
}

// TestLongCaseDetailRetainsContextAndRecovery checks that the complete staff
// record can be read without downloading a file or losing its retry controls.
func TestLongCaseDetailRetainsContextAndRecovery(t *testing.T) {
	detail := &quack.CaseDetailResponse{
		CaseResponse: quack.CaseResponse{ID: "case-1", CaseNumber: 9, Reason: "Spam", ContextValues: []quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("🦆 long context\n", 500) + "FINAL CONTEXT"}}},
		Actions:      []quack.CaseActionDetailResponse{{CaseActionResponse: quack.CaseActionResponse{ID: "failed-1", ActionType: model.ActionBanUser, Status: model.ActionExecutionFailed}}},
	}
	var contents strings.Builder
	for page := 1; ; page++ {
		message := CaseDetailPage(detail, page, "819019613371236432").ForApplication("819019613371236432")
		if !message.Ephemeral || len(message.Files) != 0 || len(utf16.Encode([]rune(message.Content))) > 2000 || len(message.Components) != 3 {
			t.Fatalf("page %d lost native content or controls: %+v", page, message)
		}
		contents.WriteString(message.Content)
		recovery := message.Components[1].(discordgo.ActionsRow)
		if recovery.Components[0].(discordgo.Button).Label != "Retry" {
			t.Fatal("retry control missing")
		}
		navigation := message.Components[2].(discordgo.ActionsRow)
		if navigation.Components[1].(discordgo.Button).Disabled {
			break
		}
		if page > 100 {
			t.Fatal("case detail has no last page")
		}
	}
	if !strings.Contains(contents.String(), "FINAL CONTEXT") {
		t.Fatal("long context was truncated")
	}
}

func TestCaseListPaginationIsStableAndScoped(t *testing.T) {
	message := CaseListMessage(&quack.CaseListResponse{Cases: []quack.CaseResponse{{CaseNumber: 9, TargetDiscordUserID: "member", Validity: model.CaseValidityVoided}}, Total: 21, Limit: 10, Offset: 10}, 2, "member")
	if message.Ephemeral || len(message.Components) != 1 || !strings.Contains(message.Content, "Page 2/3") {
		t.Fatalf("unexpected pagination: %+v", message)
	}
}

// TestVoidedCaseDoesNotInviteAnotherAppeal keeps the staff detail consistent with
// the terminal correction made when an appeal is accepted.
func TestVoidedCaseDoesNotInviteAnotherAppeal(t *testing.T) {
	detail := &quack.CaseDetailResponse{
		CaseResponse:     quack.CaseResponse{ID: "case-1", CaseNumber: 1, Validity: model.CaseValidityValid},
		TemplateSnapshot: &quack.CaseTemplateSnapshotResponse{},
	}
	detail.TemplateSnapshot.Template.Appealable = true
	if !strings.Contains(CaseDetailMessage(detail).Content, "The member can appeal this case.") {
		t.Fatal("valid appealable case lost its appeal guidance")
	}
	detail.Validity = model.CaseValidityVoided
	message := CaseDetailMessage(detail)
	if strings.Contains(message.Content, "The member can appeal this case.") || !strings.Contains(message.Content, "This case was voided") {
		t.Fatalf("voided case has misleading guidance: %s", message.Content)
	}
	row := message.Components[0].(discordgo.ActionsRow)
	if !row.Components[3].(discordgo.Button).Disabled {
		t.Fatal("voided case still offers an enabled void control")
	}
}
