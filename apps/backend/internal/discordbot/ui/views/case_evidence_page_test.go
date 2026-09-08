package views

import (
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

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
