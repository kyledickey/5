package commands

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"strings"
)

// SetupHandlers supplies optional-module configuration without coupling commands to their runtime.
type SetupHandlers struct{ Tickets, Honeypot, Logging ui.Handler }

// SetupCommandSpec exposes the bot's server configuration without a dashboard.
func SetupCommandSpec(moduleSetup ...SetupHandlers) CommandSpec {
	permissions := int64(discordgo.PermissionManageServer)
	dm := false
	spec := CommandSpec{Definition: &discordgo.ApplicationCommand{Name: "setup", Description: "Configure Quack for this server", DefaultMemberPermissions: &permissions, DMPermission: &dm, Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "appeals", Description: "Set up appeal reviews", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Use an existing channel; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}}}}}, Handler: handleSetup}
	spec.Definition.Options[0].Options = append(spec.Definition.Options[0].Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: "rejoin", Description: "Discord invite for accepted appeals; use none to remove it", MaxLength: 256})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionSubCommand, Name: "tickets", Description: "Set up private support tickets",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "entry", Description: "Use an existing entry channel; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}},
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "queue", Description: "Use an existing queue; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}},
		},
	})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "honeypot", Description: "Create or update the honeypot trap", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Use an existing channel; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}, {Type: discordgo.ApplicationCommandOptionString, Name: "warning", Description: "Warning shown in the trap channel", MaxLength: 1500}}})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "logging", Description: "Set up Discord event logs", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Use an existing channel; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}}})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionSubCommand, Name: "audit", Description: "Set up moderation history",
		Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Use an existing channel; otherwise Quack creates one", ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}},
	})
	for _, command := range spec.Definition.Options {
		if command.Name == "tickets" || command.Name == "honeypot" || command.Name == "logging" {
			command.Options = append(command.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: "enabled", Description: "Enable or disable the saved setup; use this option on its own"})
		}
	}
	spec.Handler = func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction != nil && ctx.Interaction.Interaction != nil {
			options := ctx.Interaction.ApplicationCommandData().Options
			if len(options) == 1 && options[0].Name == "audit" {
				return handleAuditSetup(ctx)
			}
			if len(options) == 1 && (options[0].Name == "tickets" || options[0].Name == "honeypot" || options[0].Name == "logging") {
				if enabled := options[0].GetOption("enabled"); enabled != nil {
					return handleSetupToggle(ctx, options[0])
				}
				var handler ui.Handler
				if len(moduleSetup) > 0 {
					if options[0].Name == "tickets" {
						handler = moduleSetup[0].Tickets
					} else if options[0].Name == "honeypot" {
						handler = moduleSetup[0].Honeypot
					} else {
						handler = moduleSetup[0].Logging
					}
				}
				if handler != nil {
					return handler(ctx)
				}
				return ui.Immediate(ui.Error("This setup feature is unavailable."))
			}
		}
		return handleSetup(ctx)
	}
	return spec
}

// handleSetup refreshes live administrator authority before creating or choosing
// a destination. Workers read the new setting without restarting the bot.
func handleSetup(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 || options[0].Name != "appeals" {
		return ui.Immediate(ui.Error("Choose which feature to set up."))
	}
	channelID := optionStringValue(options[0].GetOption("channel"))
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not verify your server permissions."))
			return err
		}
		if guild == nil || !guild.Can(model.PermissionActionGuildSettingsWrite) {
			_, err = responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to set up appeals."))
			return err
		}
		settings, err := ctx.Services.Settings.Get(taskCtx, guild)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load appeal settings. Try again."))
			return err
		}
		channelID, err = ui.SetupChannel(taskCtx, ctx.Session, ctx.Interaction.GuildID, channelID, settings.AppealQueueChannelDiscordID, "appeals", ui.SetupStaffChannel)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}
		input := quack.GuildSettingsInput{AppealQueueChannelDiscordID: &channelID}
		if option := options[0].GetOption("rejoin"); option != nil {
			value := strings.TrimSpace(option.StringValue())
			if strings.EqualFold(value, "none") {
				value = ""
			}
			input.AppealRejoinURL = &value
		}
		_, err = ctx.Services.Settings.Update(taskCtx, guild, input)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not save appeal settings. Check Manage Server permission, Quack's queue channel permissions, and the HTTPS Discord invite (or none)."))
			return err
		}
		_, err = ui.Publish(responder, ui.Signal("settings", fmt.Sprintf("Appeal reviews will go to <#%s>. Members can appeal from their case DM; moderators can accept or reject in this channel.", channelID), true))
		return err
	})
}
