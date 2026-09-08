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
			if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Task == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Fatalf("selection was not deferred: %+v", result)
			}
			_, services, _ := newCaseCommandHarnessWithLivePermissions(t, 0)
			result = selection.handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "permission") || responder.followup.Content != "" {
				t.Fatalf("missing permission rejection: %+v", responder.edit.Content)
			}
		})
	}
}

// TestContextCommandsDeferBeforeLookups exercises both entry points without
// services, then checks live permission rejection stays in the private response.
func TestContextCommandsDeferBeforeLookups(t *testing.T) {
	for _, kind := range []string{"user", "message"} {
		t.Run(kind, func(t *testing.T) {
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
			data := discordgo.ApplicationCommandInteractionData{TargetID: "selected", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{}}
			handler := HandleUserCaseInteraction
			if kind == "user" {
				data.Resolved.Users = map[string]*discordgo.User{"selected": {ID: "selected"}}
			} else {
				handler = HandleMessageCaseInteraction
				data.Resolved.Messages = map[string]*discordgo.Message{"selected": {ID: "selected", ChannelID: "channel", Author: &discordgo.User{ID: "target"}}}
			}
			interaction.Data = data
			result := handler(ui.Context{Context: context.Background(), Interaction: interaction})
			if result.Task == nil || result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Fatalf("not privately deferred: %+v", result)
			}
			_, services, _ := newCaseCommandHarnessWithLivePermissions(t, 0)
			result = handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "permission") || responder.followup.Content != "" {
				t.Fatalf("permission failure leaked or missing: %+v", responder)
			}
		})
	}
}

// TestSingleTemplateContextPublishesPublicCase confirms private acknowledgement
// precedes a standalone public notice without using the interaction webhook.
func TestSingleTemplateContextPublishesPublicCase(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
	interaction.Data = discordgo.ApplicationCommandInteractionData{TargetID: "target", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{"target": {ID: "target"}}}}
	result := HandleUserCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.channelPublishes != 1 || responder.webhookFollowups != 0 || responder.editCount == 0 || responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "<@target>") || responder.deleted {
		t.Fatalf("public result failed: %+v", responder)
	}
}
