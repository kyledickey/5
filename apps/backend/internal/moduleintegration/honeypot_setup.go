package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// SetupHoneypot creates a trap with an editable template and a visible warning.
// Existing channels and selected policies are reused without replacing admin edits.
func (r *Runtime) SetupHoneypot(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Run honeypot setup in your server."))
	}
	warning, specified := "", ""
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) == 1 {
		if option := options[0].GetOption("channel"); option != nil {
			specified, _ = option.Value.(string)
		}
		if option := options[0].GetOption("warning"); option != nil {
			warning, _ = option.Value.(string)
		}
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		fail := func(message string) error { _, err := responder.EditOriginal(ui.ErrorEdit(message)); return err }
		taskCtx = quack.ContextWithAuditSource(taskCtx, model.AuditSourceDiscord)
		guild, err := r.services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: interactionUserID(ctx.Interaction)})
		if err != nil || guild == nil || !guild.Can(model.PermissionActionGuildSettingsWrite) {
			return fail("You need Manage Server permission to set up the honeypot.")
		}
		release, err := lockHoneypotWarning(taskCtx, &r.honeypotWarningLocks, guild.Guild.ID)
		if err != nil {
			return fail("Honeypot setup timed out. Try again.")
		}
		defer release()
		actor := honeypot.Actor{GuildID: guild.Guild.ID, DiscordUserID: interactionUserID(ctx.Interaction), CanManage: true}
		settings, status, err := r.Honeypot.Settings(taskCtx, actor)
		if err != nil {
			return fail("Could not load honeypot settings. Try again.")
		}
		if settings.TemplateID == "" {
			template, err := r.services.Templates.EnsureHoneypotTemplate(taskCtx, guild)
			if err != nil {
				return fail("Could not create the honeypot template. If it is archived, restore it first.")
			}
			settings.TemplateID = template.ID
		}
		if err := (honeypotTemplateValidator{repository: r.repository}).ValidateHoneypotTemplate(taskCtx, actor.GuildID, settings.TemplateID); err != nil {
			return fail("The selected honeypot template is unavailable. Restore or repair it before setup.")
		}
		channelID, err := ui.SetupChannel(taskCtx, r.session, ctx.Interaction.GuildID, specified, settings.ChannelDiscordID, "honeypot", ui.SetupHoneypotChannel)
		if err != nil {
			return fail(err.Error())
		}
		channel, err := r.session.Channel(channelID, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || channel == nil {
			return fail("Could not access the honeypot channel. Check Quack's permissions.")
		}
		if settings.ChannelDiscordID != channelID {
			settings.ChannelDiscordID, settings.WarningMessageID = channelID, ""
			if _, _, err = r.Honeypot.UpdateSettings(taskCtx, actor, false, settings); err != nil {
				return fail("Could not save the honeypot channel. Specify it when you retry setup.")
			}
		}
		if channel.GuildID != ctx.Interaction.GuildID || channel.Type != discordgo.ChannelTypeGuildText {
			return fail("The honeypot must be a text channel in this server.")
		}
		if err := (honeypotChannelValidator{session: r.session, resolver: r.resolver}).ValidateHoneypotChannel(taskCtx, actor.GuildID, channel.ID); err != nil {
			return fail("Quack needs View Channel, Send Messages, Read Message History and Manage Messages in the honeypot channel. Update its permissions and run setup again.")
		}
		if strings.TrimSpace(warning) != "" {
			settings.WarningText = strings.ReplaceAll(warning, `\n`, "\n")
		}
		if settings.WarningText == "" {
			settings.WarningText = defaultHoneypotWarning
		}
		content := honeypotWarningContent(settings.WarningText, status.Statistics.Created)
		var sent *discordgo.Message
		if settings.WarningMessageID != "" {
			sent, err = r.session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: settings.WarningMessageID, Channel: channel.ID, Content: &content, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
			var rest *discordgo.RESTError
			if err != nil && !(errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownMessage) {
				return fail("Could not update the honeypot warning. Check Quack's channel permissions.")
			}
		}
		if sent == nil {
			sent, err = r.session.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{Content: content, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(taskCtx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		}
		if err != nil || sent == nil {
			return fail("Could not post the honeypot warning. Check Send Messages permission and run setup again.")
		}
		settings.WarningMessageID = sent.ID
		if _, _, err = r.Honeypot.UpdateSettings(taskCtx, actor, true, settings); err != nil {
			return fail("The warning is posted, but the honeypot could not be enabled. Check the channel and template, then run setup again.")
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", fmt.Sprintf("Honeypot ready in <#%s>. You can rename the channel and edit the selected template's punishment. Moderators and bots are exempt.", channel.ID), true)))
		return err
	})
}
