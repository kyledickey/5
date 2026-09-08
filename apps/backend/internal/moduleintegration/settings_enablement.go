package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// ValidateGuildModuleEnablement reuses retained native setup without replacing
// settings or posting panels. The core transaction compares these validated
// configuration bytes before changing the canonical enabled flag.
func (r *Runtime) ValidateGuildModuleEnablement(ctx context.Context, guild *quack.GuildStaffContext, moduleID string) (string, error) {
	if r == nil || r.registry == nil || guild == nil || guild.Guild == nil {
		return "", errors.New("module validation unavailable")
	}
	config, err := r.registry.Configuration(ctx, guild.Guild.ID, modules.ID(moduleID))
	if err != nil {
		return "", err
	}
	if config == nil {
		return "", errors.New("module is not configured; run /setup first")
	}
	switch config.ModuleID {
	case modules.Tickets:
		if err := tickets.ValidateEnabledConfiguration(config.ConfigJSON); err != nil {
			return "", err
		}
		var settings tickets.Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return "", err
		}
		if settings.EntryChannelDiscordID == settings.QueueChannelDiscordID {
			return "", errors.New("ticket entry and staff queue channels must be separate")
		}
		if r.session == nil {
			return "", errors.New("Discord validation unavailable")
		}
		channel, err := r.session.Channel(settings.EntryChannelDiscordID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return "", err
		}
		if channel == nil || channel.GuildID != guild.Guild.DiscordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
			return "", errors.New("ticket entry must be an accessible text channel in this server")
		}
		if err := (&discordadapter.Bot{Session: r.session}).ValidateStaffChannel(ctx, guild.Guild.DiscordGuildID, settings.QueueChannelDiscordID); err != nil {
			return "", err
		}
		if err := (ticketDiscordClient{session: r.session}).validateTicketBotPermissions(ctx, guild.Guild.DiscordGuildID, settings.EntryChannelDiscordID, settings.QueueChannelDiscordID); err != nil {
			return "", err
		}
	case modules.GeneralLogging:
		if err := generallogging.ValidateEnabledConfiguration(config.ConfigJSON); err != nil {
			return "", err
		}
		var settings generallogging.Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return "", err
		}
		if r.session == nil {
			return "", errors.New("Discord validation unavailable")
		}
		// Match native logging setup: channel delivery alone cannot provide
		// attributed external ban/unban events without guild audit-log access.
		discordGuild, member, err := currentBotMember(ctx, r.session, guild.Guild.DiscordGuildID)
		if err != nil || channelPermissions(discordGuild, &discordgo.Channel{GuildID: guild.Guild.DiscordGuildID}, member)&discordgo.PermissionViewAuditLogs == 0 {
			return "", errors.New("Quack needs View Audit Log permission to log bans by other moderators without duplicating its own actions")
		}
		seen := map[string]bool{}
		for _, channelID := range settings.Channels {
			if seen[channelID] {
				continue
			}
			seen[channelID] = true
			if err := (loggingDiscordClient{session: r.session, resolver: r.resolver}).ValidateStaffOnlyChannel(ctx, guild.Guild.ID, channelID); err != nil {
				return "", err
			}
		}
	case modules.Honeypots:
		if err := honeypot.ValidateEnabledConfiguration(config.ConfigJSON); err != nil {
			return "", err
		}
		var settings honeypot.Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return "", err
		}
		if r.session == nil {
			return "", errors.New("Discord validation unavailable")
		}
		if err := (honeypotChannelValidator{session: r.session, resolver: r.resolver}).ValidateHoneypotChannel(ctx, guild.Guild.ID, settings.ChannelDiscordID); err != nil {
			return "", err
		}
		if err := (r.honeypotTemplates).ValidateHoneypotTemplate(ctx, guild.Guild.ID, settings.TemplateID); err != nil {
			return "", err
		}
	default:
		return "", errors.New("unknown module")
	}
	return config.ConfigJSON, nil
}
