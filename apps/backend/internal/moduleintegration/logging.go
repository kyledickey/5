package moduleintegration

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	discordadapter "github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// loggingDiscordClient sends already-redacted payloads only to channels that the
// guild's current moderators can read.
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

// SetupLogging enables the supported Discord event categories in one staff channel.
// The logging service validates live bot permissions before saving its routes.
func (r *Runtime) SetupLogging(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run logging setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 {
		return ui.Immediate(ui.Error("Choose which feature to set up."))
	}
	channelID := ""
	if option := options[0].GetOption("channel"); option != nil {
		channelID, _ = option.Value.(string)
	}
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		taskCtx = quack.ContextWithAuditSource(taskCtx, model.AuditSourceDiscord)
		current := ctx
		current.Context = taskCtx
		identity, err := r.ticketActor(current)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your permissions. Try again."))
			return err
		}
		if !identity.CanManage {
			_, err := responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to set up logging."))
			return err
		}
		guild, member, permissionErr := currentBotMember(taskCtx, r.session, ctx.Interaction.GuildID)
		if permissionErr != nil || channelPermissions(guild, &discordgo.Channel{GuildID: ctx.Interaction.GuildID}, member)&discordgo.PermissionViewAuditLogs == 0 {
			_, err := responder.EditOriginal(ui.ErrorEdit("Quack needs View Audit Log permission to log bans by other moderators without duplicating its own actions."))
			return err
		}
		actor := generallogging.Actor{GuildID: identity.GuildID, DiscordUserID: identity.DiscordUserID, CanManage: true}
		settings, _, _, err := r.Logging.Settings(taskCtx, actor)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load logging settings. Try again."))
			return err
		}
		channelID, err = ui.SetupChannel(taskCtx, r.session, ctx.Interaction.GuildID, channelID, settings.Channels[generallogging.MessageEdit], "discord-log", ui.SetupStaffChannel)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		if _, err := r.Logging.UpdateSettings(taskCtx, actor, true, settings.RouteAllTo(channelID)); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not enable logging. Choose a text channel where Quack can View Channel, Send Messages and Attach Files."))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", fmt.Sprintf("Discord logs will go to <#%s>.", channelID), true)))
		return err
	})
}
