package commands

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/redis/go-redis/v9"
)

type fakeCommandClient struct {
	commands []*discordgo.ApplicationCommand
	created  []*discordgo.ApplicationCommand
	edited   []*discordgo.ApplicationCommand
	deleted  []string
}

func (f *fakeCommandClient) ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	return f.commands, nil
}

func (f *fakeCommandClient) CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	created := cloneCommand(command)
	created.ID = "created-" + command.Name
	f.created = append(f.created, created)
	f.commands = append(f.commands, created)
	return created, nil
}

func (f *fakeCommandClient) EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	updated := cloneCommand(command)
	updated.ID = commandID
	f.edited = append(f.edited, updated)
	return updated, nil
}

func (f *fakeCommandClient) DeleteCommand(ctx context.Context, appID, guildID, commandID string) error {
	f.deleted = append(f.deleted, commandID)
	return nil
}

type memoryCommandCache struct {
	entries map[string]commandCacheEntry
}

func newMemoryCommandCache() *memoryCommandCache {
	return &memoryCommandCache{entries: map[string]commandCacheEntry{}}
}

func (c *memoryCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	entry, ok := c.entries[scope+"|"+commandName]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (c *memoryCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	c.entries[scope+"|"+commandName] = entry
	return nil
}

func TestCommandSyncerCreatesMissingCommand(t *testing.T) {
	client := &fakeCommandClient{}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 1 || len(client.edited) != 0 {
		t.Fatalf("expected one create and no edits, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if _, ok := cache.entries["global|case"]; !ok {
		t.Fatalf("expected command hash cache to be written")
	}
}

func TestCommandSyncerSkipsUnchangedCachedCommand(t *testing.T) {
	remote := cloneCommand(CaseCommandDefinition())
	remote.ID = "remote-case"
	localHash, err := commandHash(CaseCommandDefinition())
	if err != nil {
		t.Fatalf("hash command: %v", err)
	}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	cache.entries["global|case"] = commandCacheEntry{DiscordCommandID: remote.ID, Hash: localHash}
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 0 {
		t.Fatalf("expected no create/edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if len(client.deleted) != 0 {
		t.Fatalf("expected no deletes, got %d", len(client.deleted))
	}
}

func TestCommandSyncerEditsChangedCommand(t *testing.T) {
	remote := cloneCommand(CaseCommandDefinition())
	remote.ID = "remote-case"
	local := CaseCommandDefinition()
	local.Description = "Updated description"

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: local, Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 1 {
		t.Fatalf("expected one edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if client.edited[0].ID != remote.ID {
		t.Fatalf("expected existing command id to be edited, got %q", client.edited[0].ID)
	}
}

func TestCommandSyncerIgnoresRemoteOnlyCommandsWhenPruneDisabled(t *testing.T) {
	remote := cloneCommand(CaseCommandDefinition())
	remote.ID = "remote-case"
	extra := &discordgo.ApplicationCommand{ID: "remote-extra", Name: "extra", Description: "Extra"}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote, extra}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.deleted) != 0 {
		t.Fatalf("expected no deletes with pruning disabled, got %+v", client.deleted)
	}
}

func TestCommandSyncerDeletesRemoteOnlyCommandsWhenPruneEnabled(t *testing.T) {
	remote := cloneCommand(CaseCommandDefinition())
	remote.ID = "remote-case"
	extra := &discordgo.ApplicationCommand{ID: "remote-extra", Name: "extra", Description: "Extra"}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote, extra}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)
	syncer.PruneEnabled = true

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.deleted) != 1 || client.deleted[0] != "remote-extra" {
		t.Fatalf("expected remote extra command to be deleted, got %+v", client.deleted)
	}
}

func TestCommandSyncerRefreshesMissingCacheForIdenticalRemote(t *testing.T) {
	remote := cloneCommand(CaseCommandDefinition())
	remote.ID = "remote-case"

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 0 {
		t.Fatalf("expected no create/edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	entry, ok := cache.entries["global|case"]
	if !ok || entry.DiscordCommandID != remote.ID {
		t.Fatalf("expected cache refresh for remote command, got %+v", entry)
	}
}

func testCommandSyncer(client *fakeCommandClient, cache commandHashCache) CommandSyncer {
	return CommandSyncer{
		Client: client,
		Cache:  cache,
		AppID:  "app-1",
	}
}

func cloneCommand(command *discordgo.ApplicationCommand) *discordgo.ApplicationCommand {
	if command == nil {
		return nil
	}
	clone := *command
	if command.Options != nil {
		clone.Options = cloneOptions(command.Options)
	}
	return &clone
}

func cloneOptions(options []*discordgo.ApplicationCommandOption) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, 0, len(options))
	for _, option := range options {
		if option == nil {
			continue
		}
		clone := *option
		if option.Options != nil {
			clone.Options = cloneOptions(option.Options)
		}
		out = append(out, &clone)
	}
	return out
}

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

// commandRateLimitTransport returns one short Discord cooldown, then the
// requested command. It verifies transport retry behavior without network I/O.
type commandRateLimitTransport struct{ calls int }

// RoundTrip simulates a definite 429 rejection, which is safe to retry.
func (r *commandRateLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.calls++
	code, body := 200, `{"id":"command","name":"template","type":1}`
	if r.calls == 1 {
		code, body = 429, `{"message":"rate limited","retry_after":0.001,"global":false}`
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

// TestCommandRegistrationHonorsDiscordCooldown prevents short registration
// bursts from aborting the entire bot startup after a definite 429 response.
func TestCommandRegistrationHonorsDiscordCooldown(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	transport := &commandRateLimitTransport{}
	session.Client = &http.Client{Transport: transport}
	client := sessionCommandClient{session: session}
	command, err := client.EditCommand(context.Background(), "app", "guild", "command", &discordgo.ApplicationCommand{Name: "template", Description: "Manage rules"})
	if err != nil || command == nil || command.ID != "command" || transport.calls != 2 {
		t.Fatalf("cooldown recovery: command=%+v calls=%d err=%v", command, transport.calls, err)
	}
}

// TestGuildCommandFingerprintMatchesDiscordOmission covers the actual returned
// field difference while preserving meaningful global DM-permission changes.
func TestGuildCommandFingerprintMatchesDiscordOmission(t *testing.T) {
	local := UserCaseCommandSpec().Definition
	remote := *local
	remote.DMPermission = nil
	localHash, _, err := commandFingerprintForScope(local, "guild")
	if err != nil {
		t.Fatal(err)
	}
	remoteHash, _, err := commandFingerprintForScope(&remote, "guild")
	if err != nil || localHash != remoteHash {
		t.Fatalf("guild definitions differ: err=%v", err)
	}
	if local.DMPermission == nil {
		t.Fatal("normalization mutated the registered command")
	}
	localHash, _, _ = commandFingerprintForScope(local, "")
	remoteHash, _, _ = commandFingerprintForScope(&remote, "")
	if localHash == remoteHash {
		t.Fatal("global DM change was ignored")
	}
}

// TestCommandSyncPublishesMentionIDs exercises each sync exit, including the
// cached no-write path used on restarts, without additional Discord requests.
func TestCommandSyncPublishesMentionIDs(t *testing.T) {
	for _, mode := range []string{"create", "edit", "unchanged", "cached"} {
		t.Run(mode, func(t *testing.T) {
			local := CaseCommandDefinition()
			client := &fakeCommandClient{}
			cache := newMemoryCommandCache()
			wantID := "created-case"
			if mode != "create" {
				remote := cloneCommand(local)
				remote.ID = "remote-case"
				wantID = remote.ID
				if mode == "edit" {
					remote.Description = "Old description"
				}
				client.commands = []*discordgo.ApplicationCommand{remote}
				if mode == "cached" {
					hash, _, err := commandFingerprintForScope(local, "")
					if err != nil {
						t.Fatal(err)
					}
					cache.entries["global|case"] = commandCacheEntry{DiscordCommandID: remote.ID, Hash: hash}
				}
			}
			syncer := testCommandSyncer(client, cache)
			syncer.AppID = "sync-mentions-" + mode
			if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: local, Handler: noopHandler}}); err != nil {
				t.Fatal(err)
			}
			if got := ui.ResolveCommandMentions("`/case add`", syncer.AppID); got != "</case add:"+wantID+">" {
				t.Fatal("actual Discord ID was not registered", got)
			}
			if (mode == "unchanged" || mode == "cached") && (len(client.created) != 0 || len(client.edited) != 0) {
				t.Fatal("read-only sync unexpectedly wrote commands")
			}
		})
	}
}

// hashOnlyStore proves command caching requires no application repository methods.
type hashOnlyStore map[string][]byte

// HashGet models Redis's missing-field contract without a live server.
func (s hashOnlyStore) HashGet(_ context.Context, key, field string) ([]byte, error) {
	body, ok := s[key+"|"+field]
	if !ok {
		return nil, redis.Nil
	}
	return body, nil
}

// HashSet records encoded entries for later synchronization runs.
func (s hashOnlyStore) HashSet(_ context.Context, key, field string, value []byte) error {
	s[key+"|"+field] = value
	return nil
}

func TestCommandCacheWithHashOnlyCapability(t *testing.T) {
	ctx := context.Background()
	store := hashOnlyStore{}
	cache := newRedisCommandCache(store)
	if entry, err := cache.Get(ctx, "global", "case"); err != nil || entry != nil {
		t.Fatalf("missing cache field: entry=%+v err=%v", entry, err)
	}
	client := &fakeCommandClient{}
	syncer := testCommandSyncer(client, cache)
	specs := []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}
	for range 2 {
		if err := syncer.Sync(ctx, specs); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.created) != 1 || len(client.edited) != 0 {
		t.Fatalf("cached sync should create once: creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if _, ok := store["discord:commands:global:hashes|case"]; !ok {
		t.Fatal("cache key format changed")
	}
	if entry, err := cache.Get(ctx, "guild:other", "case"); err != nil || entry != nil {
		t.Fatalf("scope leaked: entry=%+v err=%v", entry, err)
	}
}

func TestCommandCacheWithoutInfrastructure(t *testing.T) {
	cache := newRedisCommandCache(nil)
	ctx := context.Background()
	if err := cache.Set(ctx, "global", "case", commandCacheEntry{Hash: "ignored"}); err != nil {
		t.Fatal(err)
	}
	if entry, err := cache.Get(ctx, "global", "case"); err != nil || entry != nil {
		t.Fatalf("disabled cache: entry=%+v err=%v", entry, err)
	}
	client := &fakeCommandClient{}
	if err := testCommandSyncer(client, cache).Sync(ctx, []CommandSpec{{Definition: CaseCommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatal(err)
	}
	if len(client.created) != 1 {
		t.Fatalf("uncached sync created %d commands", len(client.created))
	}
}

func TestCommandHashIgnoresDiscordGeneratedFields(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	right.ID = "remote-command"
	right.ApplicationID = "app"
	right.GuildID = "guild"
	right.Version = "version"

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected generated fields to be ignored, got %s and %s", leftHash, rightHash)
	}
}

func TestCommandHashChangesWhenDefinitionChanges(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	right.Options[0].Options[0].Description = "Changed template description"

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected command hash to change")
	}
}

func TestCommandHashTreatsUnsetTypeAsChatCommand(t *testing.T) {
	left := &discordgo.ApplicationCommand{Name: "case", Description: "Case"}
	right := &discordgo.ApplicationCommand{Name: "case", Description: "Case", Type: discordgo.ChatApplicationCommand}

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected unset command type to match chat command")
	}
}

func TestCommandHashIgnoresRemoteDefaultNSFWFalse(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	nsfw := false
	right.NSFW = &nsfw

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected nsfw=false to be ignored")
	}
}

func TestCommandHashChangesForExplicitNSFWTrue(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	nsfw := true
	right.NSFW = &nsfw

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected nsfw=true to change hash")
	}
}

func TestCommandHashIgnoresRemoteDefaultIntegrationTypes(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	integrationTypes := []discordgo.ApplicationIntegrationType{
		discordgo.ApplicationIntegrationGuildInstall,
		discordgo.ApplicationIntegrationUserInstall,
	}
	right.IntegrationTypes = &integrationTypes

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash != rightHash {
		t.Fatalf("expected default integration types to be ignored")
	}
}

func TestCommandHashChangesForExplicitNonDefaultIntegrationTypes(t *testing.T) {
	left := CaseCommandDefinition()
	right := CaseCommandDefinition()
	integrationTypes := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}
	right.IntegrationTypes = &integrationTypes

	leftHash, err := commandHash(left)
	if err != nil {
		t.Fatalf("hash left: %v", err)
	}
	rightHash, err := commandHash(right)
	if err != nil {
		t.Fatalf("hash right: %v", err)
	}

	if leftHash == rightHash {
		t.Fatalf("expected explicit non-default integration types to change hash")
	}
}
