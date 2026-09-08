package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// failingCasePublication simulates delivery and cleanup failures after moderation
// has already committed, without obscuring the persisted private receipt.
type failingCasePublication struct {
	fakeResponder
	failPublish bool
	failCleanup bool
}

// Followup fails only public publication, preserving the earlier private edit.
func (r *failingCasePublication) Followup(message ui.Message) (*discordgo.Message, error) {
	if r.failPublish {
		return nil, errors.New("Discord unavailable")
	}
	return r.fakeResponder.Followup(message)
}

// DeleteOriginal simulates failure to remove the now-redundant private copy.
func (r *failingCasePublication) DeleteOriginal() error {
	if r.failCleanup {
		return errors.New("Discord unavailable")
	}
	return r.fakeResponder.DeleteOriginal()
}

// TestCasePublicationFailureKeepsSuccessfulReceipt ensures the dispatcher cannot
// replace an already-created case with a generic command-failed response.
func TestCasePublicationFailureKeepsSuccessfulReceipt(t *testing.T) {
	for _, failure := range []string{"publish", "cleanup"} {
		t.Run(failure, func(t *testing.T) {
			responder := &failingCasePublication{failPublish: failure == "publish", failCleanup: failure == "cleanup"}
			created := &quack.CaseResponse{ID: "case", CaseNumber: 12, TargetDiscordUserID: "member", Reason: "Rule violation"}
			if err := publishPrivateContextCase(context.Background(), responder, nil, created, nil); err != nil {
				t.Fatalf("committed case reported as failed: %v", err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "12") {
				t.Fatalf("case receipt lost: %+v", responder.edit.Content)
			}
			if failure == "publish" && (!strings.Contains(*responder.edit.Content, "case was created") || responder.deleted) {
				t.Fatal("publication failure discarded successful private result")
			}
			if failure == "cleanup" && responder.followup.Content == "" {
				t.Fatal("public receipt missing")
			}
		})
	}
}
