package ui_test

import (
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

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
