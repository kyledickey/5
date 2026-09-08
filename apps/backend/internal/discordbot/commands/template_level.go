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

// templateLevelOption expresses thresholds as the case being created, rather
// than requiring administrators to calculate the engine's prior-case count.
func templateLevelOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "level", Description: "Set an outcome from a chosen case number onward", Options: []*discordgo.ApplicationCommandOption{
		{Type: discordgo.ApplicationCommandOptionString, Name: "template", Description: "Rule to edit", Required: true, Autocomplete: true},
		{Type: discordgo.ApplicationCommandOptionInteger, Name: "case", Description: "Start on this case: 1 for default, 3 for the third case", Required: true, MinValue: floatPointer(1), MaxValue: 1000000},
		{Type: discordgo.ApplicationCommandOptionString, Name: "outcome", Description: "Outcome at this level", Required: true, Choices: []*discordgo.ApplicationCommandOptionChoice{{Name: "Warning", Value: "warning"}, {Name: "Timeout", Value: "timeout"}, {Name: "Kick", Value: "kick"}, {Name: "Ban", Value: "ban"}}},
		{Type: discordgo.ApplicationCommandOptionInteger, Name: "minutes", Description: "Timeout length in minutes", MinValue: floatPointer(1), MaxValue: 40320},
		{Type: discordgo.ApplicationCommandOptionBoolean, Name: "notify", Description: "Send the member a DM at this level; defaults to on for new levels"},
	}}
}

// templatePolicyAutocomplete lists currently active rules for authorized managers.
func templatePolicyAutocomplete(ctx ui.Context) *discordgo.InteractionResponse {
	guild, err := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
		return ui.Autocomplete(nil)
	}
	options := ctx.Interaction.ApplicationCommandData().Options
	if len(options) != 1 {
		return ui.Autocomplete(nil)
	}
	level := options[0]
	if level == nil || level.GetOption("template") == nil {
		return ui.Autocomplete(nil)
	}
	query := strings.ToLower(level.GetOption("template").StringValue())
	templates, err := ctx.Services.Templates.List(ctx.Context, guild)
	if err != nil {
		return ui.Autocomplete(nil)
	}
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	for _, template := range templates {
		if level.Name == "restore" && template.ArchivedAt == nil {
			continue
		}
		if level.Name != "restore" && level.Name != "view" && level.Name != "edit" && template.ArchivedAt != nil {
			continue
		}
		if strings.Contains(strings.ToLower(template.Name+" "+template.Slug), query) {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: templateAutocompleteLabel(template), Value: template.ID})
			if len(choices) == 25 {
				break
			}
		}
	}
	return ui.Autocomplete(choices)
}

// handleTemplateLevel updates one threshold while retaining unrelated levels and
// policy fields. The engine uses immutable snapshots for already-created cases.
func handleTemplateLevel(ctx ui.Context, option *discordgo.ApplicationCommandInteractionDataOption) ui.HandlerResult {
	ref, countOption, outcomeOption := option.GetOption("template"), option.GetOption("case"), option.GetOption("outcome")
	if ref == nil || countOption == nil || outcomeOption == nil {
		return ui.Immediate(ui.Error("Choose a template, case number and outcome."))
	}
	count, outcome := countOption.IntValue(), outcomeOption.StringValue()
	minutes := int64(0)
	if value := option.GetOption("minutes"); value != nil {
		minutes = value.IntValue()
	}
	if count < 1 || count > 1000000 || !validTemplateOutcome(outcome, minutes) {
		return ui.Immediate(ui.Error("Check the case number and timeout minutes."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		fail := func(text string) error { _, err := responder.EditOriginal(ui.ErrorEdit(text)); return err }
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
			return fail("You need Manage Server permission to edit templates.")
		}
		_, template, err := resolveTemplate(taskCtx, ctx.Services, guild, ref.StringValue())
		if err != nil || template == nil {
			return fail("That active template is unavailable.")
		}
		policy := template.EditInput()
		level := quack.TemplateLevelInput{Name: fmt.Sprintf("Case %d onward", count), TriggerCaseCount: int(count - 1), NotifyUser: true}
		if count == 1 {
			level.IsDefault = true
			level.Name = "Default"
		}
		action := model.ActionType("")
		switch outcome {
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
		replaced := false
		for index, existing := range policy.Levels {
			if existing.IsDefault == level.IsDefault && (level.IsDefault || existing.TriggerCaseCount == level.TriggerCaseCount) {
				level.Name = existing.Name
				level.Position = existing.Position
				level.NotifyUser = existing.NotifyUser
				policy.Levels[index] = level
				replaced = true
				break
			}
		}
		if !replaced {
			level.Position = len(policy.Levels) + 1
			policy.Levels = append(policy.Levels, level)
		}
		if value := option.GetOption("notify"); value != nil {
			for index := range policy.Levels {
				if policy.Levels[index].IsDefault == level.IsDefault && (level.IsDefault || policy.Levels[index].TriggerCaseCount == level.TriggerCaseCount) {
					policy.Levels[index].NotifyUser = value.BoolValue()
					break
				}
			}
		}
		_, err = ctx.Services.Templates.Update(taskCtx, guild, template.ID, policy)
		if errors.Is(err, quack.ErrTemplateConflict) {
			return fail("Someone changed this template while you were editing. Run the command again to use the latest settings.")
		}
		if err != nil {
			return fail("Could not save that level. Check the outcome and try again.")
		}
		text := fmt.Sprintf("**%s** now uses **%s** from case **%d** onward, until a higher level applies. Existing cases are unchanged.", ui.PlainText(template.Name), outcome, count)
		if outcome == "timeout" {
			text += fmt.Sprintf(" Timeout: %d minutes.", minutes)
		}
		if value := option.GetOption("notify"); value != nil {
			if value.BoolValue() {
				text += " Member DMs are on at this level."
			} else {
				text += " Member DMs are off at this level."
			}
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", text, true)))
		return err
	})
}
