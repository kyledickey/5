package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"log/slog"

	"github.com/bwmarrin/discordgo"
)

// DiscordCommandClient defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
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

// sessionCommandClient defines the external operations needed by this package, keeping the concrete client at the adapter boundary.
type sessionCommandClient struct {
	session *discordgo.Session
}

// ListCommands returns commands subject to authorization, ordering, and filtering constraints.
func (c sessionCommandClient) ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommands(appID, guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

// CreateCommand creates command while preserving validation, authorization, and persistence invariants.
func (c sessionCommandClient) CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandCreate(appID, guildID, command, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

// EditCommand encapsulates the edit command rule so callers share one consistent package implementation.
func (c sessionCommandClient) EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandEdit(appID, guildID, commandID, command, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(true))
}

// DeleteCommand encapsulates the delete command rule so callers share one consistent package implementation.
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

	return s.pruneRemoteOnlyCommands(ctx, scope, existing, localCommandNames)
}

// syncOne encapsulates the sync one rule so callers share one consistent package implementation.
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
		s.cacheCommand(ctx, cache, scope, commandName, commandID(created), localHash)
		slog.Info("Registered Discord application command", "command", commandName)
		return nil
	}

	remoteHash, remoteDefinition, err := commandFingerprintForScope(remote, s.GuildID)
	if err != nil {
		return fmt.Errorf("hash remote command %s: %w", commandName, err)
	}
	if shouldSkipCommandSync(cached, remote.ID, localHash, remoteHash) {
		slog.Info("Discord application command is unchanged; skipping", "command", commandName, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "remote_hash", remoteHash, "cached_hash", cachedHash(cached))
		return nil
	}
	if remoteHash == localHash {
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
	s.cacheCommand(ctx, cache, scope, commandName, commandID(updated), localHash)
	slog.Info("Updated Discord application command", "command", commandName)
	return nil
}

// pruneRemoteOnlyCommands encapsulates the prune remote only commands rule so callers share one consistent package implementation.
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
	}

	return nil
}

// cacheCommand encapsulates the cache command rule so callers share one consistent package implementation.
func (s CommandSyncer) cacheCommand(ctx context.Context, cache commandHashCache, scope, commandName, commandID, hash string) {
	if err := cache.Set(ctx, scope, commandName, commandCacheEntry{
		DiscordCommandID: commandID,
		Hash:             hash,
	}); err != nil {
		slog.Warn("Command cache write failed", "error", err, "command", commandName)
	}
}

// shouldSkipCommandSync encapsulates the should skip command sync rule so callers share one consistent package implementation.
func shouldSkipCommandSync(cached *commandCacheEntry, remoteCommandID, localHash, remoteHash string) bool {
	return cached != nil &&
		cached.DiscordCommandID == remoteCommandID &&
		cached.Hash == localHash &&
		remoteHash == localHash
}

// cachedCommandID encapsulates the cached command id rule so callers share one consistent package implementation.
func cachedCommandID(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.DiscordCommandID
}

// cachedHash computes a stable digest for cached hash so unchanged Discord commands can skip synchronization.
func cachedHash(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.Hash
}

// commandScope encapsulates the command scope rule so callers share one consistent package implementation.
func commandScope(guildID string) string {
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return "global"
	}
	return "guild:" + guildID
}

// commandID encapsulates the command id rule so callers share one consistent package implementation.
func commandID(command *discordgo.ApplicationCommand) string {
	if command == nil {
		return ""
	}
	return command.ID
}

// sortedSpecs produces a stable sorted specs representation for deterministic validation, comparison, or caching.
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

// commandNames encapsulates the command names rule so callers share one consistent package implementation.
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
		return CommandFingerprint(command)
	}
	copy := *command
	copy.DMPermission = nil
	return CommandFingerprint(&copy)
}
