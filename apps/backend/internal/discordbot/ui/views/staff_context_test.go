package views

import (
	"github.com/quackdiscord/bot/internal/quack"
	"strings"
	"testing"
	"unicode/utf16"
)

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
