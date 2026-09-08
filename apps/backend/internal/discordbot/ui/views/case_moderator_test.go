package views

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
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

// TestCaseProfileUsesAllTimeCountsAndLabelsLegacy ensures a one-row page does
// not become a misleading escalation count or hide imported moderation history.
func TestCaseProfileUsesAllTimeCountsAndLabelsLegacy(t *testing.T) {
	profile := &quack.CaseProfileResponse{Cases: []quack.CaseResponse{{CaseNumber: 8, TargetDiscordUserID: "member", Source: model.CaseSourceV4Import, Validity: model.CaseValidityValid}}, Total: 21, Limit: 10, Offset: 20, Summary: quack.CaseProfileSummary{Total: 21, ByValidity: map[string]int64{"valid": 17, "voided": 4}}}
	message := CaseProfileMessage(profile, 3, "member")
	for _, text := range []string{"21 total · 17 valid · 4 voided", "Imported v4", "eligible v5 cases", "Page 3/3"} {
		if !strings.Contains(message.Content, text) {
			t.Fatalf("missing %q: %s", text, message.Content)
		}
	}
	if !message.Ephemeral || len(message.Components) == 0 {
		t.Fatal("profile privacy or pagination lost")
	}
}

// TestCaseHistoryWorstCaseLabelsStayNative bounds UTF-16 after application emoji
// expansion while retaining every row, tag, date, summary, and page control.
func TestCaseHistoryWorstCaseLabelsStayNative(t *testing.T) {
	for _, name := range []string{strings.Repeat("😀", 100), strings.Repeat("_*~`", 25), strings.Repeat("{{quack:history}}", 6)} {
		profile := &quack.CaseProfileResponse{Total: 100, Limit: 10, Summary: quack.CaseProfileSummary{Total: 100, ByValidity: map[string]int64{"valid": 90, "voided": 10}}}
		for i := 0; i < 10; i++ {
			profile.Cases = append(profile.Cases, quack.CaseResponse{CaseNumber: uint64(18446744073709551600) + uint64(i), TargetDiscordUserID: "12345678901234567890", Source: model.CaseSourceV4Import, Validity: model.CaseValidityVoided, CreatedAt: time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC), SelectedLevel: &quack.CaseSelectedLevel{TemplateLevelDetails: quack.TemplateLevelDetails{Name: name}}})
		}
		for _, message := range []ui.Message{CaseListMessage(&quack.CaseListResponse{Cases: profile.Cases, Total: 100, Limit: 10}, 1, ""), CaseProfileMessage(profile, 1, "12345678901234567890")} {
			for _, app := range []string{"", "968198214450831370", "819019613371236432"} {
				prepared := message.ForApplication(app)
				if len(utf16.Encode([]rune(prepared.Content))) > 2000 || len(prepared.Files) != 0 || len(prepared.Components) == 0 {
					t.Fatalf("history left native text: units=%d files=%d", len(utf16.Encode([]rune(prepared.Content))), len(prepared.Files))
				}
				for _, item := range profile.Cases {
					if !strings.Contains(prepared.Content, fmt.Sprintf("**#%d**", item.CaseNumber)) {
						t.Fatal("history row truncated away")
					}
				}
				if strings.Count(prepared.Content, "Imported v4") != 10 || strings.Count(prepared.Content, "**Voided**") != 10 || !strings.Contains(prepared.Content, "Page 1/10") {
					t.Fatal("row tags or pagination lost")
				}
			}
		}
		if profile.Cases[0].SelectedLevel.Name != name {
			t.Fatal("display truncation changed original detail name")
		}
	}
}

// TestImportedCaseViewsPreserveHistoricalOutcome ensures history without v5
// executions never falls through to the current warning-only presentation.
func TestImportedCaseViewsPreserveHistoricalOutcome(t *testing.T) {
	for action, label := range map[string]string{"warning": "Warning", "ban": "Ban", "kick": "Kick", "unban": "Unban", "timeout": "Timeout", "message_delete": "Message deletion"} {
		t.Run(action, func(t *testing.T) {
			item := quack.CaseResponse{CaseNumber: 9, TargetDiscordUserID: "member", Source: model.CaseSourceV4Import, Reason: "Original reason", ContextURL: "https://discord.com/channels/1/2/3", Metadata: map[string]any{"v4": map[string]any{"action_type": action}}}
			detail := CaseDetailMessage(&quack.CaseDetailResponse{CaseResponse: item})
			list := CaseListMessage(&quack.CaseListResponse{Cases: []quack.CaseResponse{item}, Total: 1}, 1, "member")
			for _, content := range []string{detail.Content, list.Content} {
				if !strings.Contains(content, label) || !strings.Contains(content, "Imported v4") || strings.Contains(content, "Warning recorded.") {
					t.Fatalf("misleading imported history: %s", content)
				}
			}
			if !strings.Contains(detail.Content, "https://discord.com/channels/1/2/3") || !strings.Contains(detail.Content, "Original reason") || !strings.Contains(detail.Content, "No new action was performed") {
				t.Fatalf("missing historical detail: %s", detail.Content)
			}
		})
	}
	for _, unsafe := range []string{"javascript:alert(1)", "https://name:secret@example.com/", "//example.com"} {
		if historicalContextLink(unsafe) != "" {
			t.Fatalf("unsafe context rendered: %q", unsafe)
		}
	}
}
