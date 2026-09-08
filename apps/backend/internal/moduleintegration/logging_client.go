package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
)

// loggingDiscordClient sends already-redacted payloads only to channels whose
// current guild moderators can read.
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

// ValidateStaffOnlyChannel checks fresh ownership and delivery permissions, including
// attachment access needed to preserve log content beyond Discord message limits.
func (c loggingDiscordClient) ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	if err := (&discordadapter.Bot{Session: c.session}).ValidateStaffChannel(ctx, discordGuildID, channelID); err != nil {
		return err
	}
	channel, err := c.session.Channel(channelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return err
	}
	if channel == nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
		return errors.New("logging destination must be a text channel in this guild")
	}
	guild, member, err := currentBotMember(ctx, c.session, discordGuildID)
	if err != nil {
		return err
	}
	permissions := channelPermissions(guild, channel, member)
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles)
	if permissions&required != required {
		return errors.New("Quack needs View Channel, Send Messages and Attach Files in the logging channel")
	}
	return nil
}

var _ generallogging.DeliveryClient = loggingDiscordClient{}
