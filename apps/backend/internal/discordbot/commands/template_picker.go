package commands

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// caseTemplatePicker shares bounded template navigation between message and user
// context menus while preserving the selected target in every component ID.
func caseTemplatePicker(templates []quack.TemplateResponse, kind, payload string, page int) ui.Message {
	if len(templates) == 0 {
		return ui.Signal("error", "No active case template is available.", true)
	}
	pages := (len(templates) + 24) / 25
	page = max(0, min(page, pages-1))
	action := "user_template"
	if kind == "m" {
		action = "message_template"
	}
	id, err := ui.EncodeCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: payload})
	if err != nil {
		return ui.Signal("error", "Use `/case add` to choose a template for this target.", true)
	}
	options := make([]discordgo.SelectMenuOption, 0, 25)
	for _, template := range templates[page*25 : min((page+1)*25, len(templates))] {
		options = append(options, discordgo.SelectMenuOption{Label: templateAutocompleteLabel(template), Value: template.ID})
	}
	menu := discordgo.SelectMenu{CustomID: id, Placeholder: "Choose an active case template", MinValues: intPointer(1), MaxValues: 1, Options: options}
	message := ui.Signal("case", "Choose the template for this case.", true)
	message.Components = []discordgo.MessageComponent{ui.Row(menu)}
	if pages > 1 {
		makeButton := func(label string, next int, disabled bool) discordgo.Button {
			id, err := ui.EncodeCustomID(ui.CustomID{Namespace: "case", Action: "template_page", Version: "v1", Payload: fmt.Sprintf("%s|%d|%s", kind, max(0, next), payload)})
			if err != nil {
				return ui.Button("case:template_page:v1:invalid", label, discordgo.SecondaryButton, true)
			}
			return ui.Button(id, label, discordgo.SecondaryButton, disabled)
		}
		message.Content += fmt.Sprintf("\n-# Page %d of %d", page+1, pages)
		message.Components = append(message.Components, ui.Row(makeButton("Previous", page-1, page == 0), makeButton("Next", page+1, page == pages-1)))
	}
	return message
}

// handleTemplatePickerPage acknowledges navigation before live authorization and
// reloads current templates so archive/create operations do not leave stale lists.
func handleTemplatePickerPage(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	parts := strings.SplitN(parsed.Payload, "|", 3)
	if err != nil || len(parts) != 3 || (parts[0] != "u" && parts[0] != "m") {
		return ui.Immediate(ui.Error("That template page is unavailable."))
	}
	page, err := strconv.Atoi(parts[1])
	if err != nil || page < 0 {
		return ui.Immediate(ui.Error("That template page is unavailable."))
	}
	return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return err
		}
		templates, err := ctx.Services.Templates.ListActive(taskCtx, guild)
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit("Could not load case templates. Try again."))
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseTemplatePicker(templates, parts[0], parts[2], page)))
		return err
	})
}
