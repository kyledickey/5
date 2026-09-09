package commands

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// renameCommandClient verifies replacements exist before legacy entries are
// deleted and can reject registration to exercise the migration safety gate.
type renameCommandClient struct {
	fakeCommandClient
	failCreate bool
}

// CreateCommand optionally fails without adding a replacement to remote state.
func (c *renameCommandClient) CreateCommand(ctx context.Context, app, guild string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	if c.failCreate {
		return nil, errors.New("registration failed")
	}
	return c.fakeCommandClient.CreateCommand(ctx, app, guild, command)
}

// DeleteCommand refuses a legacy deletion until its replacement was registered.
func (c *renameCommandClient) DeleteCommand(ctx context.Context, app, guild, id string) error {
	replacement := "Add case"
	if id == "old-user" {
		replacement = "Add case for member"
	}
	if !slices.ContainsFunc(c.commands, func(command *discordgo.ApplicationCommand) bool { return command.Name == replacement }) {
		return errors.New("retired before replacement existed")
	}
	return c.fakeCommandClient.DeleteCommand(ctx, app, guild, id)
}

// TestContextRenameRetirementWithoutPruning keeps unrelated remote commands and
// wrong-type names intact while retiring both exact historical context entries.
func TestContextRenameRetirementWithoutPruning(t *testing.T) {
	for _, prune := range []bool{false, true} {
		client := &renameCommandClient{fakeCommandClient: fakeCommandClient{commands: []*discordgo.ApplicationCommand{
			{ID: "old-message", Name: "Create moderation case", Type: discordgo.MessageApplicationCommand},
			{ID: "old-user", Name: "Create case for member", Type: discordgo.UserApplicationCommand},
		}}}
		specs := []CommandSpec{{Definition: &discordgo.ApplicationCommand{Name: "Add case", Type: discordgo.MessageApplicationCommand}}, {Definition: &discordgo.ApplicationCommand{Name: "Add case for member", Type: discordgo.UserApplicationCommand}}}
		if !prune {
			client.commands = append(client.commands, &discordgo.ApplicationCommand{ID: "unrelated", Name: "Unrelated", Type: discordgo.MessageApplicationCommand}, &discordgo.ApplicationCommand{ID: "wrong-type", Name: "Create moderation case", Type: discordgo.UserApplicationCommand})
		}
		syncer := CommandSyncer{Client: client, AppID: "rename-test", PruneEnabled: prune}
		if err := syncer.Sync(context.Background(), specs); err != nil {
			t.Fatal(err)
		}
		if len(client.created) != 2 || len(client.deleted) != 2 || !slices.Contains(client.deleted, "old-message") || !slices.Contains(client.deleted, "old-user") {
			t.Fatalf("wrong migration: created=%+v deleted=%v", client.created, client.deleted)
		}
	}
}

// TestContextRenameRequiresSuccessfulMatchingReplacement preserves old controls
// when creation fails or the replacement has the wrong context-command type.
func TestContextRenameRequiresSuccessfulMatchingReplacement(t *testing.T) {
	for _, mode := range []string{"failure", "wrong-type", "missing"} {
		client := &renameCommandClient{fakeCommandClient: fakeCommandClient{commands: []*discordgo.ApplicationCommand{{ID: "old-message", Name: "Create moderation case", Type: discordgo.MessageApplicationCommand}}}, failCreate: mode == "failure"}
		specs := []CommandSpec{{Definition: &discordgo.ApplicationCommand{Name: "Add case", Type: discordgo.MessageApplicationCommand}}}
		if mode == "wrong-type" {
			specs[0].Definition.Type = discordgo.UserApplicationCommand
		}
		if mode == "missing" {
			specs = nil
		}
		err := (CommandSyncer{Client: client, AppID: "rename-" + mode}).Sync(context.Background(), specs)
		if (err != nil) != (mode == "failure") {
			t.Fatalf("%s: %v", mode, err)
		}
		if len(client.deleted) != 0 {
			t.Fatalf("%s retired old command: %v", mode, client.deleted)
		}
	}
}
