package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// ticketPermission describes a Discord capability in administrator-facing language.
type ticketPermission struct {
	bit  int64
	name string
}

// validateTicketBotPermissions evaluates fresh REST state rather than gateway
// caches. Setup requires the capabilities used by opening, closure and transcripts.
func (c ticketDiscordClient) validateTicketBotPermissions(ctx context.Context, guildID, entryID, queueID string) error {
	botID, err := c.botUserID(ctx)
	if err != nil {
		return err
	}
	guild, err := c.session.Guild(guildID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || guild == nil {
		return errors.New("could not check Quack's current server permissions")
	}
	member, err := c.session.GuildMember(guildID, botID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || member == nil {
		return errors.New("could not check Quack's current server membership")
	}
	common := []ticketPermission{{discordgo.PermissionViewChannel, "View Channel"}, {discordgo.PermissionSendMessages, "Send Messages"}, {discordgo.PermissionReadMessageHistory, "Read Message History"}}
	for _, destination := range []struct {
		id       string
		required []ticketPermission
	}{
		{entryID, append(append([]ticketPermission{}, common...), ticketPermission{discordgo.PermissionCreatePrivateThreads, "Create Private Threads"}, ticketPermission{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"}, ticketPermission{discordgo.PermissionManageThreads, "Manage Threads"})},
		{queueID, append(append([]ticketPermission{}, common...), ticketPermission{discordgo.PermissionAttachFiles, "Attach Files"})},
	} {
		channel, err := c.session.Channel(destination.id, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || channel == nil || channel.GuildID != guildID || channel.Type != discordgo.ChannelTypeGuildText {
			return errors.New("ticket destinations must be text channels in this server")
		}
		snapshot := *guild
		snapshot.Channels = []*discordgo.Channel{channel}
		snapshot.Members = []*discordgo.Member{member}
		state := discordgo.NewState()
		if err := state.GuildAdd(&snapshot); err != nil {
			return err
		}
		permissions, err := state.UserChannelPermissions(botID, channel.ID)
		if err != nil {
			return errors.New("could not calculate Quack's channel permissions")
		}
		var missing []string
		for _, required := range destination.required {
			if permissions&required.bit == 0 {
				missing = append(missing, required.name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("Quack needs %s in <#%s>. Update the channel permissions and run setup again", strings.Join(missing, ", "), channel.ID)
		}
	}
	return nil
}
