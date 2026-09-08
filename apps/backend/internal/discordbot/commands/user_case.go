package commands

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// UserCaseCommandSpec exposes the native member context-menu entry point.
func UserCaseCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Type: discordgo.UserApplicationCommand, Name: "Create case for member", DefaultMemberPermissions: &permissions, DMPermission: &dm}, Handler: HandleUserCaseInteraction}
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
	guild, err := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}
	templates, err := ctx.Services.Templates.ListActive(ctx.Context, guild)
	if err != nil || len(templates) == 0 {
		return ui.Immediate(ui.Error("No active case template is available."))
	}
	if len(templates) == 1 {
		return createUserContextCase(ctx, guild, data.TargetID, &templates[0])
	}
	return ui.Immediate(ui.Ephemeral(caseTemplatePicker(templates, "u", data.TargetID, 0)))
}

// handleUserTemplateComponent refreshes moderator authority when a policy is selected.
func handleUserTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)
	if err != nil || parsed.Payload == "" || len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That case selection is unavailable."))
	}
	guild, err := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}
	_, template, err := resolveTemplate(ctx.Context, ctx.Services, guild, data.Values[0])
	if err != nil || template == nil {
		return ui.Immediate(ui.Error("That case template is not available."))
	}
	return createUserContextCase(ctx, guild, parsed.Payload, template)
}

// createUserContextCase applies the selected policy immediately through the normal
// case boundary and keeps the result's action statuses current.
func createUserContextCase(ctx ui.Context, guild *quack.GuildStaffContext, target string, template *quack.TemplateResponse) ui.HandlerResult {
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		created, err := ctx.Services.Cases.Create(taskCtx, guild, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: target, Source: model.CaseSourceDiscord, IdempotencyKey: ctx.Interaction.ID})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return err
		}
		message, err := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: template}))
		if err == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, created, message.ID, template)
		}
		return err
	})
}
