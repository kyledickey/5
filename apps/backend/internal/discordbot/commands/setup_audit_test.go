package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack/model"
)

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
// registered slash command through persistence, rejection and private feedback.
func TestAuditSetupUsesLiveManagerAuthorityAndValidatedDestination(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		permissions int64
		invalid     bool
		want        string
	}{
		{"manager", discordgo.PermissionManageGuild, false, "Moderation history"},
		{"revoked", discordgo.PermissionModerateMembers, false, "Manage Server"},
		{"public channel", discordgo.PermissionManageGuild, true, "private text channel"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			repository, services, _ := newCaseCommandHarnessWithLivePermissions(t, uint64(scenario.permissions))
			bootstrap, err := repository.BootstrapGuild(context.Background(), model.BootstrapGuildParams{DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1"})
			if err != nil {
				t.Fatal(err)
			}
			validator := &auditSetupValidator{}
			if scenario.invalid {
				validator.err = errors.New("public channel")
			}
			services.Settings.WithStaffChannelValidator(validator)
			interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
			interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "setup", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "audit", Type: discordgo.ApplicationCommandOptionSubCommand, Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "123456789012345678"}}}}}
			spec := SetupCommandSpec()
			found := false
			for _, option := range spec.Definition.Options {
				if option.Name == "audit" {
					found = len(option.Options) == 1 && option.Options[0].Required
				}
			}
			if !found {
				t.Fatal("audit command missing")
			}
			result := spec.Handler(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
			if result.Task == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || validator.calls != 0 {
				t.Fatal("setup did not acknowledge before lookups")
			}
			responder := &fakeResponder{}
			if err := result.Task(context.Background(), responder); err != nil {
				t.Fatal(err)
			}
			if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, scenario.want) {
				t.Fatalf("unexpected feedback: %+v", responder.edit.Content)
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
