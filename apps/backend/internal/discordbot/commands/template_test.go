package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
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
			assertPublicCommandAcknowledgement(t, result)
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			commandFeedback(t, responder, scenario.allowed)
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
		assertPublicCommandAcknowledgement(t, result)
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		commandFeedback(t, responder, true)
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

// runTemplateManagement exercises the registered command handler with native
// option shapes and an independently refreshed Discord permission lookup.
func runTemplateManagement(t *testing.T, services *quack.Services, id, operation string, fields ...*discordgo.ApplicationCommandInteractionDataOption) *fakeResponder {
	t.Helper()
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
	options := append([]*discordgo.ApplicationCommandInteractionDataOption{{Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: id}}, fields...)
	interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "template", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: operation, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options}}}
	result := handleTemplateCommand(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	assertPublicCommandAcknowledgement(t, result)
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	return responder
}

// TestTemplateManagementLifecycle follows edits, level removal, archive and
// restore through persistence while keeping the same template identity.
func TestTemplateManagementLifecycle(t *testing.T) {
	_, services, id := newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionManageGuild))
	ctx := context.Background()
	guild := caseCommandGuildContext(t, services)
	read := func() *quack.TemplateResponse {
		t.Helper()
		template, err := services.Templates.Get(ctx, guild, id)
		if err != nil {
			t.Fatal(err)
		}
		return template
	}
	runTemplateManagement(t, services, id, "edit",
		&discordgo.ApplicationCommandInteractionDataOption{Name: "name", Type: discordgo.ApplicationCommandOptionString, Value: "Chat rules"},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "reason", Type: discordgo.ApplicationCommandOptionString, Value: "Keep chat readable."},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "appeals", Type: discordgo.ApplicationCommandOptionBoolean, Value: false},
	)
	if template := read(); template.Name != "Chat rules" || template.ReasonTemplate != "Keep chat readable." || template.Appealable || len(template.Levels) != 1 {
		t.Fatalf("edit lost policy fields: %+v", template)
	}
	runTemplateManagement(t, services, id, "level",
		&discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(3)},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "outcome", Type: discordgo.ApplicationCommandOptionString, Value: "ban"},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "notify", Type: discordgo.ApplicationCommandOptionBoolean, Value: false},
	)
	template := read()
	if len(template.Levels) != 2 {
		t.Fatalf("missing escalation: %+v", template.Levels)
	}
	for _, level := range template.Levels {
		if !level.IsDefault && level.NotifyUser {
			t.Fatal("DM choice ignored")
		}
	}
	view := runTemplateManagement(t, services, id, "view")
	commandFeedback(t, view, true)
	if view.edit.Content == nil || !strings.Contains(*view.edit.Content, "**3+ times:** Ban") || !strings.Contains(*view.edit.Content, "DM off") || !strings.Contains(*view.edit.Content, "Keep chat readable.") {
		t.Fatalf("incomplete policy view: %+v", view.edit.Content)
	}
	runTemplateManagement(t, services, id, "remove-level", &discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(1)})
	if len(read().Levels) != 2 {
		t.Fatal("default removal modified policy")
	}
	runTemplateManagement(t, services, id, "remove-level", &discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(3)})
	if levels := read().Levels; len(levels) != 1 || !levels[0].IsDefault {
		t.Fatalf("removal lost default: %+v", levels)
	}
	runTemplateManagement(t, services, id, "archive")
	if read().ArchivedAt == nil {
		t.Fatal("not archived")
	}
	active, err := services.Templates.ListActive(ctx, guild)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range active {
		if entry.ID == id {
			t.Fatal("archived rule available for cases")
		}
	}
	view = runTemplateManagement(t, services, id, "view")
	if view.edit.Content == nil || !strings.Contains(*view.edit.Content, "Archived") {
		t.Fatal("archived policy cannot be inspected")
	}
	runTemplateManagement(t, services, id, "restore")
	if read().ArchivedAt != nil {
		t.Fatal("not restored")
	}
}

// TestTemplateManagementRejectsRevokedManager checks every new control against
// live authority rather than trusting the interaction's cached manager bit.
func TestTemplateManagementRejectsRevokedManager(t *testing.T) {
	_, services, id := newCaseCommandHarnessWithLivePermissions(t, 0)
	for _, operation := range []string{"view", "edit", "remove-level", "archive", "restore"} {
		responder := runTemplateManagement(t, services, id, operation)
		if !strings.Contains(commandFeedback(t, responder, false), "Manage Server") {
			t.Fatalf("%s allowed revoked manager", operation)
		}
	}
}

// TestNativeTemplateThresholdMatchesCreatedCase counts real cases after native
// configuration so UI conversion errors cannot trigger punishment a case early.
func TestNativeTemplateThresholdMatchesCreatedCase(t *testing.T) {
	_, services, id := newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionManageGuild))
	runTemplateManagement(t, services, id, "level",
		&discordgo.ApplicationCommandInteractionDataOption{Name: "case", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(3)},
		&discordgo.ApplicationCommandInteractionDataOption{Name: "outcome", Type: discordgo.ApplicationCommandOptionString, Value: "ban"},
	)
	for number := 1; number <= 3; number++ {
		created, err := services.Cases.Create(context.Background(), caseCommandGuildContext(t, services), quack.CaseInput{TemplateID: id, TargetDiscordUserID: "target"})
		if err != nil {
			t.Fatal(err)
		}
		if created.SelectedLevel == nil || created.SelectedLevel.MatchedCaseCount != int64(number) {
			t.Fatalf("case %d count: %+v", number, created.SelectedLevel)
		}
		if number < 3 && (!created.SelectedLevel.IsDefault || len(created.Actions) != 0) {
			t.Fatalf("case %d punished early: %+v", number, created)
		}
		if number == 3 && (created.SelectedLevel.IsDefault || len(created.Actions) != 1) {
			t.Fatalf("third case did not escalate: %+v", created)
		}
	}
}

// TestNativeTemplateDecayCanBeEnabledAndDisabled exercises the native choice
// through guarded persistence and confirms the view explains retained history.
func TestNativeTemplateDecayCanBeEnabledAndDisabled(t *testing.T) {
	_, services, id := newCaseCommandHarnessWithLivePermissions(t, uint64(discordgo.PermissionManageGuild))
	for _, days := range []int{30, 0} {
		runTemplateManagement(t, services, id, "edit", &discordgo.ApplicationCommandInteractionDataOption{Name: "decay-days", Type: discordgo.ApplicationCommandOptionInteger, Value: float64(days)})
		template, err := services.Templates.Get(context.Background(), caseCommandGuildContext(t, services), id)
		if err != nil || template.CaseDecayDays != days {
			t.Fatalf("decay=%+v err=%v", template, err)
		}
		view := runTemplateManagement(t, services, id, "view")
		expected := "Counting all cases for this rule."
		if days != 0 {
			expected = "Counting cases from the last **30 days**."
		}
		if view.edit.Content == nil || !strings.Contains(*view.edit.Content, expected) {
			t.Fatalf("missing decay explanation: %+v", view.edit.Content)
		}
	}
}
