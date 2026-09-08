package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestTemplateCreateModalActivatesSelectedPolicy exercises the command and form
// through real template persistence, including defaults and live manager denial.
func TestTemplateCreateModalActivatesSelectedPolicy(t *testing.T) {
	for _, scenario := range []struct {
		outcome string
		minutes int64
		action  model.ActionType
		allowed bool
	}{
		{"warning", 0, "", true}, {"timeout", 60, model.ActionTimeoutUser, true}, {"kick", 0, model.ActionKickUser, true}, {"ban", 0, model.ActionBanUser, true}, {"ban", 0, model.ActionBanUser, false},
	} {
		name := scenario.outcome
		if !scenario.allowed {
			name += "-revoked"
		}
		t.Run(name, func(t *testing.T) {
			permission := uint64(discordgo.PermissionManageGuild)
			if !scenario.allowed {
				permission = uint64(discordgo.PermissionModerateMembers)
			}
			_, services, _ := newCaseCommandHarnessWithLivePermissions(t, permission)
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
			interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "template", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "create", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "outcome", Type: discordgo.ApplicationCommandOptionString, Value: scenario.outcome}, {Name: "minutes", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(scenario.minutes)}}}}}
			result := handleTemplateCommand(ui.Context{Context: context.Background(), Interaction: interaction})
			if result.Response == nil || result.Response.Type != discordgo.InteractionResponseModal {
				t.Fatalf("missing form: %+v", result)
			}
			interaction.Type = discordgo.InteractionModalSubmit
			interaction.Data = discordgo.ModalSubmitInteractionData{CustomID: result.Response.Data.CustomID, Components: []discordgo.MessageComponent{
				ui.Row(discordgo.TextInput{CustomID: "name", Value: "New rule"}), ui.Row(discordgo.TextInput{CustomID: "reason", Value: "Keep chat appropriate."}),
			}}
			result = handleTemplateCreateSubmit(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			if result.Task == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
				t.Fatal("submission not deferred privately")
			}
			if err := result.Task(context.Background(), &fakeResponder{}); err != nil {
				t.Fatal(err)
			}
			templates, err := services.Templates.ListActive(context.Background(), caseCommandGuildContext(t, services))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, template := range templates {
				if template.Name != "New rule" {
					continue
				}
				found = true
				if template.ReasonTemplate != "Keep chat appropriate." || !template.Appealable || len(template.Levels) != 1 || !template.Levels[0].NotifyUser || !template.Levels[0].IsDefault {
					t.Fatalf("wrong policy: %+v", template)
				}
				actions := template.Levels[0].Actions
				if scenario.action == "" {
					if len(actions) != 0 {
						t.Fatal("warning unexpectedly punishes")
					}
				} else if len(actions) != 1 || actions[0].ActionType != scenario.action || actions[0].TimeoutDurationSeconds != int(scenario.minutes*60) {
					t.Fatalf("wrong action: %+v", actions)
				}
			}
			if found != scenario.allowed {
				t.Fatalf("live permission boundary failed: created=%v", found)
			}
		})
	}
}
