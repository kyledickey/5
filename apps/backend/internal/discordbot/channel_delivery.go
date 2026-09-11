package discordbot

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// staffChannelPermissionCopy is what an administrator sees when Quack lacks a
// permission in a staff destination. It is returned as a ui.UserError so
// callers that display err.Error() keep showing exactly this sentence.
const staffChannelPermissionCopy = "Quack needs View Channel, Send Messages, Read Message History and Attach Files in the staff channel"

// validateStaffDeliveryPermissions evaluates current bot membership and channel
// overwrites using isolated REST state. Attach Files supports full-length records;
// Read Message History supports queue refresh and transcript workflows.
func (b *Bot) validateStaffDeliveryPermissions(
	ctx context.Context,
	guild *discordgo.Guild,
	channel *discordgo.Channel,
	botID string,
) error {
	if botID == "" {
		user, err := b.Session.User("@me", singleAttempt(ctx)...)
		if err != nil || user == nil || user.ID == "" {
			return quack.ErrAuthorizationUnavailable
		}
		botID = user.ID
	}
	member, err := b.Session.GuildMember(guild.ID, botID, singleAttempt(ctx)...)
	if err != nil || member == nil || member.User == nil || member.User.ID != botID {
		return quack.ErrAuthorizationUnavailable
	}
	current := *guild
	current.Channels = []*discordgo.Channel{channel}
	current.Members = []*discordgo.Member{member}
	state := discordgo.NewState()
	if err := state.GuildAdd(&current); err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	permissions, err := state.UserChannelPermissions(botID, channel.ID)
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages |
		discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles)
	if err != nil || permissions&required != required {
		return &ui.UserError{Message: staffChannelPermissionCopy}
	}
	return nil
}
