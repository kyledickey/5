package discordbot

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// ValidateStaffChannel checks guild ownership and Quack's delivery permissions.
// Administrators choose who can see an existing destination; setup and workers
// must not reject that choice based on viewer roles or public visibility.
// A missing permission is reported as a ui.UserError with administrator copy;
// transient Discord failures as quack.ErrAuthorizationUnavailable.
func (b *Bot) ValidateStaffChannel(ctx context.Context, guildID, channelID string) error {
	channel, err := b.Session.Channel(channelID, singleAttempt(ctx)...)
	if err != nil || channel == nil || channel.GuildID != guildID || channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New("destination must be a text channel in this guild")
	}
	guild, err := b.Session.Guild(guildID, singleAttempt(ctx)...)
	if err != nil || guild == nil {
		return quack.ErrAuthorizationUnavailable
	}
	botID := ""
	if b.Session.State != nil && b.Session.State.User != nil {
		botID = b.Session.State.User.ID
	}
	return b.validateStaffDeliveryPermissions(ctx, guild, channel, botID)
}

// authorizeEvidenceSource evaluates fresh Discord state, including private-thread membership,
// before the bot reads a message on behalf of a moderator.
func (b *Bot) authorizeEvidenceSource(ctx context.Context, ref quack.DiscordMessageReference) error {
	channel, err := b.Session.Channel(ref.ChannelID, singleAttempt(ctx)...)
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
		permissionChannel, err = b.Session.Channel(channel.ParentID, singleAttempt(ctx)...)
		if err != nil || permissionChannel == nil || permissionChannel.GuildID != ref.GuildID {
			return quack.ErrAuthorizationUnavailable
		}
	}
	guild, err := b.Session.Guild(ref.GuildID, singleAttempt(ctx)...)
	if err != nil || guild == nil {
		return quack.ErrAuthorizationUnavailable
	}
	member, err := b.Session.GuildMember(ref.GuildID, ref.ActorDiscordUserID, singleAttempt(ctx)...)
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
		if _, err := b.Session.ThreadMember(channel.ID, ref.ActorDiscordUserID, false, singleAttempt(ctx)...); err != nil {
			return errors.New("moderator cannot read the evidence thread")
		}
	}
	return nil
}
