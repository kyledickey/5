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

// errModuleNotSetUp is returned when core settings try to enable a module that
// has never been configured natively; the copy points administrators at /setup.
var errModuleNotSetUp = errors.New("module is not configured; run /setup first")

// ValidateGuildModuleEnablement is the hook core guild settings call before
// flipping a module's enabled flag. It re-runs the module's enabled-state
// validation and the live Discord checks that native /setup performs, without
// writing configuration or posting panels, and returns the exact configuration
// JSON the core transaction compares before committing.
func (r *Runtime) ValidateGuildModuleEnablement(ctx context.Context, guild *quack.GuildStaffContext, moduleID string) (string, error) {
	if guild == nil || guild.Guild == nil {
		return "", errors.New("module validation requires a guild context")
	}
	config, err := r.registry.Configuration(ctx, guild.Guild.ID, modules.ID(moduleID))
	if err != nil {
		return "", err
	}
	if config == nil {
		return "", errModuleNotSetUp
	}
	switch config.ModuleID {
	case modules.Tickets:
		err = r.validateTicketsEnablement(ctx, guild, config.ConfigJSON)
	case modules.GeneralLogging:
		err = r.validateLoggingEnablement(ctx, guild, config.ConfigJSON)
	case modules.Honeypots:
		err = r.validateHoneypotEnablement(ctx, guild, config.ConfigJSON)
	default:
		err = errors.New("unknown module")
	}
	if err != nil {
		return "", err
	}
	return config.ConfigJSON, nil
}

// validateTicketsEnablement checks the stored ticket settings the way ticket
// setup does: distinct entry and queue channels, an accessible text entry
// channel in this guild, a staff-only queue channel and the bot permissions
// both need.
func (r *Runtime) validateTicketsEnablement(ctx context.Context, guild *quack.GuildStaffContext, configJSON string) error {
	if err := tickets.ValidateEnabledConfiguration(configJSON); err != nil {
		return err
	}
	var settings tickets.Settings
	if err := json.Unmarshal([]byte(configJSON), &settings); err != nil {
		return err
	}
	if settings.EntryChannelDiscordID == settings.QueueChannelDiscordID {
		return errors.New("ticket entry and staff queue channels must be separate")
	}
	discordGuildID := guild.Guild.DiscordGuildID
	channel, err := r.session.Channel(settings.EntryChannelDiscordID, restOptions(ctx)...)
	if err != nil {
		return err
	}
	if channel == nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New("ticket entry must be an accessible text channel in this server")
	}
	bot := &discordadapter.Bot{Session: r.session}
	if err := bot.ValidateStaffChannel(ctx, discordGuildID, settings.QueueChannelDiscordID); err != nil {
		return err
	}
	client := ticketDiscordClient{session: r.session}
	return client.validateTicketBotPermissions(ctx, discordGuildID, settings.EntryChannelDiscordID, settings.QueueChannelDiscordID)
}

// validateLoggingEnablement checks the stored logging routes the way logging
// setup does. Delivery permissions alone are not enough: attributing external
// bans and unbans needs guild audit-log access, so that is required too.
func (r *Runtime) validateLoggingEnablement(ctx context.Context, guild *quack.GuildStaffContext, configJSON string) error {
	if err := generallogging.ValidateEnabledConfiguration(configJSON); err != nil {
		return err
	}
	var settings generallogging.Settings
	if err := json.Unmarshal([]byte(configJSON), &settings); err != nil {
		return err
	}
	if err := requireAuditLogAccess(ctx, r.session, guild.Guild.DiscordGuildID); err != nil {
		return err
	}
	client := loggingDiscordClient{session: r.session, resolver: r.resolver}
	seen := map[string]bool{}
	for _, channelID := range settings.Channels {
		if seen[channelID] {
			continue
		}
		seen[channelID] = true
		if err := client.ValidateStaffOnlyChannel(ctx, guild.Guild.ID, channelID); err != nil {
			return err
		}
	}
	return nil
}

// validateHoneypotEnablement checks the stored trap channel and template
// against live Discord and current template policy.
func (r *Runtime) validateHoneypotEnablement(ctx context.Context, guild *quack.GuildStaffContext, configJSON string) error {
	if err := honeypot.ValidateEnabledConfiguration(configJSON); err != nil {
		return err
	}
	var settings honeypot.Settings
	if err := json.Unmarshal([]byte(configJSON), &settings); err != nil {
		return err
	}
	channels := honeypotChannelValidator{session: r.session, resolver: r.resolver}
	if err := channels.ValidateHoneypotChannel(ctx, guild.Guild.ID, settings.ChannelDiscordID); err != nil {
		return err
	}
	return r.honeypotTemplates.ValidateHoneypotTemplate(ctx, guild.Guild.ID, settings.TemplateID)
}

// errAuditLogAccessRequired explains why logging cannot be enabled without the
// View Audit Log permission; the same condition gates native logging setup.
var errAuditLogAccessRequired = errors.New(
	"enabling logging requires the View Audit Log permission so bans by other moderators can be logged without duplicating Quack's own actions",
)

// requireAuditLogAccess loads the bot's current guild membership and fails when
// it cannot read the audit log.
func requireAuditLogAccess(ctx context.Context, session *discordgo.Session, discordGuildID string) error {
	guild, member, err := currentBotMember(ctx, session, discordGuildID)
	if err != nil {
		return errAuditLogAccessRequired
	}
	guildLevel := &discordgo.Channel{GuildID: discordGuildID}
	if channelPermissions(guild, guildLevel, member)&discordgo.PermissionViewAuditLogs == 0 {
		return errAuditLogAccessRequired
	}
	return nil
}
