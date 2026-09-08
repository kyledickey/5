package views

import (
	"strings"
	"testing"
)

// TestLogAttachmentLabelsRejectUnsafeDestinations keeps malformed historical or
// forged payloads from injecting Markdown links, while retaining file names.
func TestLogAttachmentLabelsRejectUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{"", "javascript:alert(1)", "https://user:secret@example.com/proof", "https://example.com/a>)[evil](https://other.test)", "https://example.com/a\nb"} {
		if got := (logAttachment{Filename: "proof.png", URL: raw}).label(); got != "proof.png" {
			t.Fatalf("unsafe destination rendered: %q", got)
		}
	}
	got := (logAttachment{Filename: "[proof].png", URL: "https://cdn.discordapp.com/a(b).png"}).label()
	if !strings.Contains(got, "\\[proof\\]") || !strings.Contains(got, "(<https://cdn.discordapp.com/a(b).png>)") {
		t.Fatal(got)
	}
}
