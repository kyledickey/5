package ui_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestPublicAndEphemeralResponses proves Public clears and Ephemeral sets the ephemeral flag.
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

// TestDeferredResponses proves DeferPublic and DeferEphemeral differ only in visibility.
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

// TestErrorResponsesUseConversationAndClearOldEmbeds proves errors are private
// text with the error icon and that an error edit removes previous embeds.
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

// TestCustomIDCodec proves EncodeCustomID and DecodeCustomID round-trip a routing ID.
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

// TestCustomIDRejectsInvalidAndTooLongValues proves malformed and oversized IDs
// return their sentinel errors.
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

// FuzzCustomIDCodec proves any value DecodeCustomID accepts re-encodes to itself.
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

// deferredResponder records edits; unexpected followup/deletion calls panic via
// the embedded nil interface, catching accidental replacement of the original.
type deferredResponder struct {
	ui.Responder
	edit    ui.Edit
	editErr error
	edits   int
}

// EditOriginal models Discord retaining the original response's identity.
func (r *deferredResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	r.edit, r.edits = edit, r.edits+1
	if r.editErr != nil {
		return nil, r.editErr
	}
	return &discordgo.Message{ID: "original"}, nil
}

// TestPublishEditsTheDeferredOriginal preserves the reply decorator by never
// creating a separate result, deleting the defer, or adding an interim message.
func TestPublishEditsTheDeferredOriginal(t *testing.T) {
	for _, failure := range []error{nil, errors.New("transport failed")} {
		responder := &deferredResponder{editErr: failure}
		result, err := ui.Publish(responder, ui.Content("Case added.", false))
		if responder.edits != 1 || responder.edit.Content == nil || *responder.edit.Content != "Case added." {
			t.Fatalf("unexpected edits: %+v", responder)
		}
		if failure != nil {
			if !errors.Is(err, failure) || result != nil {
				t.Fatalf("edit failure was lost: %v", err)
			}
		} else if err != nil || result == nil || result.ID != "original" {
			t.Fatalf("response identity changed: %+v %v", result, err)
		}
	}
}

// publicTaskResponder records only expected operations; unexpected methods fail
// through the nil embedded interface instead of quietly accepting extra notices.
type publicTaskResponder struct {
	ui.Responder
	calls    []string
	edit     ui.Edit
	followup ui.Message
}

// EditOriginal records the single successful command response.
func (r *publicTaskResponder) EditOriginal(edit ui.Edit) (*discordgo.Message, error) {
	r.calls = append(r.calls, "edit")
	r.edit = edit
	return &discordgo.Message{ID: "original"}, nil
}

// DeleteOriginal records removal of the public pending acknowledgement.
func (r *publicTaskResponder) DeleteOriginal() error { r.calls = append(r.calls, "delete"); return nil }

// Followup records the private failure response.
func (r *publicTaskResponder) Followup(message ui.Message) (*discordgo.Message, error) {
	r.calls = append(r.calls, "followup")
	r.followup = message
	return &discordgo.Message{ID: "private"}, nil
}

// TestAsyncPublicKeepsOneAttributedSuccess verifies successful work updates its
// original public response without deleting it or creating a second message.
func TestAsyncPublicKeepsOneAttributedSuccess(t *testing.T) {
	result := ui.AsyncPublic(func(_ context.Context, r ui.Responder) error {
		_, err := r.EditOriginal(ui.EditMessage(ui.Content("Saved.", false)))
		return err
	})
	if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource ||
		result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatal("success did not start publicly")
	}
	responder := &publicTaskResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(responder.calls, []string{"edit"}) || responder.edit.Content == nil || *responder.edit.Content != "Saved." {
		t.Fatalf("success created extra responses: %+v", responder)
	}
}

// TestAsyncPublicRemovesPlaceholderAndKeepsErrorPrivate verifies a handled error
// never edits its details into the public response and produces one private reply.
func TestAsyncPublicRemovesPlaceholderAndKeepsErrorPrivate(t *testing.T) {
	result := ui.AsyncPublic(func(_ context.Context, r ui.Responder) error {
		_, err := r.EditOriginal(ui.ErrorEdit("You need Manage Server permission."))
		return err
	})
	responder := &publicTaskResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(responder.calls, []string{"delete", "followup"}) || !responder.followup.Ephemeral ||
		!strings.Contains(responder.followup.Content, "Manage Server") {
		t.Fatalf("error became public or duplicated: %+v", responder)
	}
}
