package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleAuditSetup saves the core moderation destination after an immediate
// private acknowledgement, live manager authorization and channel validation.
func handleAuditSetup(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}
	option := ctx.Interaction.ApplicationCommandData().GetOption("audit")
	if option == nil {
		return ui.Immediate(ui.Error("Choose the audit channel."))
	}
	channelID := optionStringValue(option.GetOption("channel"))
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not verify your current server permissions."))
			return err
		}
		if guild == nil || !guild.Can(model.PermissionActionGuildSettingsWrite) {
			_, err = responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to change the audit channel."))
			return err
		}
		settings, err := ctx.Services.Settings.Get(taskCtx, guild)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load audit settings. Try again."))
			return err
		}
		channelID, err = ui.SetupChannel(taskCtx, ctx.Session, ctx.Interaction.GuildID, channelID, settings.AuditMirrorChannelDiscordID, "moderation-log", ui.SetupStaffChannel)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		_, err = ctx.Services.Settings.Update(taskCtx, guild, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &channelID})
		if err != nil {
			text := "Could not save the audit channel. Try again."
			switch {
			case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
				text = "You need Manage Server permission to change the audit channel."
			case errors.Is(err, quack.ErrGuildSettingsValidation):
				text = "Choose a text channel in this server where Quack can view, send, read history and attach files."
			}
			_, err = responder.EditOriginal(ui.ErrorEdit(text))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", fmt.Sprintf("Moderation history will go to <#%s>: cases, action outcomes, appeals, tickets and settings changes.", channelID), true)))
		return err
	})
}
