package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// templateManagementOptions supplies native controls for existing policies.
// Archiving retains history; restoring makes the same rule available again.
func templateManagementOptions() []*discordgo.ApplicationCommandOption {
	specs := []struct{ name, description string }{
		{"view", "Show a rule and its escalation outcomes"},
		{"edit", "Change a rule's name, member reason, appeals or case decay"},
		{"remove-level", "Remove an escalation and use the preceding outcome"},
		{"archive", "Stop using a rule for new cases; keep its history"},
		{"restore", "Make an archived rule available again"},
	}
	result := make([]*discordgo.ApplicationCommandOption, 0, len(specs))
	for _, spec := range specs {
		option := &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: spec.name, Description: spec.description, Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "template", Description: "Rule to manage", Required: true, Autocomplete: true},
		}}
		switch spec.name {
		case "edit":
			option.Options = append(option.Options,
				&discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: "name", Description: "Rule name", MaxLength: 100},
				&discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionString, Name: "reason", Description: "Reason shown to the member", MaxLength: 1000},
				&discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionBoolean, Name: "appeals", Description: "Allow members to appeal new cases under this rule"},
				&discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionInteger, Name: "decay-days", Description: "Count cases from the last N days; 0 keeps all-time counting", MinValue: floatPointer(0), MaxValue: quack.MaxCaseDecayDays},
			)
		case "remove-level":
			option.Options = append(option.Options, &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionInteger, Name: "case", Description: "Starting case number of the escalation to remove", Required: true, MinValue: floatPointer(2), MaxValue: 1000000})
		}
		result = append(result, option)
	}
	return result
}

// handleTemplateManage acknowledges privately before loading current permissions
// and policy. Edits use a versioned snapshot and never alter existing cases.
func handleTemplateManage(ctx ui.Context, option *discordgo.ApplicationCommandInteractionDataOption) ui.HandlerResult {
	ref := option.GetOption("template")
	if ref == nil {
		return ui.Immediate(ui.Error("Choose a template."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		fail := func(text string) error { _, err := responder.EditOriginal(ui.ErrorEdit(text)); return err }
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
			return fail("You need Manage Server permission to manage templates.")
		}
		templates, err := ctx.Services.Templates.List(taskCtx, guild)
		if err != nil {
			return fail("Could not load templates. Try again.")
		}
		var selected *quack.TemplateResponse
		for _, template := range templates {
			if template.ID == strings.TrimSpace(ref.StringValue()) || strings.EqualFold(template.Slug, strings.TrimSpace(ref.StringValue())) {
				selected = &template
				break
			}
		}
		if selected == nil {
			return fail("That template is unavailable.")
		}
		input := selected.EditInput()
		message := ""
		switch option.Name {
		case "view":
			_, err := responder.EditOriginal(ui.EditMessage(templatePolicyMessage(*selected)))
			return err
		case "archive":
			_, err = ctx.Services.Templates.Archive(taskCtx, guild, selected.ID)
			message = fmt.Sprintf("**%s** is archived. Its history is kept, and it cannot be used for new cases.", ui.PlainText(selected.Name))
		case "restore":
			_, err = ctx.Services.Templates.Restore(taskCtx, guild, selected.ID)
			message = fmt.Sprintf("**%s** is available for new cases again.", ui.PlainText(selected.Name))
		case "edit":
			changed := false
			if value := option.GetOption("name"); value != nil {
				input.Name = strings.TrimSpace(value.StringValue())
				changed = true
			}
			if value := option.GetOption("reason"); value != nil {
				input.ReasonTemplate = strings.TrimSpace(value.StringValue())
				changed = true
			}
			if value := option.GetOption("appeals"); value != nil {
				input.Appealable = value.BoolValue()
				changed = true
			}
			if value := option.GetOption("decay-days"); value != nil {
				input.CaseDecayDays = int(value.IntValue())
				changed = true
			}
			if !changed {
				return fail("Set a name, reason, appeals choice or decay window to change.")
			}
			if input.Name == "" || input.ReasonTemplate == "" {
				return fail("The rule needs a name and a member reason.")
			}
			selected, err = ctx.Services.Templates.Update(taskCtx, guild, selected.ID, input)
			if err == nil {
				message = fmt.Sprintf("**%s** updated. These settings apply to new cases.", ui.PlainText(selected.Name))
			}
		case "remove-level":
			value := option.GetOption("case")
			if value == nil || value.IntValue() < 2 || value.IntValue() > 1000000 {
				return fail("Choose an escalation starting at case 2 or later. The default outcome must stay.")
			}
			found := false
			for index, level := range input.Levels {
				if !level.IsDefault && level.TriggerCaseCount == int(value.IntValue()) {
					input.Levels = append(input.Levels[:index], input.Levels[index+1:]...)
					found = true
					break
				}
			}
			if !found {
				return fail("There is no escalation starting at that case number. Use `/template view` to see the rule.")
			}
			_, err = ctx.Services.Templates.Update(taskCtx, guild, selected.ID, input)
			message = fmt.Sprintf("Removed the escalation starting at case **%d**. The preceding outcome now continues until the next level. Existing cases are unchanged.", value.IntValue())
		default:
			return fail("Choose a template operation.")
		}
		if errors.Is(err, quack.ErrTemplateConflict) {
			return fail("Someone changed this template while you were editing. Run the command again to use the latest settings.")
		}
		if err != nil {
			return fail("Could not update the template. Check your settings and try again.")
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", message, true)))
		return err
	})
}

// templatePolicyMessage presents thresholds in the same case numbers admins use
// when configuring them. It omits worker controls and immutable internal IDs.
func templatePolicyMessage(template quack.TemplateResponse) ui.Message {
	status := "Active"
	if template.ArchivedAt != nil {
		status = "Archived — unavailable for new cases"
	}
	appeals := "Off"
	if template.Appealable {
		appeals = "On"
	}
	lines := []string{fmt.Sprintf("**%s** · %s", ui.PlainText(template.Name), status), ui.PlainText(template.ReasonTemplate), "", "**Outcomes**"}
	levels := append([]quack.TemplateLevelResponse(nil), template.Levels...)
	sort.SliceStable(levels, func(i, j int) bool {
		if levels[i].IsDefault != levels[j].IsDefault {
			return levels[i].IsDefault
		}
		return levels[i].TriggerCaseCount < levels[j].TriggerCaseCount
	})
	for _, level := range levels {
		count := level.TriggerCaseCount
		if level.IsDefault {
			count = 1
		}
		outcome := "Warning"
		if len(level.Actions) > 0 {
			action := level.Actions[0]
			outcome = action.ActionType.Label()
			if action.ActionType == model.ActionTimeoutUser {
				if action.TimeoutDurationSeconds%60 == 0 {
					outcome += fmt.Sprintf(" (%d minutes)", action.TimeoutDurationSeconds/60)
				} else {
					outcome += fmt.Sprintf(" (%d seconds)", action.TimeoutDurationSeconds)
				}
			}
		}
		dm := "DM off"
		if level.NotifyUser {
			dm = "DM on"
		}
		lines = append(lines, fmt.Sprintf("From case **%d**: %s · %s", count, outcome, dm))
	}
	window := "All-time counting (decay off)."
	if template.CaseDecayDays > 0 {
		window = fmt.Sprintf("Count cases from the last **%d days**. Older cases stay in history.", template.CaseDecayDays)
	}
	lines = append(lines, "", window, "Appeals: **"+appeals+"**", "Each outcome lasts until the next level. Counts include this case and earlier, non-voided cases for this rule. Imported v4 history does not count.", "Use `/template edit` for rule text, appeals and decay, `/template level` for outcomes and DMs, or `/template remove-level` to remove an escalation.")
	return ui.Signal("settings", strings.Join(lines, "\n"), true)
}
