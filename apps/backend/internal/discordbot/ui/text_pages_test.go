package ui

import (
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// TestTextPagesPreservesLongUnicodeRecords verifies Discord's UTF-16 limit without
// losing whitespace, splitting Unicode encodings, or dropping uninterrupted text.
func TestTextPagesPreservesLongUnicodeRecords(t *testing.T) {
	for _, source := range []string{"", strings.Repeat("🦆", 3000), strings.Repeat("message with spaces\n", 300), strings.Repeat("x", 5000)} {
		pages := TextPages(source, 1600)
		if strings.Join(pages, "") != source {
			t.Fatal("pagination changed the captured record")
		}
		for _, page := range pages {
			if !utf8.ValidString(page) || len(utf16.Encode([]rune(page))) > 1600 {
				t.Fatal("page exceeds Discord text budget or contains invalid Unicode")
			}
		}
	}
}

// TestTextPagesKeepsEvidenceLinksClickable preserves labels containing spaces at
// a page boundary instead of turning their URL into text on the next page.
func TestTextPagesKeepsEvidenceLinksClickable(t *testing.T) {
	link := "[Screenshot of the message](https://example.com/evidence.png)"
	source := strings.Repeat("x", 1580) + "\n" + link
	pages := TextPages(source, 1600)
	if len(pages) != 2 || pages[1] != link || strings.Join(pages, "") != source {
		t.Fatalf("evidence link was split: %q", pages)
	}
}
