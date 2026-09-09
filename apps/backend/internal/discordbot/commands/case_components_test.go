package commands

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func TestCaseComponentRegistrarInstallsRealRecoveryAndPaginationHandlers(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	if err := RegisterCaseComponents(registry); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"list_prev", "list_next", "user_prev", "user_next", "failures_prev", "failures_next", "retry", "dismiss", "void", "reverse", "message_template", "user_template", "template_page"} {
		if _, ok, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("component %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
	for _, action := range []string{"void_submit", "reverse_submit", "edit_context_submit"} {
		if _, ok, err := registry.LookupModal(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: "payload"})); err != nil || !ok {
			t.Fatalf("modal %s not registered: ok=%v err=%v", action, ok, err)
		}
	}
}

func TestCaseAddActsImmediatelyWithOptionalContext(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	template := createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "abuse", Name: "Abuse", ReasonTemplate: "Abusive behavior", ContextFields: []quack.TemplateContextFieldInput{{Key: "details", Label: "What happened?", FieldType: model.ContextFieldLongText, Position: 1, Required: true}}, Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})

	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction(template.ID, "target-2", uint64(discordgo.PermissionModerateMembers))})
	if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Task == nil {
		t.Fatalf("case creation must not wait for context: %+v", result)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.deleted || responder.channelPublishes != 0 || responder.webhookFollowups != 0 || responder.followup.Content != "" || responder.edit.Content == nil || responder.edit.Embeds == nil || len(*responder.edit.Embeds) != 0 || responder.editCount != 1 {
		t.Fatalf("expected one in-place public result, got %+v", responder)
	}
	for _, want := range []string{"Case #1", "<@target-2>", "Abuse"} {
		if !strings.Contains(*responder.edit.Content, want) {
			t.Fatalf("missing %q: %+v", want, responder.followup)
		}
	}
	for _, hidden := range []string{"Matching Cases", "Visible context", "Evidence", "Repeated abusive replies"} {
		if strings.Contains(*responder.edit.Content, hidden) {
			t.Fatalf("public result leaked %s", hidden)
		}
	}

}

func TestMessageContextActionOffersActiveTemplateSelection(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guildContext := caseCommandGuildContext(t, services)
	createCaseCommandTemplate(t, services, guildContext, quack.TemplateInput{Slug: "other", Name: "Other", ReasonTemplate: "Other reason", Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}}})
	interaction := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{ID: "message-command", Type: discordgo.InteractionApplicationCommand, GuildID: "guild-1", ChannelID: "channel-1", Member: &discordgo.Member{User: &discordgo.User{ID: "mod-1", Username: "mod"}, Permissions: int64(discordgo.PermissionModerateMembers)}, Data: discordgo.ApplicationCommandInteractionData{Name: messageCaseCommandName, TargetID: "message-1", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Messages: map[string]*discordgo.Message{"message-1": {ID: "message-1", ChannelID: "channel-1", Author: &discordgo.User{ID: "target-1"}}}}}}}
	result := HandleMessageCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Response == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || result.Task == nil {
		t.Fatal("expected private acknowledgement")
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
	if len(menu.Options) != 2 || !strings.Contains(menu.CustomID, "case:message_template:v1:") {
		t.Fatalf("unexpected active-template selector: %+v", menu)
	}
}

// TestCaseCreationDoesNotDependOnContextFieldCount protects immediate creation
// for imported policies containing multiple formerly required fields.
func TestCaseCreationDoesNotDependOnContextFieldCount(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	guild := caseCommandGuildContext(t, services)
	fields := []quack.TemplateContextFieldInput{}
	for i := 1; i <= 6; i++ {
		fields = append(fields, quack.TemplateContextFieldInput{Key: fmt.Sprintf("field_%d", i), Label: fmt.Sprintf("Field %d", i), FieldType: model.ContextFieldShortText, Position: i, Required: true})
	}
	template := createCaseCommandTemplate(t, services, guild, quack.TemplateInput{Slug: "many-fields", Name: "Many fields", ReasonTemplate: "Rule", ContextFields: fields, Levels: []quack.TemplateLevelInput{{Name: "Warning", Position: 1, IsDefault: true}}})
	result := HandleCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: caseAddInteraction(template.ID, "target-many", uint64(discordgo.PermissionModerateMembers))})
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("unexpected form: %+v", result)
	}
	if err := result.Task(context.Background(), &fakeResponder{}); err != nil {
		t.Fatal(err)
	}
}

func TestCaseCommandHasNoLegacyDirectPunishmentCommands(t *testing.T) {
	definition := CaseCommandDefinition()
	for _, option := range definition.Options {
		switch option.Name {
		case "warn", "timeout", "kick", "ban":
			t.Fatalf("legacy direct punishment command remains: %s", option.Name)
		}
	}
}
