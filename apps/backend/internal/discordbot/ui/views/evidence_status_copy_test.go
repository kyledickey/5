package views

import (
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

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
