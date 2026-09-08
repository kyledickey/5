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
	spec := SetupCommandSpec(func(ui.Context) ui.HandlerResult { called = true; return ui.Immediate(ui.Error("test")) })
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
