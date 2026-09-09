package ui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

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
	if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
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
	if !reflect.DeepEqual(responder.calls, []string{"delete", "followup"}) || !responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "Manage Server") {
		t.Fatalf("error became public or duplicated: %+v", responder)
	}
}
