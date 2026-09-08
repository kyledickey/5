package commands

import (
	"context"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// SetupHandlers supplies optional-module configuration without coupling commands to their runtime.
type SetupHandlers struct{ Tickets, Honeypot, Logging ui.Handler }

// SetupCommandSpec exposes the bot's server configuration without a dashboard.
func SetupCommandSpec(moduleSetup ...SetupHandlers) CommandSpec {
	permissions := int64(discordgo.PermissionManageServer)
	dm := false
	spec := CommandSpec{Definition: &discordgo.ApplicationCommand{Name: "setup", Description: "Configure Quack for this server", DefaultMemberPermissions: &permissions, DMPermission: &dm, Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "appeals", Description: "Choose the private channel for appeal reviews", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Private text channel for the appeal queue", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}}}}}, Handler: handleSetup}
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionSubCommand, Name: "tickets", Description: "Set up private support tickets",
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "entry", Description: "Text channel for the Open ticket button", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}},
			{Type: discordgo.ApplicationCommandOptionChannel, Name: "queue", Description: "Private text channel for staff ticket notifications", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}},
		},
	})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "honeypot", Description: "Create or update the honeypot trap", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionString, Name: "warning", Description: "Warning shown in the trap channel", MaxLength: 1500}}})
	spec.Definition.Options = append(spec.Definition.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "logging", Description: "Send Discord event logs to one private channel", Options: []*discordgo.ApplicationCommandOption{{Type: discordgo.ApplicationCommandOptionChannel, Name: "channel", Description: "Private staff channel for Discord event logs", Required: true, ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText}}}})
	spec.Handler = func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction != nil && ctx.Interaction.Interaction != nil {
			options := ctx.Interaction.ApplicationCommandData().Options
			if len(options) == 1 && (options[0].Name == "tickets" || options[0].Name == "honeypot" || options[0].Name == "logging") {
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

// handleSetup refreshes live administrator authority and validates channel privacy
// before saving. Workers read the new setting without restarting the bot.
func handleSetup(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 || options[0].Name != "appeals" {
		return ui.Immediate(ui.Error("Choose which feature to set up."))
	}
	channelID := optionStringValue(options[0].GetOption("channel"))
	if channelID == "" {
		return ui.Immediate(ui.Error("Choose a private text channel for appeal reviews."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not verify your server permissions."))
			return err
		}
		_, err = ctx.Services.Settings.Update(taskCtx, guild, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &channelID})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not save the appeal queue. You need Manage Server permission, and the channel must be private, in this server, and accessible to Quack."))
			return err
		}
		_, err = ui.Publish(responder, ui.Signal("settings", fmt.Sprintf("Appeal reviews will go to <#%s>. Members can appeal from their case DM; moderators can accept or reject in this channel.", channelID), true))
		return err
	})
}
