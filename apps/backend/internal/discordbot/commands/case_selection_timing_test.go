package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestTemplateSelectionDefersLiveAuthorization protects the response deadline
// and prevents a stale picker from granting revoked moderator authority.
func TestTemplateSelectionDefersLiveAuthorization(t *testing.T) {
	for _, selection := range []struct {
		action, payload string
		handler         ui.Handler
	}{
		{"user_template", "target", handleUserTemplateComponent},
		{"message_template", "target|channel|message", handleMessageTemplateComponent},
	} {
		t.Run(selection.action, func(t *testing.T) {
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
			interaction.Type = discordgo.InteractionMessageComponent
			interaction.Data = discordgo.MessageComponentInteractionData{CustomID: ui.MustCustomID(ui.CustomID{Namespace: "case", Action: selection.action, Version: "v1", Payload: selection.payload}), Values: []string{"template"}}
			// Missing services prove that the initial handler performs no eager lookup.
			result := selection.handler(ui.Context{Context: context.Background(), Interaction: interaction})
			if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Task == nil {
				t.Fatalf("selection was not deferred: %+v", result)
			}
			_, services, _ := newCaseCommandHarnessWithLivePermissions(t, 0)
			result = selection.handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "permission") {
				t.Fatalf("missing permission rejection: %+v", responder.edit.Content)
			}
		})
	}
}
