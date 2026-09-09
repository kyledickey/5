package commands

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// UserCaseCommandSpec exposes the native member context-menu entry point.
func UserCaseCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Type: discordgo.UserApplicationCommand, Name: "Add case for member", DefaultMemberPermissions: &permissions, DMPermission: &dm}, Handler: HandleUserCaseInteraction}
}

// HandleUserCaseInteraction selects a policy for the member chosen in Discord.
// No message evidence is fabricated; staff can attach context after creation.
func HandleUserCaseInteraction(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Select a server member to create a case."))
	}
	data := ctx.Interaction.ApplicationCommandData()
	if data.TargetID == "" || data.Resolved == nil || data.Resolved.Users[data.TargetID] == nil {
		return ui.Immediate(ui.Error("The selected member is unavailable."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err == nil {
			err = ctx.Services.Guilds.Authorize(taskCtx, guild, model.PermissionActionCaseCreate, model.AuditSourceDiscord)
		}
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		templates, err := ctx.Services.Templates.ListActive(taskCtx, guild)
		if err != nil || len(templates) == 0 {
			_, err = responder.EditOriginal(ui.ErrorEdit("No active case template is available."))
			return err
		}
		if len(templates) > 1 {
			_, err = responder.EditOriginal(ui.EditMessage(caseTemplatePicker(templates, "u", data.TargetID, 0)))
			return err
		}
		template := &templates[0]
		created, err := ctx.Services.Cases.Create(taskCtx, guild, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: data.TargetID, Source: model.CaseSourceDiscord, IdempotencyKey: ctx.Interaction.ID})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		return publishPrivateContextCase(taskCtx, responder, ctx.Services, created, template)
	})
}

// handleUserTemplateComponent refreshes moderator authority when a policy is selected.
func handleUserTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)
	if err != nil || parsed.Payload == "" || len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That case selection is unavailable."))
	}
	return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		if err := ctx.Services.Guilds.Authorize(taskCtx, guild, model.PermissionActionCaseCreate, model.AuditSourceDiscord); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		_, template, err := resolveTemplate(taskCtx, ctx.Services, guild, data.Values[0])
		if err != nil || template == nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("That case template is not available."))
			return err
		}
		created, err := ctx.Services.Cases.Create(taskCtx, guild, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: parsed.Payload, Source: model.CaseSourceDiscord, IdempotencyKey: caseSelectionKey(ctx.Interaction)})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return err
		}
		return publishPrivateContextCase(taskCtx, responder, ctx.Services, created, template)
	})
}
