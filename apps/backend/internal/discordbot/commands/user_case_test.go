package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestUserContextCreatesCaseForSelectedMember(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guild, quack.TemplateInput{Slug: "profile-rule", Name: "Profile rule", ReasonTemplate: "Profile violates the rules", Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	interaction := caseAddInteraction("", "target-2", uint64(discordgo.PermissionModerateMembers))
	interaction.Data = discordgo.ApplicationCommandInteractionData{Name: UserCaseCommandSpec().Definition.Name, CommandType: discordgo.UserApplicationCommand, TargetID: "target-2", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{"target-2": {ID: "target-2"}}}}
	result := HandleUserCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Task == nil {
		t.Fatal("picker did not defer")
	}
	picker := &fakeResponder{}
	if err := result.Task(context.Background(), picker); err != nil {
		t.Fatal(err)
	}
	if picker.edit.Components == nil || len(*picker.edit.Components) != 1 {
		t.Fatal("missing template picker")
	}
	row := (*picker.edit.Components)[0].(discordgo.ActionsRow)
	menu := row.Components[0].(discordgo.SelectMenu)
	interaction.Type = discordgo.InteractionMessageComponent
	interaction.Data = discordgo.MessageComponentInteractionData{CustomID: menu.CustomID, ComponentType: discordgo.SelectMenuComponent, Values: []string{template.ID}}
	result = handleUserTemplateComponent(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Task == nil {
		t.Fatalf("selected policy did not create case: %+v", result.Response)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.followup.Ephemeral || !responder.deleted || !strings.Contains(responder.followup.Content, "<@target-2>") || !strings.Contains(responder.followup.Content, "Profile rule") {
		t.Fatalf("incorrect target/result: %+v", responder.edit)
	}
	if UserCaseCommandSpec().Definition.Type != discordgo.UserApplicationCommand {
		t.Fatal("not a user context command")
	}
}
