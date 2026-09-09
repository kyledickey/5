package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TemplateCommandSpec exposes policy authoring separately from issuing cases.
func TemplateCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionManageGuild)
	dm := false
	return CommandSpec{Definition: &discordgo.ApplicationCommand{Name: "template", Description: "Create and manage moderation rules", DefaultMemberPermissions: &permissions, DMPermission: &dm, Options: append(templateManagementOptions(), []*discordgo.ApplicationCommandOption{
		templateLevelOption(),
		{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "create", Description: "Create a rule with a default outcome", Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "outcome", Description: "Default outcome; warning if omitted", Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Warning", Value: "warning"}, {Name: "Timeout", Value: "timeout"}, {Name: "Kick", Value: "kick"}, {Name: "Ban", Value: "ban"}}},
			{Type: discordgo.ApplicationCommandOptionInteger, Name: "minutes", Description: "Timeout length in minutes (required for a timeout)", MinValue: floatPointer(1), MaxValue: 40320},
		}},
	}...)}, Handler: handleTemplateCommand}
}

// floatPointer supplies Discord's optional numeric lower bound.
func floatPointer(value float64) *float64 { return &value }

// handleTemplateCommand opens the short form without waiting for network lookups.
// Submission refreshes manager authority before creating any policy.
func handleTemplateCommand(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Create templates in your server."))
	}
	if ctx.Interaction.Type == discordgo.InteractionApplicationCommandAutocomplete {
		return ui.Immediate(templatePolicyAutocomplete(ctx))
	}
	if level := ctx.Interaction.ApplicationCommandData().GetOption("level"); level != nil {
		return handleTemplateLevel(ctx, level)
	}
	for _, option := range ctx.Interaction.ApplicationCommandData().Options {
		switch option.Name {
		case "view", "edit", "remove-level", "archive", "restore":
			return handleTemplateManage(ctx, option)
		}
	}
	create := ctx.Interaction.ApplicationCommandData().GetOption("create")
	if create == nil {
		return ui.Immediate(ui.Error("Choose a template operation."))
	}
	outcome := "warning"
	if option := create.GetOption("outcome"); option != nil {
		outcome = option.StringValue()
	}
	minutes := int64(0)
	if option := create.GetOption("minutes"); option != nil {
		minutes = option.IntValue()
	}
	if !validTemplateOutcome(outcome, minutes) {
		return ui.Immediate(ui.Error("For a timeout, set minutes between 1 and 40320. Other outcomes do not use minutes."))
	}
	id := ui.MustCustomID(ui.CustomID{Namespace: "template", Action: "create", Version: "v1", Payload: fmt.Sprintf("%s|%d", outcome, minutes)})
	return ui.Immediate(ui.Modal("Create a rule", id, []discordgo.MessageComponent{
		ui.Row(discordgo.TextInput{CustomID: "name", Label: "Rule name", Placeholder: "NSFW chatting", Style: discordgo.TextInputShort, Required: true, MaxLength: 100}),
		ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Reason shown to the member", Placeholder: "Keep explicit content out of chat.", Style: discordgo.TextInputParagraph, Required: true, MaxLength: 1000}),
	}))
}

// validTemplateOutcome rejects forged or incompatible option combinations.
func validTemplateOutcome(outcome string, minutes int64) bool {
	switch outcome {
	case "timeout":
		return minutes >= 1 && minutes <= 40320
	case "warning", "kick", "ban":
		return minutes == 0
	default:
		return false
	}
}

// RegisterTemplateComponents installs policy forms in the shared dispatcher.
func RegisterTemplateComponents(registry *interactions.ComponentRegistry) error {
	return registry.RegisterModal("template", "create", handleTemplateCreateSubmit)
}

// handleTemplateCreateSubmit activates one default policy through the shared
// validation/audit boundary. Moderators may immediately use it with /case add.
func handleTemplateCreateSubmit(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Create templates in your server."))
	}
	data := ctx.Interaction.ModalSubmitData()
	id, err := ui.DecodeCustomID(data.CustomID)
	parts := strings.Split(id.Payload, "|")
	if err != nil || len(parts) != 2 {
		return ui.Immediate(ui.Error("That template form is unavailable."))
	}
	minutes, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || !validTemplateOutcome(parts[0], minutes) {
		return ui.Immediate(ui.Error("That template outcome is invalid."))
	}
	name, reason := strings.TrimSpace(modalTextValue(data, "name")), strings.TrimSpace(modalTextValue(data, "reason"))
	if name == "" || reason == "" {
		return ui.Immediate(ui.Error("Enter a rule name and a reason."))
	}
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
			_, err = responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to create templates."))
			return err
		}
		level := quack.TemplateLevelInput{Name: "Default", Position: 1, IsDefault: true, NotifyUser: true}
		action := model.ActionType("")
		switch parts[0] {
		case "timeout":
			action = model.ActionTimeoutUser
		case "kick":
			action = model.ActionKickUser
		case "ban":
			action = model.ActionBanUser
		}
		if action != "" {
			level.Actions = []quack.TemplateActionInput{{ActionType: action, TimeoutDurationSeconds: int(minutes * 60)}}
		}
		created, err := ctx.Services.Templates.Create(taskCtx, guild, quack.TemplateInput{Slug: "rule-" + ctx.Interaction.ID, Name: name, ReasonTemplate: reason, Appealable: true, Levels: []quack.TemplateLevelInput{level}})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not create the template. Check the form and try again."))
			return err
		}
		outcome := parts[0]
		if outcome == "timeout" {
			outcome = fmt.Sprintf("%d-minute timeout", minutes)
		}
		message := ui.Signal("settings", fmt.Sprintf("**%s** is ready. First case: **%s**.\nUse `/case add` when someone breaks this rule.", ui.PlainText(created.Name), outcome), true)
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}
