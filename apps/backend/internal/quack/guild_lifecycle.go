package quack

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// GuildOperationalHealth reports guild-local Discord and managed-channel
// degradation without changing process readiness for healthy guilds.
type GuildOperationalHealth struct {
	Degraded        bool            `json:"degraded"`
	Reasons         []string        `json:"reasons"`
	BotPermissions  map[string]bool `json:"bot_permissions"`
	ManagedChannels map[string]bool `json:"managed_channels"`
}

// OperationalGuildID resolves a stored guild identity for an already authorized
// operator. It does not require live Discord access, so diagnostics remain usable
// during an outage. An unknown guild returns an empty ID without creating it.
func (s *GuildService) OperationalGuildID(ctx context.Context, discordGuildID string) (string, error) {
	if s == nil || s.store == nil {
		return "", errors.New("guild service is not configured")
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return "", err
	}
	return guild.ID, nil
}

// OperationalGuildHealth refreshes the bot's current guild permissions and
// combines them with managed-channel configuration. Deleted channel gateway
// reconciliation clears stale references, so missing required references are
// reported immediately and isolated to this guild.
func (s *GuildService) OperationalGuildHealth(ctx context.Context, discordGuildID string) (GuildOperationalHealth, error) {
	status := GuildOperationalHealth{Reasons: []string{}, BotPermissions: map[string]bool{}, ManagedChannels: map[string]bool{}}
	if s == nil || s.store == nil || s.discord == nil {
		return status, ErrAuthorizationUnavailable
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return status, ErrBotNotInGuild
	}
	live, err := s.discord.GuildAuthorization(ctx, guild.DiscordGuildID, "", "")
	if err != nil || live == nil || !live.Bot.Present {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "discord_bot_unavailable")
		return status, nil
	}
	permissions := map[string]int64{
		"moderate_members": discordgo.PermissionModerateMembers,
		"kick_members":     discordgo.PermissionKickMembers,
		"ban_members":      discordgo.PermissionBanMembers,
		"manage_channels":  discordgo.PermissionManageChannels,
	}
	for name, permission := range permissions {
		available := hasDiscordPermission(live.Bot.PermissionBits, uint64(permission))
		status.BotPermissions[name] = available
		if !available {
			status.Degraded = true
			status.Reasons = append(status.Reasons, "missing_bot_permission:"+name)
		}
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return status, err
	}
	status.ManagedChannels["evidence"] = strings.TrimSpace(settings.ManagedEvidenceChannelDiscordID) != ""
	status.ManagedChannels["audit_mirror"] = strings.TrimSpace(settings.AuditMirrorChannelDiscordID) != ""
	if !status.ManagedChannels["evidence"] {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "managed_evidence_channel_unavailable")
	}
	return status, nil
}

// BootstrapDiscordGuild atomically installs or reactivates a guild, refreshes metadata, repairs stale channels, and ensures one starter policy.
func (s *GuildService) BootstrapDiscordGuild(ctx context.Context, input DiscordGuildLifecycleInput) (*model.BootstrapGuildResult, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("guild service is not configured")
	}
	guildID := strings.TrimSpace(input.DiscordGuildID)
	if guildID == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.OwnerDiscordUserID) == "" {
		return nil, errors.New("guild id, name, and owner are required")
	}
	return s.store.BootstrapGuild(ctx, model.BootstrapGuildParams{
		DiscordGuildID: guildID, Name: strings.TrimSpace(input.Name),
		IconURL:                discordGuildIconURL(guildID, strings.TrimSpace(input.Icon)),
		OwnerDiscordUserID:     strings.TrimSpace(input.OwnerDiscordUserID),
		KnownChannelDiscordIDs: input.KnownChannelDiscordIDs,
	})
}

// DeactivateDiscordGuild marks a true guild departure inactive while preserving settings, starter policy, and history.
func (s *GuildService) DeactivateDiscordGuild(ctx context.Context, discordGuildID string) (*model.Guild, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("guild service is not configured")
	}
	return s.store.DeactivateGuild(ctx, strings.TrimSpace(discordGuildID), systemGuildAudit("guild.lifecycle.leave", "guild"))
}

// ClearDeletedChannel removes stale core settings references when Discord confirms a configured channel was deleted.
func (s *GuildService) ClearDeletedChannel(ctx context.Context, discordGuildID, channelID string) (*model.GuildSettings, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("guild service is not configured")
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return nil, err
	}
	return s.store.ClearGuildChannelReferences(ctx, guild.ID, strings.TrimSpace(channelID), systemGuildAudit("guild_settings.channel_reference.cleared", "guild_settings"))
}

// systemGuildAudit creates adapter-attributed lifecycle evidence without pretending the bot is a Discord staff member.
func systemGuildAudit(action, resourceType string) *model.AuditLogEntry {
	return &model.AuditLogEntry{
		ActorDiscordUserID: "quack-system", Source: model.AuditSourceDiscord,
		Action: action, ResourceType: resourceType, Result: model.AuditResultSuccess,
		MetadataJSON: "{}",
	}
}
