package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestSetupAppealsExposesReasonRequirement verifies the optional boolean keeps
// the existing setup flow while making moderator reasons configurable.
func TestSetupAppealsExposesReasonRequirement(t *testing.T) {
	spec := SetupCommandSpec()
	var appeals *discordgo.ApplicationCommandOption
	for _, option := range spec.Definition.Options {
		if option.Name == "appeals" {
			appeals = option
			break
		}
	}
	if appeals == nil {
		t.Fatal("appeal setup subcommand missing")
	}
	for _, option := range appeals.Options {
		if option.Name == "require-reason" {
			if option.Type != discordgo.ApplicationCommandOptionBoolean || option.Required || !strings.Contains(option.Description, "member receives") {
				t.Fatalf("invalid require-reason option: %+v", option)
			}
			return
		}
	}
	t.Fatal("appeal reason requirement option missing")
}

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

// auditSetupValidator tracks the shared channel boundary used by setup.
type auditSetupValidator struct {
	err   error
	calls int
}

func (v *auditSetupValidator) ValidateStaffChannel(context.Context, string, string) error {
	v.calls++
	return v.err
}

// TestAuditSetupUsesLiveManagerAuthorityAndValidatedDestination follows the
// registered slash command through persistence, public success and private errors.
func TestAuditSetupUsesLiveManagerAuthorityAndValidatedDestination(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		permissions int64
		invalid     bool
		want        string
	}{
		{"manager", discordgo.PermissionManageGuild, false, "Moderation history"},
		{"revoked", discordgo.PermissionModerateMembers, false, "Manage Server"},
		{"inaccessible channel", discordgo.PermissionManageGuild, true, "text channel"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repository, services, _ := newCaseCommandHarnessWithLivePermissions(t, uint64(scenario.permissions))
			bootstrap, err := repository.BootstrapGuild(context.Background(), model.BootstrapGuildParams{DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1"})
			if err != nil {
				t.Fatal(err)
			}
			validator := &auditSetupValidator{}
			if scenario.invalid {
				validator.err = errors.New("inaccessible channel")
			}
			services.Settings.WithStaffChannelValidator(validator)
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
			interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "setup", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "audit", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "123456789012345678"}}}}}
			spec := SetupCommandSpec()
			found := false
			for _, option := range spec.Definition.Options {
				if option.Name == "audit" {
					found = len(option.Options) == 1 && !option.Options[0].Required
				}
			}
			if !found {
				t.Fatal("audit command missing")
			}
			result := spec.Handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			assertPublicCommandAcknowledgement(t, result)
			if validator.calls != 0 {
				t.Fatal("setup did not acknowledge before lookups")
			}
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			feedback := commandFeedback(t, responder, scenario.name == "manager")
			if !strings.Contains(feedback, scenario.want) {
				t.Fatalf("unexpected feedback: %s", feedback)
			}
			settings, err := repository.GetGuildSettings(context.Background(), bootstrap.Guild.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if scenario.name == "manager" {
				want = "123456789012345678"
			}
			if settings.AuditMirrorChannelDiscordID != want {
				t.Fatalf("unexpected persisted channel: %s", settings.AuditMirrorChannelDiscordID)
			}
		})
	}
}

// setupToggleValidator substitutes live module checks while tracking whether
// the command routes enablement through the shared retained-config boundary.
type setupToggleValidator struct {
	calls []string
	err   error
}

// ValidateGuildModuleEnablement returns the exact retained configuration used
// by the fixture so the settings transaction's configuration fence stays active.
func (v *setupToggleValidator) ValidateGuildModuleEnablement(_ context.Context, _ *quack.GuildStaffContext, id string) (string, error) {
	v.calls = append(v.calls, id)
	return `{"retained":true}`, v.err
}

// TestSetupToggleUsesCanonicalSettings covers all three booleans, live authority,
// retained configuration, validation rejection and untouched sibling modules.
func TestSetupToggleUsesCanonicalSettings(t *testing.T) {
	for command, moduleID := range map[string]modules.ID{"tickets": modules.Tickets, "honeypot": modules.Honeypots, "logging": modules.GeneralLogging} {
		for _, enabled := range []bool{false, true} {
			for _, scenario := range []string{"manager", "revoked", "validation failure"} {
				if scenario == "validation failure" && !enabled {
					continue
				}
				t.Run(fmt.Sprintf("%s/%t/%s", command, enabled, scenario), func(t *testing.T) {
					permissions := uint64(discordgo.PermissionManageGuild)
					if scenario == "revoked" {
						permissions = uint64(discordgo.PermissionModerateMembers)
					}
					repository, services, _ := newCaseCommandHarnessWithLivePermissions(t, permissions)
					bootstrap, err := repository.BootstrapGuild(context.Background(), model.BootstrapGuildParams{DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1"})
					if err != nil {
						t.Fatal(err)
					}
					store := modules.NewSQLSettingsStore(repository.DB())
					ids := []modules.ID{modules.Tickets, modules.Honeypots, modules.GeneralLogging}
					for _, id := range ids {
						if _, err := store.PutModuleConfiguration(context.Background(), modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id, Enabled: !enabled, ConfigJSON: `{"retained":true}`}); err != nil {
							t.Fatal(err)
						}
					}
					validator := &setupToggleValidator{}
					if scenario == "validation failure" {
						validator.err = errors.New("private Discord/SQL failure")
					}
					services.Settings.WithModuleEnablementValidator(validator)
					interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
					interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "setup", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: command, Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: enabled}}}}}
					// No Session is supplied: toggle execution must not create/edit channels.
					result := SetupCommandSpec().Handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
					assertPublicCommandAcknowledgement(t, result)
					if len(validator.calls) != 0 {
						t.Fatal("toggle did not defer before validation")
					}
					responder := &fakeResponder{}
					if err := result.Task(context.Background(), responder); err != nil {
						t.Fatal(err)
					}
					text := commandFeedback(t, responder, scenario == "manager")
					if strings.Contains(text, "private") {
						t.Fatalf("internal error leaked: %s", text)
					}
					want := "turned on"
					if !enabled {
						want = "enabled:true"
					}
					if scenario == "revoked" {
						want = "Manage Server"
					}
					if scenario == "validation failure" {
						want = "/setup " + command
					}
					if !strings.Contains(text, want) {
						t.Fatalf("unexpected toggle feedback: %s", text)
					}
					expectedCalls := 0
					if enabled && scenario != "revoked" {
						expectedCalls = 1
					}
					if len(validator.calls) != expectedCalls || (expectedCalls == 1 && validator.calls[0] != string(moduleID)) {
						t.Fatalf("wrong validation route: %v", validator.calls)
					}
					for _, id := range ids {
						saved, err := store.GetModuleConfiguration(context.Background(), bootstrap.Guild.ID, id)
						wantEnabled := !enabled
						if id == moduleID && scenario == "manager" {
							wantEnabled = enabled
						}
						if err != nil || saved == nil || saved.Enabled != wantEnabled || saved.ConfigJSON != `{"retained":true}` {
							t.Fatalf("module configuration changed incorrectly: module=%s saved=%+v err=%v", id, saved, err)
						}
					}
				})
			}
		}
	}
}

// TestSetupToggleOptionsAndDefaultDispatch preserves both default creation and
// supplied-channel setup, while refusing ambiguous combined toggle requests.
func TestSetupToggleOptionsAndDefaultDispatch(t *testing.T) {
	for _, module := range []string{"tickets", "honeypot", "logging"} {
		t.Run(module, func(t *testing.T) {
			calls := 0
			handler := func(ui.Context) ui.HandlerResult { calls++; return ui.Immediate(ui.Error("setup handler reached")) }
			spec := SetupCommandSpec(SetupHandlers{Tickets: handler, Honeypot: handler, Logging: handler})
			for _, definition := range spec.Definition.Options {
				if definition.Name != module {
					continue
				}
				found := false
				for _, option := range definition.Options {
					if option.Name == "enabled" {
						found = !option.Required && option.Type == discordgo.ApplicationCommandOptionBoolean
					}
				}
				if !found {
					t.Fatal("optional boolean enabled missing")
				}
			}
			extraName := "channel"
			if module == "tickets" {
				extraName = "entry"
			}
			extra := &discordgo.ApplicationCommandInteractionDataOption{Name: extraName, Type: discordgo.ApplicationCommandOptionChannel, Value: "existing-channel"}
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
			invoke := func(options ...*discordgo.ApplicationCommandInteractionDataOption) ui.HandlerResult {
				interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "setup", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: module, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options}}}
				return spec.Handler(ui.Context{Interaction: interaction})
			}
			invoke()
			invoke(extra)
			if calls != 2 {
				t.Fatal("default or supplied-channel setup dispatch changed")
			}
			conflicts := []*discordgo.ApplicationCommandInteractionDataOption{extra}
			if module == "tickets" {
				conflicts = append(conflicts, &discordgo.ApplicationCommandInteractionDataOption{Name: "queue", Type: discordgo.ApplicationCommandOptionChannel, Value: "queue"})
			}
			if module == "honeypot" {
				conflicts = append(conflicts, &discordgo.ApplicationCommandInteractionDataOption{Name: "warning", Type: discordgo.ApplicationCommandOptionString, Value: "warning"})
			}
			for _, conflict := range conflicts {
				result := invoke(&discordgo.ApplicationCommandInteractionDataOption{Name: "enabled", Type: discordgo.ApplicationCommandOptionBoolean, Value: false}, conflict)
				if calls != 2 || result.Task != nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(result.Response.Data.Content, "on its own") {
					t.Fatal("mixed options were not rejected privately")
				}
			}
		})
	}
}
