package commands

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestEvidenceNavigationRechecksAuthority prevents a previously authorized
// private evidence message from retaining access after a moderator loses roles.
func TestEvidenceNavigationRechecksAuthority(t *testing.T) {
	_, services, _ := newCaseCommandHarnessWithLivePermissions(t, 0)
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
	interaction.Type = discordgo.InteractionMessageComponent
	interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "evidence_next", Version: "v1", Payload: "1|case-1"})}
	result := pageEvidence(1)(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatal("evidence navigation did not acknowledge before its live lookup")
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("stale interaction permissions were trusted: %v", err)
	}
	if responder.editCount != 0 || responder.followup.Content != "" {
		t.Fatal("revoked moderator received evidence")
	}
}
