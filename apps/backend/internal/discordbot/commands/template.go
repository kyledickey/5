package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	return CommandSpec{
		Definition: &discordgo.ApplicationCommand{
			Name:                     "template",
			Description:              "Create and manage moderation rules",
			DefaultMemberPermissions: &permissions,
			DMPermission:             &dm,
			Options: append(templateManagementOptions(),
				[]*discordgo.ApplicationCommandOption{
					templateLevelOption(),
					{
						Type:        discordgo.ApplicationCommandOptionSubCommand,
						Name:        "create",
						Description: "Create a rule with a default outcome",
						Options: []*discordgo.ApplicationCommandOption{
							{
								Type:        discordgo.ApplicationCommandOptionString,
								Name:        "outcome",
								Description: "Default outcome; warning if omitted",
								Choices: []*discordgo.ApplicationCommandOptionChoice{
									{Name: "Warning", Value: "warning"},
									{Name: "Timeout", Value: "timeout"},
									{Name: "Kick", Value: "kick"},
									{Name: "Ban", Value: "ban"},
								},
							},
							{
								Type:        discordgo.ApplicationCommandOptionInteger,
								Name:        "minutes",
								Description: "Timeout length in minutes (required for a timeout)", MinValue: floatPointer(1),
								MaxValue: 40320,
							},
						},
					},
				}...,
			),
		},
		Handler: handleTemplateCommand,
	}
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

	id := ui.MustCustomID(
		ui.CustomID{
			Namespace: "template",
			Action:    "create",
			Version:   "v1",
			Payload:   fmt.Sprintf("%s|%d", outcome, minutes),
		})

	return ui.Immediate(ui.Modal("Create a rule", id,
		[]discordgo.MessageComponent{
			ui.Row(
				discordgo.TextInput{
					CustomID:    "name",
					Label:       "Rule name",
					Placeholder: "NSFW chatting",
					Style:       discordgo.TextInputShort,
					Required:    true,
					MaxLength:   100,
				},
			),
			ui.Row(
				discordgo.TextInput{
					CustomID:    "reason",
					Label:       "Reason shown to the member",
					Placeholder: "Keep explicit content out of chat.",
					Style:       discordgo.TextInputParagraph,
					Required:    true,
					MaxLength:   1000,
				},
			),
		},
	),
	)
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

	return ui.AsyncPublic(
		func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)

			if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
				_, err = responder.EditOriginal(ui.ErrorEdit("You need Manage Server permission to create templates."))
				return err
			}

			level := quack.TemplateLevelInput{
				Name:       "Default",
				Position:   1,
				IsDefault:  true,
				NotifyUser: true,
			}

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
				level.Actions = []quack.TemplateActionInput{
					{
						ActionType:             action,
						TimeoutDurationSeconds: int(minutes * 60),
					},
				}
			}

			created, err := ctx.Services.Templates.Create(
				taskCtx,
				guild,
				quack.TemplateInput{
					Slug:           "rule-" + ctx.Interaction.ID,
					Name:           name,
					ReasonTemplate: reason,
					Appealable:     true,
					Levels:         []quack.TemplateLevelInput{level},
				},
			)
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("Could not create the template. Check the form and try again."))
				return err
			}

			outcome := parts[0]
			if outcome == "timeout" {
				outcome = fmt.Sprintf("%d-minute timeout", minutes)
			}

			message := ui.Signal("settings",
				fmt.Sprintf(
					"**%s** is ready. First case: **%s**.\nUse `/case add` when someone breaks this rule.",
					ui.PlainText(created.Name),
					outcome,
				), true)
			_, err = responder.EditOriginal(ui.EditMessage(message))
			return err
		},
	)
}

// templateLevelOption is the level subcommand spec
func templateLevelOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        "level",
		Description: "Choose the punishment after repeated rule breaks",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "template",
				Description:  "Rule to edit",
				Required:     true,
				Autocomplete: true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "case",
				Description: "How many times? 1 is the first case, 3 is the third",
				Required:    true,
				MinValue:    floatPointer(1),
				MaxValue:    1000000,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "outcome",
				Description: "What should happen?",
				Required:    true,
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Warning", Value: "warning"},
					{Name: "Timeout", Value: "timeout"},
					{Name: "Kick", Value: "kick"},
					{Name: "Ban", Value: "ban"},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "minutes",
				Description: "Timeout length in minutes",
				MinValue:    floatPointer(1),
				MaxValue:    40320,
			},
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "notify",
				Description: "Send the member a DM at this level; defaults to on for new levels",
			},
		},
	}
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
		if level.Name != "restore" &&
			level.Name != "view" &&
			level.Name != "edit" &&
			template.ArchivedAt != nil {
			continue
		}
		if strings.Contains(strings.ToLower(template.Name+" "+template.Slug), query) {
			choices = append(
				choices,
				&discordgo.ApplicationCommandOptionChoice{
					Name:  templateAutocompleteLabel(template),
					Value: template.ID,
				},
			)
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
	ref := option.GetOption("template")
	countOption := option.GetOption("case")
	outcomeOption := option.GetOption("outcome")
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

	return ui.AsyncPublic(
		func(taskCtx context.Context, responder ui.Responder) error {
			fail := func(text string) error {
				_, err := responder.EditOriginal(ui.ErrorEdit(text))
				return err
			}

			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
				return fail("You need Manage Server permission to edit templates.")
			}

			_, template, err := resolveTemplate(taskCtx, ctx.Services, guild, ref.StringValue())
			if err != nil || template == nil {
				return fail("That active template is unavailable.")
			}

			policy := template.EditInput()
			level := quack.TemplateLevelInput{
				Name:             fmt.Sprintf("Case %d onward", count),
				TriggerCaseCount: int(count),
				NotifyUser:       true,
			}

			if count == 1 {
				level.IsDefault = true
				level.TriggerCaseCount = 0
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
				level.Actions = []quack.TemplateActionInput{
					{
						ActionType:             action,
						TimeoutDurationSeconds: int(minutes * 60),
					},
				}
			}

			replaced := false
			for index, existing := range policy.Levels {
				if existing.IsDefault == level.IsDefault &&
					(level.IsDefault ||
						existing.TriggerCaseCount == level.TriggerCaseCount) {
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
					if policy.Levels[index].IsDefault == level.IsDefault &&
						(level.IsDefault ||
							policy.Levels[index].TriggerCaseCount == level.TriggerCaseCount) {
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

			text := fmt.Sprintf(
				"**%s:** **%s** after **%d** cases.",
				ui.PlainText(template.Name),
				outcome,
				count,
			)

			if outcome == "timeout" {
				minuteLabel := "minutes"
				if minutes == 1 {
					minuteLabel = "minute"
				}
				text += fmt.Sprintf(" Timeout: %d %s.", minutes, minuteLabel)
			}

			if value := option.GetOption("notify"); value != nil {
				if value.BoolValue() {
					text += " I’ll DM the member."
				} else {
					text += " The member will not be notified."
				}
			}

			_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", text, true)))

			return err
		})
}

// templateManagementOptions supplies native controls for existing policies.
// Archiving retains history; restoring makes the same rule available again.
func templateManagementOptions() []*discordgo.ApplicationCommandOption {
	specs := []struct{ name, description string }{
		{"view", "Show a rule and its escalation outcomes"},
		{"edit", "Edit a rule"},
		{"remove-level", "Remove a punishment step from a rule"},
		{"archive", "Stop using a rule for new cases; keep its history"},
		{"restore", "Make an archived rule available again"},
	}

	result := make([]*discordgo.ApplicationCommandOption, 0, len(specs))
	for _, spec := range specs {
		option := &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        spec.name,
			Description: spec.description,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "template",
					Description:  "Rule to manage",
					Required:     true,
					Autocomplete: true,
				},
			},
		}

		switch spec.name {
		case "edit":
			option.Options = append(option.Options,
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Rule name",
					MaxLength:   100,
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Reason shown to the member",
					MaxLength:   1000,
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "appeals",
					Description: "Allow members to appeal new cases under this rule",
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "decay-days",
					Description: "How long cases should remain be counted for. 0=forever",
					MinValue:    floatPointer(0),
					MaxValue:    quack.MaxCaseDecayDays,
				},
			)
		case "remove-level":
			option.Options = append(option.Options,
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "case",
					Description: "Which step? Use its case count from /template view, e.g. 3",
					Required:    true,
					MinValue:    floatPointer(2),
					MaxValue:    1000000,
				},
			)
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
		return ui.Immediate(ui.Error("Choose a rule first."))
	}

	return ui.AsyncPublic(
		func(taskCtx context.Context, responder ui.Responder) error {
			fail := func(text string) error {
				_, err := responder.EditOriginal(ui.ErrorEdit(text))
				return err
			}

			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil || guild == nil || !guild.Can(model.PermissionActionCaseTemplateWrite) {
				return fail("You need Manage Server permission to manage templates.")
			}

			templates, err := ctx.Services.Templates.List(taskCtx, guild)
			if err != nil {
				return fail("I couldn’t load the rules. Try again in a moment.")
			}

			var selected *quack.TemplateResponse
			for _, template := range templates {
				if template.ID == strings.TrimSpace(ref.StringValue()) ||
					strings.EqualFold(
						template.Slug,
						strings.TrimSpace(ref.StringValue()),
					) {
					selected = &template
					break
				}
			}
			if selected == nil {
				return fail("I can’t find that rule. Choose it from the command’s suggestions.")
			}

			input := selected.EditInput()
			message := ""
			switch option.Name {
			case "view":
				_, err := responder.EditOriginal(ui.EditMessage(templatePolicyMessage(*selected)))
				return err

			case "archive":
				_, err = ctx.Services.Templates.Archive(taskCtx, guild, selected.ID)
				message = fmt.Sprintf(
					"**%s** is archived. Its history is kept, and it cannot be used for new cases.",
					ui.PlainText(selected.Name),
				)

			case "restore":
				_, err = ctx.Services.Templates.Restore(taskCtx, guild, selected.ID)
				message = fmt.Sprintf(
					"**%s** is available for new cases again.",
					ui.PlainText(selected.Name),
				)

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
					message = fmt.Sprintf(
						"**%s** updated. These settings apply to new cases.",
						ui.PlainText(selected.Name),
					)
				}

			case "remove-level":
				value := option.GetOption("case")
				if value == nil || value.IntValue() < 2 || value.IntValue() > 1000000 {
					return fail("Choose a step from case 2 onward. To change the first step, use `/template level`.")
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
					return fail("There is no step at that count. Check `/template view` for this rule’s steps.")
				}

				_, err = ctx.Services.Templates.Update(taskCtx, guild, selected.ID, input)
				message = fmt.Sprintf(
					"Removed the **%d-case** step. Check `/template view` for the updated rule.",
					value.IntValue(),
				)

			default:
				return fail("Choose a template operation.")
			}

			if errors.Is(err, quack.ErrTemplateConflict) {
				return fail("Someone changed this template while you were editing. Run the command again to use the latest settings.")
			}
			if err != nil {
				return fail("I couldn’t save that change. Check the options and try again.")
			}

			_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("settings", message, true)))
			return err
		},
	)
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

	lines := []string{
		fmt.Sprintf(
			"**%s** · %s",
			ui.PlainText(template.Name),
			status,
		),
		ui.PlainText(template.ReasonTemplate),
		"",
		"**When someone breaks this rule**",
	}

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

		lines = append(lines, fmt.Sprintf("**%d+ times:** %s · %s", count, outcome, dm))
	}

	window := "-# Counting all cases for this rule."
	if template.CaseDecayDays > 0 {
		window = fmt.Sprintf("-# Counting cases from the last **%d days**.", template.CaseDecayDays)
	}

	lines = append(lines, "", window, "-# Appeals: **"+appeals+"**", "Need a hand? `/help topic:rules`")

	return ui.Signal("settings", strings.Join(lines, "\n"), true)
}
