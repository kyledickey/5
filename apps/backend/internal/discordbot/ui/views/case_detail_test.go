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
	for _, required := range []string{"Case #7 · <@target>.", "> Official reason", "Timeout couldn’t be completed", "permission denied", "> Details — Visible context", "[View message](https://discord.com/channels/1/2/3)", "Case created", "-# Case #7"} {
		if !strings.Contains(message.Content, required) {
			t.Fatalf("missing %q from staff conversation: %s", required, message.Content)
		}
	}
	row := message.Components[0].(discordgo.ActionsRow)
	if len(row.Components) != 4 {
		t.Fatalf("expected context, evidence, user, and void controls: %+v", row)
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
		if message.Ephemeral || len(message.Files) != 0 || len(utf16.Encode([]rune(message.Content))) > 2000 || len(message.Components) != 3 {
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
	for _, text := range []string{"21 total · 17 active · 4 voided", "Imported v4", "Page 3/3"} {
		if !strings.Contains(message.Content, text) {
			t.Fatalf("missing %q: %s", text, message.Content)
		}
	}
	if message.Ephemeral || len(message.Components) == 0 {
		t.Fatal("public profile or pagination lost")
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

// TestEvidenceSnapshotPagesRetainNativeContent covers empty, upload-only and
// long-text evidence without discarding attachment warnings or forcing a file.
func TestEvidenceSnapshotPagesRetainNativeContent(t *testing.T) {
	for _, evidence := range [][]quack.CaseEvidenceResponse{nil, {{CaptureOutcome: "uploaded", Attachments: []quack.CaseEvidenceAttachmentResponse{{Filename: "proof.png", PreservedURL: "https://discord.com/channels/g/c/m", CopyOutcome: "preserved"}}}}, {{Content: strings.Repeat("Retained text. ", 300)}}} {
		detail := &quack.CaseEvidencePageResponse{CaseDetailResponse: quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case", CaseNumber: 9}, Evidence: evidence}, Position: 1, Total: 1}
		if evidence == nil {
			detail.Position = 0
			detail.Total = 0
		}
		pages := CaseEvidenceSnapshotPages(detail, "")
		for page := range pages {
			message := CaseEvidenceSnapshotPage(detail, page+1, "")
			if len(message.Files) != 0 || len([]rune(message.Content)) > 2000 {
				t.Fatal("evidence left native page", message)
			}
		}
		if len(evidence) > 0 && evidence[0].CaptureOutcome == "uploaded" && !strings.Contains(strings.Join(pages, ""), "https://discord.com/channels/g/c/m") {
			t.Fatal("upload link missing")
		}
	}
}

// TestEvidenceSummaryCaptureLabels keeps native headings readable while retaining
// captured content, original message links, attachment outcomes and warnings.
func TestEvidenceSummaryCaptureLabels(t *testing.T) {
	for outcome, label := range map[string]string{
		"uploaded": "Uploaded file", "captured": "Captured message",
		"unavailable": "Capture unavailable", "deleted": "Message deleted or missing",
		"inaccessible": "Message inaccessible", "": "Evidence", "future_status": "Evidence",
	} {
		t.Run(outcome, func(t *testing.T) {
			item := quack.CaseEvidenceResponse{CaptureOutcome: outcome, Content: "Captured text", CaptureWarning: "Capture warning", Attachments: []quack.CaseEvidenceAttachmentResponse{{Filename: "proof.png", PreservedURL: "https://example.com/proof.png", CopyOutcome: "preserved", Warning: "Attachment warning"}}}
			text := evidenceSummary([]quack.CaseEvidenceResponse{item})
			if !strings.HasPrefix(text, label+"\n") {
				t.Fatalf("wrong capture heading: %s", text)
			}
			for _, retained := range []string{"Captured text", "Capture warning", "[proof.png](https://example.com/proof.png) · Saved copy", "Attachment warning"} {
				if !strings.Contains(text, retained) {
					t.Fatalf("lost evidence detail %q: %s", retained, text)
				}
			}
			item.MessageURL = "https://discord.com/channels/1/2/3"
			if text := evidenceSummary([]quack.CaseEvidenceResponse{item}); !strings.HasPrefix(text, "[View message]("+item.MessageURL+")\n") {
				t.Fatalf("message navigation changed: %s", text)
			}
		})
	}
}

// TestEvidenceSummaryExplainsCopyFailures keeps saved and unconfirmed files
// distinct, with a single useful warning and the original navigation intact.
func TestEvidenceSummaryExplainsCopyFailures(t *testing.T) {
	for _, tt := range []struct{ outcome, warning, label, explanation string }{
		{"preserved", "", "Saved copy", ""},
		{"metadata_only", "attachment copy failed; original metadata retained", "No confirmed copy", "Quack could not save this file."},
		{"metadata_only", "attachment copy could not be confirmed; original metadata retained", "No confirmed copy", "Quack could not confirm a saved copy."},
		{"metadata_only", "managed evidence channel is unavailable", "No confirmed copy", "Ask an administrator to check it."},
		{"metadata_only", "attachment exceeds the managed copy size limit", "No confirmed copy", "This file is too large to save."},
		{"metadata_only", "attachment type is not eligible for managed copying", "No confirmed copy", "This file type cannot be saved."},
		{"copied", "Useful historical warning", "Copy status unavailable", "Useful historical warning"},
	} {
		t.Run(tt.outcome+tt.warning, func(t *testing.T) {
			item := quack.CaseEvidenceResponse{CaptureOutcome: "uploaded", CaptureWarning: tt.warning, Attachments: []quack.CaseEvidenceAttachmentResponse{{Filename: "proof.png", OriginalURL: "https://example.com/original", CopyOutcome: tt.outcome, Warning: tt.warning}}}
			if tt.outcome == "preserved" {
				item.Attachments[0].PreservedURL = "https://discord.com/channels/g/c/m"
			}
			text := evidenceSummary([]quack.CaseEvidenceResponse{item})
			link := item.Attachments[0].OriginalURL
			if tt.outcome == "preserved" {
				link = item.Attachments[0].PreservedURL
			}
			if !strings.Contains(text, "[proof.png]("+link+") · "+tt.label) {
				t.Fatalf("lost copy status or link: %s", text)
			}
			if tt.explanation != "" && strings.Count(text, tt.explanation) != 1 {
				t.Fatalf("missing or duplicated warning: %s", text)
			}
			if strings.Contains(text, "metadata_only") || strings.Contains(text, "original metadata retained") {
				t.Fatalf("storage language leaked: %s", text)
			}
		})
	}
}

// TestEvidenceSummaryKeepsDistinctFailures removes the snapshot's duplicated
// attachment warning while retaining truncation and a second file's own failure.
func TestEvidenceSummaryKeepsDistinctFailures(t *testing.T) {
	const failed = "attachment copy failed; original metadata retained"
	item := quack.CaseEvidenceResponse{CaptureOutcome: "captured", Content: "Retained staff text", CaptureWarning: "message content snapshot was truncated; " + failed + "; Custom snapshot warning", Attachments: []quack.CaseEvidenceAttachmentResponse{
		{Filename: "first.png", CopyOutcome: "metadata_only", Warning: failed},
		{Filename: "second.png", CopyOutcome: "metadata_only", Warning: "Custom second-file warning"},
	}}
	text := evidenceSummary([]quack.CaseEvidenceResponse{item})
	for _, want := range []string{"Retained staff text", "Only part of the message text was saved.", "Quack could not save this file.", "Custom snapshot warning", "Custom second-file warning"} {
		if strings.Count(text, want) != 1 {
			t.Fatalf("lost or duplicated %q: %s", want, text)
		}
	}
	if strings.Contains(text, failed) {
		t.Fatalf("raw warning retained: %s", text)
	}
}

// TestEvidenceShowsCompleteStaffContext verifies notes survive pagination even
// when there are no captured messages or attachments on the case.
func TestEvidenceShowsCompleteStaffContext(t *testing.T) {
	detail := &quack.CaseEvidencePageResponse{CaseDetailResponse: quack.CaseDetailResponse{CaseResponse: quack.CaseResponse{ID: "case", CaseNumber: 7, ContextValues: []quack.CaseContextValueResponse{{Label: "Context", Value: strings.Repeat("Moderator note. ", 300) + "FINAL NOTE"}}}}}
	pages := CaseEvidenceSnapshotPages(detail, "")
	content := ""
	for i := range pages {
		message := CaseEvidenceSnapshotPage(detail, i+1, "")
		if message.Ephemeral || len(utf16.Encode([]rune(message.Content))) > 2000 {
			t.Fatal("invalid staff evidence page")
		}
		content += message.Content
	}
	if !strings.Contains(content, "Moderator note") || !strings.Contains(content, "FINAL NOTE") {
		t.Fatal("context was lost")
	}
}
