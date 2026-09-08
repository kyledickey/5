package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestTemplateLevelUsesHumanCaseNumberAndPreservesOtherLevels follows native
// edits through persistence, retaining existing rule text and lower outcomes.
func TestTemplateLevelUsesHumanCaseNumberAndPreservesOtherLevels(t *testing.T) {
	_, services, templateID := newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionManageGuild))
	for _, outcome := range []string{"ban", "kick"} {
		interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
		interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "template", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "level", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: templateID}, {Name: "case", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(3)}, {Name: "outcome", Type: discordgo.ApplicationCommandOptionString, Value: outcome},
		}}}}
		result := handleTemplateCommand(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
		if result.Task == nil {
			t.Fatal("edit did not defer")
		}
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		template, err := services.Templates.Get(context.Background(), caseCommandGuildContext(t, services), templateID)
		if err != nil {
			t.Fatal(err)
		}
		if len(template.Levels) != 2 || template.Name != "Spam" || template.ReasonTemplate != "Spam" {
			t.Fatalf("edit failed or lost rule: %+v feedback=%+v", template, responder.edit.Content)
		}
		found := false
		for _, level := range template.Levels {
			if level.IsDefault {
				if len(level.Actions) != 0 || !level.NotifyUser {
					t.Fatal("default outcome changed")
				}
				continue
			}
			found = true
			want := model.ActionBanUser
			if outcome == "kick" {
				want = model.ActionKickUser
			}
			if level.TriggerCaseCount != 3 || len(level.Actions) != 1 || level.Actions[0].ActionType != want {
				t.Fatalf("third-case threshold incorrect: %+v", level)
			}
		}
		if !found {
			t.Fatal("missing escalation")
		}
	}
}
