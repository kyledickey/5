package ui

import (
	"io"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
)

// TestTextTransportsPreserveLongContentAndControls covers the smaller text limit,
// Unicode, mention suppression, and attachment replacement during pagination.
func TestTextTransportsPreserveLongContentAndControls(t *testing.T) {
	body := Conversation("case", "Case for <@123>.", strings.Repeat("🦆", 1100), "Next steps.", "Case #12", true)
	body.Components = []discordgo.MessageComponent{Row(Button("case:void:v1:case", "Void case", discordgo.SecondaryButton, false))}
	prepared := body.ForApplication("819019613371236432")
	if len(utf16.Encode([]rune(prepared.Content))) > 2000 || !strings.Contains(prepared.Content, "<:quack_case:") || len(prepared.Files) != 1 || len(prepared.Components) != 1 {
		t.Fatalf("invalid message: %+v", prepared)
	}
	full, err := io.ReadAll(prepared.Files[0].Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), strings.Repeat("🦆", 1100)) || !strings.Contains(string(full), "Case #12") {
		t.Fatal("long content lost")
	}
	data := prepared.ResponseData()
	if len(data.Files) != 1 || data.Flags&discordgo.MessageFlagsEphemeral == 0 || data.AllowedMentions == nil || len(data.AllowedMentions.Parse) != 0 {
		t.Fatal("initial response lost attachments or privacy")
	}
	sent := body.SendParams("968198214450831370")
	if len(sent.Files) != 1 || sent.AllowedMentions == nil || len(sent.AllowedMentions.Parse) != 0 || sent.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Fatal("channel send lost safe text presentation")
	}
	edit := EditMessage(Notice("Done.", true)).ForApplication("819019613371236432").WebhookEdit()
	if edit.Embeds == nil || len(*edit.Embeds) != 0 || edit.Attachments == nil || len(*edit.Attachments) != 0 {
		t.Fatal("short edit did not clear previous card/file")
	}
}

// TestTextLimitHandlesAnUnbrokenParagraph avoids cutting an emoji or Markdown link.
func TestTextLimitHandlesAnUnbrokenParagraph(t *testing.T) {
	m := Content(strings.Repeat("🦆", 1001), false).ForApplication("unknown")
	if len(m.Files) != 1 || m.Content != "The full message is attached." {
		t.Fatal(m.Content)
	}
	m = Content(strings.Repeat("🦆", 1000), false).ForApplication("unknown")
	if len(m.Files) != 0 {
		t.Fatal("a message at the limit was unnecessarily attached")
	}
}

// TestPrepareResponseLeavesFormsUntouched prevents message formatting from
// corrupting modal IDs, autocomplete choices, or deferred acknowledgements.
func TestPrepareResponseLeavesFormsUntouched(t *testing.T) {
	modal := Modal("Tell us more", "case:context:v1:1", nil)
	if PrepareResponse(modal, "819019613371236432") != modal {
		t.Fatal("modal was replaced")
	}
	response := Error("Try again.")
	prepared := PrepareResponse(response, "819019613371236432")
	if !strings.Contains(prepared.Data.Content, "<:quack_error:") || prepared.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(response.Data.Content, "{{quack:") {
		t.Fatal("response resolution mutated the source or lost visibility")
	}
}
