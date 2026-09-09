package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleSetupToggle changes only module enablement through the canonical settings
// service. Omitting enabled remains the existing channel-creation setup journey.
func handleSetupToggle(ctx ui.Context, command *discordgo.ApplicationCommandInteractionDataOption) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}
	if command == nil || len(command.Options) != 1 || command.Options[0] == nil || command.Options[0].Name != "enabled" {
		return ui.Immediate(ui.Error("Use enabled on its own. To change channels or the warning, run setup separately without enabled."))
	}
	enabled, ok := command.Options[0].Value.(bool)
	if !ok || command.Options[0].Type != discordgo.ApplicationCommandOptionBoolean {
		return ui.Immediate(ui.Error("Choose true or false for enabled."))
	}
	input := quack.GuildSettingsInput{}
	name := ""
	switch command.Name {
	case "tickets":
		input.TicketsEnabled, name = &enabled, "Tickets"
	case "honeypot":
		input.HoneypotEnabled, name = &enabled, "Honeypot"
	case "logging":
		input.GeneralLoggingEnabled, name = &enabled, "Logging"
	default:
		return ui.Immediate(ui.Error("Choose tickets, honeypot, or logging."))
	}
	if ctx.Services == nil || ctx.Services.Guilds == nil || ctx.Services.Settings == nil {
		return ui.Immediate(ui.Error("This setup feature is unavailable."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not verify your server permissions. Try again."))
			return err
		}
		if guild == nil || !guild.Can(model.PermissionActionGuildSettingsWrite) {
			_, err = responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to turn features on or off."))
			return err
		}
		_, err = ctx.Services.Settings.Update(taskCtx, guild, input)
		if err != nil {
			text := "Could not save this setting. Try again."
			if errors.Is(err, quack.ErrGuildSettingsValidation) {
				text = fmt.Sprintf("Could not enable %s with the saved setup. Check its channels and Quack's permissions, or run `/setup %s` without enabled to configure it.", command.Name, command.Name)
			} else if errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
				text = "You need Manage Server permission to turn features on or off."
			}
			_, err = responder.EditOriginal(ui.ErrorEdit(text))
			return err
		}
		text := name + " enabled using the saved setup."
		if !enabled {
			text = fmt.Sprintf("%s disabled. Existing setup is kept. Re-enable with `/setup %s enabled:true`.", name, command.Name)
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", text, true)))
		return err
	})
}
