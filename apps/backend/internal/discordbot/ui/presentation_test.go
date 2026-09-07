package ui

import (
	"strings"
	"testing"
)

// TestEmbedAggregateBudget keeps long staff details sendable without mutating the reusable builder.
func TestEmbedAggregateBudget(t *testing.T) {
	builder := NewInfoEmbed("Case", strings.Repeat("a", 4000)).SetFooter("Case #12")
	for i := 0; i < 10; i++ {
		builder.AddField("Details", strings.Repeat("b", 1024), false)
	}
	embed := builder.Build()
	total := len([]rune(embed.Title)) + len([]rune(embed.Description)) + len([]rune(embed.Footer.Text))
	for _, field := range embed.Fields {
		total += len([]rune(field.Name)) + len([]rune(field.Value))
	}
	if total > 6000 || len(embed.Fields) == 0 {
		t.Fatalf("invalid bounded card: %d characters", total)
	}
	if len(builder.embed.Fields) != 10 {
		t.Fatal("building a card changed its source")
	}
}
