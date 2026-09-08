package moduleintegration

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// SetupLogging enables the supported Discord event categories in one staff channel.
// The logging service validates live destination privacy before saving its routes.
func (r *Runtime) SetupLogging(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run logging setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 || options[0].GetOption("channel") == nil {
		return ui.Immediate(ui.Error("Choose a private staff channel for Discord logs."))
	}
	channelID, ok := options[0].GetOption("channel").Value.(string)
	if !ok || channelID == "" {
		return ui.Immediate(ui.Error("Choose a private staff channel for Discord logs."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, identity tickets.Actor) error {
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
		if _, err := r.Logging.UpdateSettings(taskCtx, actor, true, settings.RouteAllTo(channelID)); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not enable logging. Choose a private staff text channel where Quack can View Channel, Send Messages and Attach Files."))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", fmt.Sprintf("Discord event logs will go to <#%s>. Message edits and deletions will include available content and attachment details.", channelID), true)))
		return err
	})
}
