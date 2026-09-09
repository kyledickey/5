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
