package ui_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

func TestPublicAndEphemeralResponses(t *testing.T) {
	public := ui.Public(ui.Content("visible", false))
	if public.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("unexpected public response type: %v", public.Type)
	}
	if public.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("expected public response without ephemeral flag")
	}

	ephemeral := ui.Ephemeral(ui.Content("private", false))
	if ephemeral.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected ephemeral response flag")
	}
}

func TestDeferredResponses(t *testing.T) {
	public := ui.DeferPublic()
	if public.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected defer public type: %v", public.Type)
	}
	if public.Data != nil && public.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatalf("expected public defer without ephemeral flag")
	}

	ephemeral := ui.DeferEphemeral()
	if ephemeral.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected defer ephemeral type: %v", ephemeral.Type)
	}
	if ephemeral.Data == nil || ephemeral.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected ephemeral defer flag")
	}
}

func TestEmbedTruncatesByRuneLimit(t *testing.T) {
	embed := ui.NewEmbed().
		SetTitle(strings.Repeat("t", ui.EmbedTitleLimit+10)).
		SetDescription(strings.Repeat("d", ui.EmbedDescriptionLimit+10)).
		Build()
	fieldEmbed := ui.NewEmbed().
		AddField(strings.Repeat("n", ui.EmbedFieldNameLimit+10), strings.Repeat("v", ui.EmbedFieldValueLimit+10), false).
		SetFooter(strings.Repeat("f", ui.EmbedFooterLimit+10)).
		Build()

	if len([]rune(embed.Title)) != ui.EmbedTitleLimit {
		t.Fatalf("title was not truncated")
	}
	if len([]rune(embed.Description)) != ui.EmbedDescriptionLimit {
		t.Fatalf("description was not truncated")
	}
	if len([]rune(fieldEmbed.Fields[0].Name)) != ui.EmbedFieldNameLimit {
		t.Fatalf("field name was not truncated")
	}
	if len([]rune(fieldEmbed.Fields[0].Value)) != ui.EmbedFieldValueLimit {
		t.Fatalf("field value was not truncated")
	}
	if len([]rune(fieldEmbed.Footer.Text)) != ui.EmbedFooterLimit {
		t.Fatalf("footer was not truncated")
	}
}

func TestEmbedHelpersBuildPresetEmbedsAndMessages(t *testing.T) {
	embed := ui.NewInfoEmbed("Title", "Description").
		AddField("", "", true).
		AddFields(ui.Field("Count", 3, true)).
		SetAuthor("Quack", "https://example.com/icon.png").
		SetThumbnail("https://example.com/thumb.png").
		SetNamedColor("error").
		Build()

	if embed.Color != ui.ColorError {
		t.Fatalf("expected named color to set error color, got %d", embed.Color)
	}
	if embed.Fields[0].Name != "\u200b" || embed.Fields[0].Value != "\u200b" {
		t.Fatalf("expected blank fields to use zero-width placeholders, got %+v", embed.Fields[0])
	}
	if embed.Fields[1].Value != "3" {
		t.Fatalf("expected field helper to stringify values, got %+v", embed.Fields[1])
	}
	if embed.Author == nil || embed.Author.Name != "Quack" {
		t.Fatalf("expected author metadata, got %+v", embed.Author)
	}
	if embed.Thumbnail == nil || embed.Thumbnail.URL == "" {
		t.Fatalf("expected thumbnail metadata, got %+v", embed.Thumbnail)
	}

	message := ui.EmbedMessage(embed, true)
	data := message.ResponseData()
	if len(data.Embeds) != 1 || data.Embeds[0] != embed {
		t.Fatalf("expected embed response data, got %+v", data.Embeds)
	}
	if data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected embed message to preserve ephemeral flag")
	}
}

func TestErrorResponsesUseConversationAndClearOldEmbeds(t *testing.T) {
	response := ui.Error("Nope")
	if response.Data == nil || response.Data.Content != "{{quack:error}} Nope" || len(response.Data.Embeds) != 0 || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private conversational error: %+v", response)
	}
	edit := ui.ErrorEdit("Nope").WebhookEdit()
	if edit.Content == nil || *edit.Content != "{{quack:error}} Nope" || edit.Embeds == nil || len(*edit.Embeds) != 0 {
		t.Fatalf("expected text error edit clearing previous embeds: %+v", edit)
	}
}

func TestCustomIDCodec(t *testing.T) {
	encoded, err := ui.EncodeCustomID(ui.CustomID{
		Namespace: "case",
		Action:    "next",
		Version:   "v1",
		Payload:   "target=123",
	})
	if err != nil {
		t.Fatalf("encode custom id: %v", err)
	}
	if encoded != "case:next:v1:target=123" {
		t.Fatalf("unexpected custom id: %q", encoded)
	}

	decoded, err := ui.DecodeCustomID(encoded)
	if err != nil {
		t.Fatalf("decode custom id: %v", err)
	}
	if decoded.Namespace != "case" || decoded.Action != "next" || decoded.Version != "v1" || decoded.Payload != "target=123" {
		t.Fatalf("unexpected decoded custom id: %+v", decoded)
	}
}

func TestCustomIDRejectsInvalidAndTooLongValues(t *testing.T) {
	if _, err := ui.DecodeCustomID("case:missing"); !errors.Is(err, ui.ErrCustomIDInvalid) {
		t.Fatalf("expected invalid custom id error, got %v", err)
	}
	_, err := ui.EncodeCustomID(ui.CustomID{
		Namespace: "case",
		Action:    "next",
		Version:   "v1",
		Payload:   strings.Repeat("x", ui.CustomIDLimit),
	})
	if !errors.Is(err, ui.ErrCustomIDTooLong) {
		t.Fatalf("expected too long custom id error, got %v", err)
	}
}

func FuzzCustomIDCodec(f *testing.F) {
	f.Add("case:next:v1:target=123")
	f.Add("case:missing")
	f.Add("")
	f.Fuzz(func(t *testing.T, encoded string) {
		decoded, err := ui.DecodeCustomID(encoded)
		if err != nil {
			return
		}
		roundTrip, err := ui.EncodeCustomID(decoded)
		if err != nil {
			t.Fatalf("decoded custom ID could not be encoded: %v", err)
		}
		if roundTrip != strings.TrimSpace(encoded) {
			t.Fatalf("custom ID round trip changed value: got %q want %q", roundTrip, strings.TrimSpace(encoded))
		}
	})
}
