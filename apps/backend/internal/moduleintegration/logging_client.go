package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
)

// loggingDiscordClient sends already-redacted payloads only to channels whose
// everyone role is denied visibility.
type loggingDiscordClient struct {
	session  *discordgo.Session
	resolver guildResolver
}

// SendStaffLog delivers one mention-suppressed message to a validated channel.
func (c loggingDiscordClient) SendStaffLog(ctx context.Context, guildID, channelID, payload string) error {
	if err := c.ValidateStaffOnlyChannel(ctx, guildID, channelID); err != nil {
		return err
	}
	_, err := c.session.ChannelMessageSendComplex(channelID, views.StaffLogMessage(payload).SendParams(ui.SessionApplicationID(c.session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// ValidateStaffOnlyChannel rejects missing, cross-guild, or publicly visible destinations.
func (c loggingDiscordClient) ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel.GuildID != discordGuildID {
		return errors.New("logging destination belongs to another guild")
	}
	return c.validateLoggingACL(channel)
}

// validateLoggingACL permits visibility only through current staff-capable
// roles (plus the bot itself) and requires the bot to send successfully.
func (c loggingDiscordClient) validateLoggingACL(channel *discordgo.Channel) error {
	if err := validateStaffOnlyACL(channel, channel.GuildID, nil); err != nil {
		return err
	}
	roles, err := c.session.GuildRoles(channel.GuildID)
	if err != nil {
		return err
	}
	staffRoles := make(map[string]bool, len(roles))
	for _, role := range roles {
		if role == nil {
			continue
		}
		staffRoles[role.ID] = role.Permissions&(discordgo.PermissionAdministrator|discordgo.PermissionManageServer|discordgo.PermissionModerateMembers) != 0
	}
	botID := ""
	if c.session.State != nil && c.session.State.User != nil {
		botID = c.session.State.User.ID
	}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Allow&discordgo.PermissionViewChannel == 0 {
			continue
		}
		switch overwrite.Type {
		case discordgo.PermissionOverwriteTypeRole:
			if overwrite.ID != channel.GuildID && !staffRoles[overwrite.ID] {
				return errors.New("logging destination grants a non-staff role visibility")
			}
		case discordgo.PermissionOverwriteTypeMember:
			if overwrite.ID != botID {
				return errors.New("logging destination grants a non-bot member visibility")
			}
		}
	}
	if botID == "" {
		return errors.New("Discord bot identity is unavailable")
	}
	permissions, err := c.session.UserChannelPermissions(botID, channel.ID)
	if err != nil {
		return err
	}
	if permissions&discordgo.PermissionViewChannel == 0 || permissions&discordgo.PermissionSendMessages == 0 {
		return errors.New("Discord bot cannot deliver to logging destination")
	}
	return nil
}
