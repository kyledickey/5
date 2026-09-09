package views

import (
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

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
