package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// SetupHandlers supplies optional-module configuration without coupling commands to their runtime.
type SetupHandlers struct{ Tickets, Honeypot, Logging ui.Handler }

// SetupCommandSpec exposes the bot's server configuration without a dashboard.
func SetupCommandSpec(moduleSetup ...SetupHandlers) CommandSpec {
	permissions := int64(discordgo.PermissionManageGuild)
	dm := false
	spec := CommandSpec{
		Definition: &discordgo.ApplicationCommand{
			Name:                     "setup",
			Description:              "Configure Quack for this server",
			DefaultMemberPermissions: &permissions,
			DMPermission:             &dm,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "appeals",
					Description: "Set up appeal reviews",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "channel",
							Description:  "Use an existing channel; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "rejoin",
							Description: "Discord invite for accepted appeals; use none to remove it",
							MaxLength:   256,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "tickets",
					Description: "Set up private support tickets",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "entry",
							Description:  "Use an existing entry channel; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "queue",
							Description:  "Use an existing queue; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "enabled",
							Description: "Enable or disable the saved setup; use this option on its own",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "honeypot",
					Description: "Create or update the honeypot trap",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "channel",
							Description:  "Use an existing channel; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "warning",
							Description: "Warning shown in the trap channel",
							MaxLength:   1500,
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "enabled",
							Description: "Enable or disable the saved setup; use this option on its own",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "logging",
					Description: "Set up Discord event logs",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "channel",
							Description:  "Use an existing channel; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
						{
							Type:        discordgo.ApplicationCommandOptionBoolean,
							Name:        "enabled",
							Description: "Enable or disable the saved setup; use this option on its own",
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "audit",
					Description: "Set up moderation history",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:         discordgo.ApplicationCommandOptionChannel,
							Name:         "channel",
							Description:  "Use an existing channel; otherwise Quack creates one",
							ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
						},
					},
				},
			},
		},
		Handler: handleSetup,
	}

	spec.Handler = func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction != nil && ctx.Interaction.Interaction != nil {
			options := ctx.Interaction.ApplicationCommandData().Options
			if len(options) == 1 && options[0].Name == "audit" {
				return handleAuditSetup(ctx)
			}

			if len(options) == 1 &&
				(options[0].Name == "tickets" ||
					options[0].Name == "honeypot" ||
					options[0].Name == "logging") {
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
	if ctx.Interaction == nil ||
		ctx.Interaction.Interaction == nil ||
		ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}

	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 || options[0].Name != "appeals" {
		return ui.Immediate(ui.Error("Choose which feature to set up."))
	}

	channelID := optionStringValue(options[0].GetOption("channel"))
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
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

		channelID, err = ui.SetupChannel(
			taskCtx, ctx.Session,
			ctx.Interaction.GuildID,
			channelID,
			settings.AppealQueueChannelDiscordID,
			"appeals",
			ui.SetupStaffChannel,
		)
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

		if option := options[0].GetOption("require-reason"); option != nil {
			value := option.BoolValue()
			input.AppealReviewReasonRequired = &value
		}

		saved, err := ctx.Services.Settings.Update(taskCtx, guild, input)
		if err != nil {
			_, err = responder.EditOriginal(
				ui.ErrorEdit(
					"Could not save appeal settings. Check Manage Server permission, Quack's queue channel permissions, and the HTTPS Discord invite (or none).",
				),
			)
			return err
		}

		confirmation := fmt.Sprintf(
			"Appeal reviews will go to <#%s>. Members can appeal from their case DM; moderators can accept or reject in this channel.",
			channelID,
		)

		if saved.AppealReviewReasonRequired {
			confirmation += " Moderators must write a decision reason; the member receives it."
		} else {
			confirmation += " Decision reasons are optional."
		}

		_, err = ui.Publish(responder, ui.Signal("settings", confirmation, true))
		return err
	})
}

// handleAuditSetup saves the core moderation destination after an immediate
// private acknowledgement, live manager authorization and channel validation.
func handleAuditSetup(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil ||
		ctx.Interaction.Interaction == nil ||
		ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}

	option := ctx.Interaction.ApplicationCommandData().GetOption("audit")
	if option == nil {
		return ui.Immediate(ui.Error("Choose the audit channel."))
	}

	channelID := optionStringValue(option.GetOption("channel"))
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
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

		channelID, err = ui.SetupChannel(
			taskCtx,
			ctx.Session,
			ctx.Interaction.GuildID,
			channelID,
			settings.AuditMirrorChannelDiscordID,
			"moderation-log",
			ui.SetupStaffChannel,
		)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(err.Error()))
			return err
		}

		_, err = ctx.Services.Settings.Update(
			taskCtx,
			guild,
			quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &channelID},
		)
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

		_, err = responder.EditOriginal(
			ui.EditMessage(
				ui.Signal("settings", fmt.Sprintf(
					"Moderation history will go to <#%s>: cases, action outcomes, appeals, tickets and settings changes.",
					channelID,
				), true)))
		return err
	})
}

// handleSetupToggle changes only module enablement through the canonical settings
// service. Omitting enabled remains the existing channel-creation setup journey.
func handleSetupToggle(ctx ui.Context, command *discordgo.ApplicationCommandInteractionDataOption) ui.HandlerResult {
	if ctx.Interaction == nil ||
		ctx.Interaction.Interaction == nil ||
		ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run setup in your server."))
	}

	if command == nil ||
		len(command.Options) != 1 ||
		command.Options[0] == nil ||
		command.Options[0].Name != "enabled" {
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
		input.TicketsEnabled = &enabled
		name = "Tickets"
	case "honeypot":
		input.HoneypotEnabled = &enabled
		name = "Honeypot"
	case "logging":
		input.GeneralLoggingEnabled = &enabled
		name = "Logging"
	default:
		return ui.Immediate(ui.Error("Choose tickets, honeypot, or logging."))
	}

	if ctx.Services == nil ||
		ctx.Services.Guilds == nil ||
		ctx.Services.Settings == nil {
		return ui.Immediate(ui.Error("This setup feature is unavailable."))
	}

	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
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
				text = fmt.Sprintf(
					"Could not enable %s with the saved setup. Check its channels and Quack's permissions, or run `/setup %s` without enabled to configure it.",
					command.Name,
					command.Name,
				)
			} else if errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
				text = "You need Manage Server permission to turn features on or off."
			}

			_, err = responder.EditOriginal(ui.ErrorEdit(text))
			return err
		}

		text := name + " turned on."
		if !enabled {
			text = fmt.Sprintf(
				"%s turned off. Your channels are saved. Turn it back on with `/setup %s enabled:true`.",
				name,
				command.Name,
			)
		}

		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", text, true)))
		return err
	})
}
