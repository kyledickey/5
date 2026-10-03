package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	r "github.com/redis/go-redis/v9"
)

type DiscordCommandClient interface {
	ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error)
	CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	DeleteCommand(ctx context.Context, appID, guildID, commandID string) error
}

// CommandSyncer reconciles explicitly registered commands with Discord and the Redis fingerprint cache.
type CommandSyncer struct {
	Client DiscordCommandClient
	Cache  commandHashCache

	AppID        string
	GuildID      string
	PruneEnabled bool
}

type sessionCommandClient struct {
	session *discordgo.Session
}

func (c sessionCommandClient) ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommands(appID, guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

func (c sessionCommandClient) CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandCreate(appID, guildID, command, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

func (c sessionCommandClient) EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandEdit(appID, guildID, commandID, command, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

func (c sessionCommandClient) DeleteCommand(ctx context.Context, appID, guildID, commandID string) error {
	return c.session.ApplicationCommandDelete(appID, guildID, commandID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

// Sync reconciles local command specifications with Discord, using fingerprints to avoid unnecessary writes.
func (s CommandSyncer) Sync(ctx context.Context, specs []CommandSpec) error {
	if s.Client == nil {
		return errors.New("discord command client is not configured")
	}
	if strings.TrimSpace(s.AppID) == "" {
		return errors.New("discord app id is not configured")
	}
	cache := s.Cache
	if cache == nil {
		cache = noopCommandCache{}
	}

	existing, err := s.Client.ListCommands(ctx, s.AppID, s.GuildID)
	if err != nil {
		return fmt.Errorf("list discord application commands: %w", err)
	}

	ui.SetCommandMentions(s.AppID, existing)
	scope := commandScope(s.GuildID)
	slog.Info("Syncing Discord application commands", "scope", scope, "app_id", s.AppID, "local_command_count", len(specs), "remote_command_count", len(existing), "prune_enabled", s.PruneEnabled)

	existingByName := make(map[string]*discordgo.ApplicationCommand, len(existing))
	for _, command := range existing {
		if command == nil {
			continue
		}
		existingByName[command.Name] = command
	}

	localCommandNames := make(map[string]struct{}, len(specs))
	for _, spec := range sortedSpecs(specs) {
		if spec.Definition == nil {
			continue
		}
		localCommandNames[spec.Definition.Name] = struct{}{}
		if err := s.syncOne(ctx, cache, scope, existingByName[spec.Definition.Name], spec); err != nil {
			return err
		}
	}

	remaining, err := s.retireRenamedContextCommands(ctx, existing, specs)
	if err != nil {
		return err
	}
	return s.pruneRemoteOnlyCommands(ctx, scope, remaining, localCommandNames)
}

// retireRenamedContextCommands removes only the two known legacy moderation
// entries after every local command has synced successfully. This targeted rename
// migration runs even when broad pruning is disabled and reuses the fetched list.
func (s CommandSyncer) retireRenamedContextCommands(ctx context.Context, remote []*discordgo.ApplicationCommand, specs []CommandSpec) ([]*discordgo.ApplicationCommand, error) {
	type rename struct {
		old, replacement string
		kind             discordgo.ApplicationCommandType
	}
	renames := []rename{
		{"Create moderation case", "Add case", discordgo.MessageApplicationCommand},
		{"Create case for member", "Add case for member", discordgo.UserApplicationCommand},
	}
	remaining := make([]*discordgo.ApplicationCommand, 0, len(remote))
	for _, command := range remote {
		retire := false
		if command != nil {
			for _, change := range renames {
				if command.Name != change.old || command.Type != change.kind {
					continue
				}
				for _, spec := range specs {
					if spec.Definition != nil && spec.Definition.Name == change.replacement && spec.Definition.Type == change.kind {
						retire = true
						break
					}
				}
			}
		}
		if !retire {
			remaining = append(remaining, command)
			continue
		}
		if err := s.Client.DeleteCommand(ctx, s.AppID, s.GuildID, command.ID); err != nil {
			return nil, fmt.Errorf("retire renamed context command %s: %w", command.Name, err)
		}
		slog.Info("Retired renamed Discord context command", "command", command.Name, "remote_command_id", command.ID)
	}
	return remaining, nil
}

func (s CommandSyncer) syncOne(ctx context.Context, cache commandHashCache, scope string, remote *discordgo.ApplicationCommand, spec CommandSpec) error {
	command := spec.Definition
	commandName := command.Name
	localHash, localDefinition, err := commandFingerprintForScope(command, s.GuildID)
	if err != nil {
		return fmt.Errorf("hash command %s: %w", commandName, err)
	}

	cached, err := cache.Get(ctx, scope, commandName)
	if err != nil {
		slog.Warn("Command cache read failed; falling back to remote comparison", "error", err, "command", commandName)
	}
	slog.Debug("Evaluating Discord application command sync", "command", commandName, "scope", scope, "local_hash", localHash, "cached_command_id", cachedCommandID(cached), "cached_hash", cachedHash(cached), "remote_exists", remote != nil)

	if remote == nil {
		slog.Info("Discord application command missing remotely; creating", "command", commandName, "scope", scope, "local_hash", localHash, "cached_command_id", cachedCommandID(cached), "cached_hash", cachedHash(cached))
		created, err := s.Client.CreateCommand(ctx, s.AppID, s.GuildID, command)
		if err != nil {
			return fmt.Errorf("create discord application command %s: %w", commandName, err)
		}
		ui.RegisterCommandMentions(s.AppID, command, commandID(created))
		s.cacheCommand(ctx, cache, scope, commandName, commandID(created), localHash)
		slog.Info("Registered Discord application command", "command", commandName)
		return nil
	}

	remoteHash, remoteDefinition, err := commandFingerprintForScope(remote, s.GuildID)
	if err != nil {
		return fmt.Errorf("hash remote command %s: %w", commandName, err)
	}
	if shouldSkipCommandSync(cached, remote.ID, localHash, remoteHash) {
		ui.RegisterCommandMentions(s.AppID, command, remote.ID)
		slog.Info("Discord application command is unchanged; skipping", "command", commandName, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "remote_hash", remoteHash, "cached_hash", cachedHash(cached))
		return nil
	}
	if remoteHash == localHash {
		ui.RegisterCommandMentions(s.AppID, command, remote.ID)
		slog.Info("Discord application command matches remote definition; refreshing cache only", "command", commandName, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "remote_hash", remoteHash, "cached_command_id", cachedCommandID(cached), "cached_hash", cachedHash(cached))
		s.cacheCommand(ctx, cache, scope, commandName, remote.ID, localHash)
		return nil
	}

	slog.Info("Discord application command definition hash changed; updating", "command", commandName, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "remote_hash", remoteHash, "cached_command_id", cachedCommandID(cached), "cached_hash", cachedHash(cached))
	slog.Debug("Discord application command canonical definitions differ", "command", commandName, "local_definition", localDefinition, "remote_definition", remoteDefinition)

	updated, err := s.Client.EditCommand(ctx, s.AppID, s.GuildID, remote.ID, command)
	if err != nil {
		return fmt.Errorf("update discord application command %s: %w", commandName, err)
	}
	ui.RegisterCommandMentions(s.AppID, command, commandID(updated))
	s.cacheCommand(ctx, cache, scope, commandName, commandID(updated), localHash)
	slog.Info("Updated Discord application command", "command", commandName)
	return nil
}

func (s CommandSyncer) pruneRemoteOnlyCommands(ctx context.Context, scope string, remoteCommands []*discordgo.ApplicationCommand, localCommandNames map[string]struct{}) error {
	remoteOnly := make([]*discordgo.ApplicationCommand, 0)
	for _, command := range remoteCommands {
		if command == nil {
			continue
		}
		if _, ok := localCommandNames[command.Name]; ok {
			continue
		}
		remoteOnly = append(remoteOnly, command)
	}
	if len(remoteOnly) == 0 {
		return nil
	}

	remoteOnlyNames := commandNames(remoteOnly)
	if !s.PruneEnabled {
		slog.Info("Remote Discord application commands are not registered locally; pruning disabled", "scope", scope, "remote_only_command_count", len(remoteOnly), "remote_only_commands", remoteOnlyNames)
		return nil
	}

	for _, command := range remoteOnly {
		slog.Info("Deleting remote Discord application command missing from local registry", "scope", scope, "command", command.Name, "remote_command_id", command.ID)
		if err := s.Client.DeleteCommand(ctx, s.AppID, s.GuildID, command.ID); err != nil {
			return fmt.Errorf("delete remote-only discord application command %s: %w", command.Name, err)
		}
		ui.RemoveCommandMentions(s.AppID, command.Name)
	}

	return nil
}

func (s CommandSyncer) cacheCommand(ctx context.Context, cache commandHashCache, scope, commandName, commandID, hash string) {
	if err := cache.Set(ctx, scope, commandName, commandCacheEntry{
		DiscordCommandID: commandID,
		Hash:             hash,
	}); err != nil {
		slog.Warn("Command cache write failed", "error", err, "command", commandName)
	}
}

func shouldSkipCommandSync(cached *commandCacheEntry, remoteCommandID, localHash, remoteHash string) bool {
	return cached != nil &&
		cached.DiscordCommandID == remoteCommandID &&
		cached.Hash == localHash &&
		remoteHash == localHash
}

func cachedCommandID(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.DiscordCommandID
}

func cachedHash(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.Hash
}

func commandScope(guildID string) string {
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return "global"
	}
	return "guild:" + guildID
}

func commandID(command *discordgo.ApplicationCommand) string {
	if command == nil {
		return ""
	}
	return command.ID
}

func sortedSpecs(specs []CommandSpec) []CommandSpec {
	out := make([]CommandSpec, 0, len(specs))
	out = append(out, specs...)
	sort.Slice(out, func(i, j int) bool {
		left := ""
		right := ""
		if out[i].Definition != nil {
			left = out[i].Definition.Name
		}
		if out[j].Definition != nil {
			right = out[j].Definition.Name
		}
		return left < right
	})
	return out
}

func commandNames(commands []*discordgo.ApplicationCommand) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		if command == nil {
			continue
		}
		names = append(names, command.Name)
	}
	sort.Strings(names)
	return names
}

// commandFingerprintForScope ignores DM availability for guild-only commands.
// Discord omits that global-only field on reads; retaining it causes every
// restart to rewrite unchanged commands. Global command behavior stays intact.
func commandFingerprintForScope(command *discordgo.ApplicationCommand, guildID string) (string, string, error) {
	if command == nil || guildID == "" {
		return commandFingerprint(command)
	}
	copy := *command
	copy.DMPermission = nil
	return commandFingerprint(&copy)
}

type canonicalCommand struct {
	Type                     discordgo.ApplicationCommandType        `json:"type"`
	Name                     string                                  `json:"name"`
	NameLocalizations        *map[discordgo.Locale]string            `json:"name_localizations,omitempty"`
	Description              string                                  `json:"description,omitempty"`
	DescriptionLocalizations *map[discordgo.Locale]string            `json:"description_localizations,omitempty"`
	DefaultPermission        *bool                                   `json:"default_permission,omitempty"`
	DefaultMemberPermissions *int64                                  `json:"default_member_permissions,omitempty"`
	DMPermission             *bool                                   `json:"dm_permission,omitempty"`
	NSFW                     *bool                                   `json:"nsfw,omitempty"`
	Contexts                 *[]discordgo.InteractionContextType     `json:"contexts,omitempty"`
	IntegrationTypes         *[]discordgo.ApplicationIntegrationType `json:"integration_types,omitempty"`
	Options                  []canonicalCommandOption                `json:"options,omitempty"`
}

type canonicalCommandOption struct {
	Type                     discordgo.ApplicationCommandOptionType `json:"type"`
	Name                     string                                 `json:"name"`
	NameLocalizations        map[discordgo.Locale]string            `json:"name_localizations,omitempty"`
	Description              string                                 `json:"description,omitempty"`
	DescriptionLocalizations map[discordgo.Locale]string            `json:"description_localizations,omitempty"`
	ChannelTypes             []discordgo.ChannelType                `json:"channel_types,omitempty"`
	Required                 bool                                   `json:"required,omitempty"`
	Options                  []canonicalCommandOption               `json:"options,omitempty"`
	Autocomplete             bool                                   `json:"autocomplete,omitempty"`
	Choices                  []canonicalCommandOptionChoice         `json:"choices,omitempty"`
	MinValue                 *float64                               `json:"min_value,omitempty"`
	MaxValue                 float64                                `json:"max_value,omitempty"`
	MinLength                *int                                   `json:"min_length,omitempty"`
	MaxLength                int                                    `json:"max_length,omitempty"`
}

type canonicalCommandOptionChoice struct {
	Name              string                      `json:"name"`
	NameLocalizations map[discordgo.Locale]string `json:"name_localizations,omitempty"`
	Value             any                         `json:"value"`
}

func commandHash(command *discordgo.ApplicationCommand) (string, error) {
	hash, _, err := commandFingerprint(command)
	return hash, err
}

func commandFingerprint(command *discordgo.ApplicationCommand) (string, string, error) {
	canonical := canonicalizeCommand(command)
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", "", err
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), string(body), nil
}

func canonicalizeCommand(command *discordgo.ApplicationCommand) canonicalCommand {
	if command == nil {
		return canonicalCommand{}
	}

	commandType := command.Type
	if commandType == 0 {
		commandType = discordgo.ChatApplicationCommand
	}

	return canonicalCommand{
		Type:                     commandType,
		Name:                     command.Name,
		NameLocalizations:        command.NameLocalizations,
		Description:              command.Description,
		DescriptionLocalizations: command.DescriptionLocalizations,
		DefaultPermission:        command.DefaultPermission,
		DefaultMemberPermissions: command.DefaultMemberPermissions,
		DMPermission:             command.DMPermission,
		NSFW:                     normalizedBoolPointer(command.NSFW),
		Contexts:                 command.Contexts,
		IntegrationTypes:         normalizedIntegrationTypes(command.IntegrationTypes),
		Options:                  canonicalizeOptions(command.Options),
	}
}

func canonicalizeOptions(options []*discordgo.ApplicationCommandOption) []canonicalCommandOption {
	if len(options) == 0 {
		return nil
	}

	out := make([]canonicalCommandOption, 0, len(options))
	for _, option := range options {
		if option == nil {
			continue
		}

		out = append(out, canonicalCommandOption{
			Type:                     option.Type,
			Name:                     option.Name,
			NameLocalizations:        option.NameLocalizations,
			Description:              option.Description,
			DescriptionLocalizations: option.DescriptionLocalizations,
			ChannelTypes:             option.ChannelTypes,
			Required:                 option.Required,
			Options:                  canonicalizeOptions(option.Options),
			Autocomplete:             option.Autocomplete,
			Choices:                  canonicalizeChoices(option.Choices),
			MinValue:                 option.MinValue,
			MaxValue:                 option.MaxValue,
			MinLength:                option.MinLength,
			MaxLength:                option.MaxLength,
		})
	}

	return out
}

func normalizedBoolPointer(value *bool) *bool {
	if value == nil || !*value {
		return nil
	}
	return value
}

func normalizedIntegrationTypes(value *[]discordgo.ApplicationIntegrationType) *[]discordgo.ApplicationIntegrationType {
	if value == nil || len(*value) == 0 {
		return nil
	}

	copied := append([]discordgo.ApplicationIntegrationType(nil), (*value)...)
	sort.Slice(copied, func(i, j int) bool {
		return copied[i] < copied[j]
	})
	if len(copied) == 2 &&
		copied[0] == discordgo.ApplicationIntegrationGuildInstall &&
		copied[1] == discordgo.ApplicationIntegrationUserInstall {
		return nil
	}
	return &copied
}

func canonicalizeChoices(choices []*discordgo.ApplicationCommandOptionChoice) []canonicalCommandOptionChoice {
	if len(choices) == 0 {
		return nil
	}

	out := make([]canonicalCommandOptionChoice, 0, len(choices))
	for _, choice := range choices {
		if choice == nil {
			continue
		}

		out = append(out, canonicalCommandOptionChoice{
			Name:              choice.Name,
			NameLocalizations: choice.NameLocalizations,
			Value:             choice.Value,
		})
	}

	return out
}

// commandCacheEntry pairs a Discord command ID with the last synchronized definition hash.
type commandCacheEntry struct {
	DiscordCommandID string `json:"discord_command_id"`
	Hash             string `json:"hash"`
}

// commandHashCache remembers synchronized definitions independently for each Discord scope.
type commandHashCache interface {
	Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error)
	Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error
}

// noopCommandCache leaves synchronization enabled when no cache capability is supplied.
type noopCommandCache struct{}

// Get reports a cache miss so synchronization compares the live Discord definition.
func (noopCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	return nil, nil
}

// Set discards fingerprints when caching is disabled.
func (noopCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	return nil
}

// CommandHashStore provides only the hash operations used to remember synchronized
// Discord command definitions. Missing fields must return redis.Nil.
type CommandHashStore interface {
	HashGet(ctx context.Context, key, field string) ([]byte, error)
	HashSet(ctx context.Context, key, field string, value []byte) error
}

// redisCommandCache encodes fingerprints in per-scope hashes using only hash storage.
type redisCommandCache struct {
	store CommandHashStore
}

// newRedisCommandCache uses the supplied hash capability, or disables caching when absent.
func newRedisCommandCache(store CommandHashStore) commandHashCache {
	if store == nil {
		return noopCommandCache{}
	}
	return redisCommandCache{store: store}
}

// Get decodes a stored fingerprint, treating absent Redis fields as cache misses.
func (c redisCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	body, err := c.store.HashGet(ctx, commandCacheKey(scope), commandName)
	if err != nil {
		if errors.Is(err, r.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read command cache: %w", err)
	}

	var entry commandCacheEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decode command cache: %w", err)
	}
	return &entry, nil
}

// Set persists the synchronized command ID and hash without expiring the fingerprint.
func (c redisCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode command cache: %w", err)
	}
	if err := c.store.HashSet(ctx, commandCacheKey(scope), commandName, body); err != nil {
		return fmt.Errorf("write command cache: %w", err)
	}
	return nil
}

// commandCacheKey isolates global and guild fingerprints within the existing Redis namespace.
func commandCacheKey(scope string) string {
	return "discord:commands:" + scope + ":hashes"
}
