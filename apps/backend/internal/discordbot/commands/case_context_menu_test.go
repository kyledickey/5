package commands

import (
	"context"
	"fmt"
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

func TestTemplatePickerReachesEveryTemplateAndPreservesTarget(t *testing.T) {
	templates := make([]quack.TemplateResponse, 51)
	for i := range templates {
		templates[i] = quack.TemplateResponse{ID: fmt.Sprintf("template-%d", i), Name: fmt.Sprintf("Rule %d", i)}
	}
	for _, target := range []struct{ kind, payload, action string }{
		{"u", "489264179472236557", "user_template"},
		{"m", "489264179472236557|1005778938108325970|1005778938108325971", "message_template"},
	} {
		t.Run(target.kind, func(t *testing.T) {
			seen := map[string]bool{}
			for page := 0; page < 3; page++ {
				message := caseTemplatePicker(templates, target.kind, target.payload, page)
				menu := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
				id, err := ui.DecodeCustomID(menu.CustomID)
				if err != nil || id.Payload != target.payload || id.Action != target.action {
					t.Fatalf("selection lost target: %+v, %v", id, err)
				}
				for _, option := range menu.Options {
					if seen[option.Value] {
						t.Fatalf("duplicate template %s", option.Value)
					}
					seen[option.Value] = true
				}
				buttons := message.Components[1].(discordgo.ActionsRow).Components
				for i, component := range buttons {
					button := component.(discordgo.Button)
					next := max(0, page-1)
					if i == 1 {
						next = page + 1
					}
					id, err := ui.DecodeCustomID(button.CustomID)
					if err != nil || id.Payload != fmt.Sprintf("%s|%d|%s", target.kind, next, target.payload) || id.Action != "template_page" {
						t.Fatalf("navigation lost target: %+v, %v", id, err)
					}
					if button.Disabled != ((i == 0 && page == 0) || (i == 1 && page == 2)) {
						t.Fatal("incorrect boundary button")
					}
				}
			}
			if len(seen) != len(templates) {
				t.Fatalf("reached %d of %d templates", len(seen), len(templates))
			}
		})
	}
}

func TestTemplatePickerClampsAfterTemplatesAreRemoved(t *testing.T) {
	message := caseTemplatePicker([]quack.TemplateResponse{{ID: "remaining", Name: "Remaining"}}, "u", "target", 99)
	menu := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if len(message.Components) != 1 || len(menu.Options) != 1 || menu.Options[0].Value != "remaining" {
		t.Fatal("stale page did not reach remaining template")
	}
	if empty := caseTemplatePicker(nil, "u", "target", 99); len(empty.Components) != 0 {
		t.Fatal("empty list rendered a select menu")
	}
}

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
			if result.Response == nil || result.Response.Type != discordgo.InteractionResponseDeferredMessageUpdate || result.Task == nil {
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

// TestSingleTemplateContextPublishesPublicCase removes the private acknowledgement
// after one standalone public notice, without a redundant success confirmation.
func TestSingleTemplateContextPublishesPublicCase(t *testing.T) {
	_, services, _ := newCaseCommandHarness(t)
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionModerateMembers))
	interaction.Data = discordgo.ApplicationCommandInteractionData{TargetID: "target", Resolved: &discordgo.ApplicationCommandInteractionDataResolved{Users: map[string]*discordgo.User{"target": {ID: "target"}}}}
	result := HandleUserCaseInteraction(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.channelPublishes != 1 || responder.webhookFollowups != 0 || responder.editCount != 0 || responder.followup.Ephemeral || !strings.Contains(responder.followup.Content, "<@target>") || !responder.deleted {
		t.Fatalf("public result failed: %+v", responder)
	}
}
