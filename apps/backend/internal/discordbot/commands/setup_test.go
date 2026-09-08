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
	if definition == nil || len(definition.Options) != 2 {
		t.Fatal("missing ticket setup destinations")
	}
	for _, option := range definition.Options {
		if !option.Required || option.Type != discordgo.ApplicationCommandOptionChannel {
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
			if len(option.Options) != 1 || option.Options[0].Name != "warning" || option.Options[0].Required {
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
