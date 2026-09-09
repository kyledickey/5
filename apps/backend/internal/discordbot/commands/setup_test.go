package commands

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// TestSetupRoutesTicketsToModule checks that the registered slash command reaches
// the integration handler rather than falling through to appeal configuration.
func TestSetupRoutesTicketsToModule(t *testing.T) {
	called := false
	spec := SetupCommandSpec(SetupHandlers{Tickets: func(ui.Context) ui.HandlerResult { called = true; return ui.Immediate(ui.Error("test")) }})
	var definition *discordgo.ApplicationCommandOption
	for _, option := range spec.Definition.Options {
		if option.Name == "tickets" {
			definition = option
		}
	}
	if definition == nil || len(definition.Options) != 3 {
		t.Fatal("missing ticket setup destinations")
	}
	for _, option := range definition.Options {
		if option.Required || (option.Type != discordgo.ApplicationCommandOptionChannel && !(option.Name == "enabled" && option.Type == discordgo.ApplicationCommandOptionBoolean)) {
			t.Fatalf("invalid destination option: %+v", option)
		}
	}
	spec.Handler(ui.Context{Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "guild", Data: discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "tickets", Type: discordgo.ApplicationCommandOptionSubCommand}}}}}})
	if !called {
		t.Fatal("ticket setup did not reach module handler")
	}
}

// TestSetupRoutesHoneypotToModule verifies trap setup is exposed with an optional
// warning and routed independently from tickets and appeal configuration.
func TestSetupRoutesHoneypotToModule(t *testing.T) {
	called := false
	spec := SetupCommandSpec(SetupHandlers{Honeypot: func(ui.Context) ui.HandlerResult { called = true; return ui.Immediate(ui.Error("test")) }})
	var found bool
	for _, option := range spec.Definition.Options {
		if option.Name == "honeypot" {
			found = true
			if len(option.Options) != 3 || option.Options[1].Name != "warning" || option.Options[1].Required || option.Options[0].Name != "channel" || option.Options[0].Required {
				t.Fatalf("incorrect warning option: %+v", option)
			}
		}
	}
	if !found {
		t.Fatal("honeypot setup missing")
	}
	spec.Handler(ui.Context{Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "guild", Data: discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "honeypot", Type: discordgo.ApplicationCommandOptionSubCommand}}}}}})
	if !called {
		t.Fatal("honeypot setup handler not called")
	}
}

func TestSetupRoutesLoggingToModule(t *testing.T) {
	called := false
	spec := SetupCommandSpec(SetupHandlers{Logging: func(ui.Context) ui.HandlerResult { called = true; return ui.Immediate(ui.Error("test")) }})
	spec.Handler(ui.Context{Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Type: discordgo.InteractionApplicationCommand, GuildID: "guild", Data: discordgo.ApplicationCommandInteractionData{Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "logging", Type: discordgo.ApplicationCommandOptionSubCommand}}}}}})
	if !called {
		t.Fatal("logging setup handler not called")
	}
}

// assertPublicCommandAcknowledgement requires an attributed public defer before
// any slow lookups, while tolerating Discord's omitted flags object.
func assertPublicCommandAcknowledgement(t *testing.T, result ui.HandlerResult) {
	t.Helper()
	if result.Task == nil || result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil && result.Response.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Fatal("command did not defer publicly")
	}
}

// commandFeedback checks success remains one original response and handled
// failures remove the placeholder and send exactly one private error instead.
func commandFeedback(t *testing.T, responder *fakeResponder, success bool) string {
	t.Helper()
	if success {
		if responder.editCount != 1 || responder.edit.Content == nil || responder.deleted || responder.webhookFollowups != 0 || responder.channelPublishes != 0 {
			t.Fatalf("success was not one original response: %+v", responder)
		}
		return *responder.edit.Content
	}
	if responder.editCount != 0 || !responder.deleted || responder.webhookFollowups != 1 || !responder.followup.Ephemeral || responder.channelPublishes != 0 {
		t.Fatalf("error was public or duplicated: %+v", responder)
	}
	return responder.followup.Content
}
