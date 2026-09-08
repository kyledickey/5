package commands

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// runTemplateManagement exercises the registered command handler with native
// option shapes and an independently refreshed Discord permission lookup.
func runTemplateManagement(t *testing.T, services *quack.Services, id, operation string, fields ...*discordgo.ApplicationCommandInteractionDataOption) *fakeResponder {
	t.Helper()
	interaction := caseAddInteraction("", "target", uint64(discordgo.PermissionManageGuild))
	options := append([]*discordgo.ApplicationCommandInteractionDataOption{{Name: "template", Type: discordgo.ApplicationCommandOptionString, Value: id}}, fields...)
	interaction.Data = discordgo.ApplicationCommandInteractionData{Name: "template", Options: []*discordgo.ApplicationCommandInteractionDataOption{{Name: operation, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options}}}
	result := handleTemplateCommand(ui.Context{Context: context.Background(), Services: services, Interaction: interaction})
	if result.Task == nil {
		t.Fatal("command did not acknowledge before network lookups")
	}
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
	if view.edit.Content == nil || !strings.Contains(*view.edit.Content, "From case **3**") || !strings.Contains(*view.edit.Content, "DM off") || !strings.Contains(*view.edit.Content, "Keep chat readable.") {
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
		if responder.edit.Content == nil || !strings.Contains(*responder.edit.Content, "Manage Server") {
			t.Fatalf("%s allowed revoked manager", operation)
		}
	}
}
