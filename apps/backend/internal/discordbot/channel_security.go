package discordbot

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// ValidateStaffChannel checks current guild ownership and an explicitly private text-channel ACL.
// Every explicit viewer must be the bot or hold current guild moderation authority.
func (b *Bot) ValidateStaffChannel(ctx context.Context, guildID, channelID string) error {
	if b == nil || b.Session == nil {
		return quack.ErrAuthorizationUnavailable
	}
	channel, err := b.Session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || channel == nil || channel.GuildID != guildID || channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New("destination must be a private text channel in this guild")
	}
	guild, err := b.Session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || guild == nil {
		return quack.ErrAuthorizationUnavailable
	}
	roles := make(map[string]int64, len(guild.Roles))
	for _, role := range guild.Roles {
		if role != nil {
			roles[role.ID] = role.Permissions
		}
	}
	botID := ""
	if b.Session.State != nil && b.Session.State.User != nil {
		botID = b.Session.State.User.ID
	}
	private := false
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite == nil {
			continue
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole && overwrite.ID == guildID {
			private = overwrite.Deny&discordgo.PermissionViewChannel != 0 && overwrite.Allow&discordgo.PermissionViewChannel == 0
		}
		if overwrite.Allow&discordgo.PermissionViewChannel == 0 {
			continue
		}
		if overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			if roles[overwrite.ID]&(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) == 0 {
				return errors.New("destination grants access to a non-staff role")
			}
		} else {
			if overwrite.ID == botID && botID != "" {
				continue
			}
			member, err := b.liveMemberAuthorization(ctx, guild, overwrite.ID)
			if err != nil || !member.Present || member.PermissionBits&uint64(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) == 0 {
				return errors.New("destination grants access to a non-staff member")
			}
		}
	}
	if !private {
		return errors.New("destination must deny public access")
	}
	return b.validateStaffDeliveryPermissions(ctx, guild, channel, botID)
}

// authorizeEvidenceSource evaluates fresh Discord state, including private-thread membership,
// before the bot reads a message on behalf of a moderator.
func (b *Bot) authorizeEvidenceSource(ctx context.Context, ref quack.DiscordMessageReference) error {
	if b == nil || b.Session == nil {
		return quack.ErrAuthorizationUnavailable
	}
	channel, err := b.Session.Channel(ref.ChannelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || channel == nil || channel.GuildID != ref.GuildID {
		return quack.ErrEvidenceValidation
	}
	if ref.SystemCapture {
		return nil
	}
	if ref.ActorDiscordUserID == "" {
		return quack.ErrEvidenceValidation
	}
	permissionChannel := channel
	if channel.IsThread() {
		permissionChannel, err = b.Session.Channel(channel.ParentID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || permissionChannel == nil || permissionChannel.GuildID != ref.GuildID {
			return quack.ErrAuthorizationUnavailable
		}
	}
	guild, err := b.Session.Guild(ref.GuildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || guild == nil {
		return quack.ErrAuthorizationUnavailable
	}
	member, err := b.Session.GuildMember(ref.GuildID, ref.ActorDiscordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || member == nil {
		return quack.ErrAuthorizationUnavailable
	}
	// This isolated state contains only fresh REST results, never gateway cache entries.
	state := discordgo.NewState()
	guild.Channels = []*discordgo.Channel{permissionChannel}
	guild.Members = []*discordgo.Member{member}
	if err := state.GuildAdd(guild); err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	permissions, err := state.UserChannelPermissions(ref.ActorDiscordUserID, permissionChannel.ID)
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	if err != nil || permissions&required != required {
		return errors.New("moderator cannot read the evidence channel")
	}
	if channel.Type == discordgo.ChannelTypeGuildPrivateThread && permissions&discordgo.PermissionManageThreads == 0 {
		_, err := b.Session.ThreadMember(channel.ID, ref.ActorDiscordUserID, false, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return errors.New("moderator cannot read the evidence thread")
		}
	}
	return nil
}
