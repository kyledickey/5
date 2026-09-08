package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// handleEditContextComponent loads current staff context for a small edit form.
// Every opening and submission checks current Discord permissions independently.
func handleEditContextComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	guild, err := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}
	detail, err := ctx.Services.Cases.Get(ctx.Context, guild, parsed.Payload)
	if err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}
	parts := []string{}
	for _, value := range detail.ContextValues {
		if value.Value != nil {
			parts = append(parts, fmt.Sprint(value.Value))
		}
	}
	text := strings.Join(parts, "\n\n")
	if len([]rune(text)) > 4000 {
		return ui.Immediate(ui.Error("This case has too much context for one Discord form."))
	}
	id := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "edit_context_submit", Version: "v1", Payload: detail.ID})
	return ui.Immediate(ui.Modal(fmt.Sprintf("Context for case #%d", detail.CaseNumber), id, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "context", Label: "What happened?", Placeholder: "Describe what happened or paste a Discord message link.", Style: discordgo.TextInputParagraph, MaxLength: 4000, Required: false, Value: text})}))
}

// handleEditContextModal updates context privately without re-running moderation.
func handleEditContextModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That context form is invalid."))
	}
	text := modalTextValue(ctx.Interaction.ModalSubmitData(), "context")
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		item, err := ctx.Services.Cases.UpdateContext(taskCtx, guild, parsed.Payload, text)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("Context saved for case #%d.", item.CaseNumber)
		if item.EvidenceIncomplete && quack.ContextContainsMessageLinks(text) {
			message += fmt.Sprintf(" Some evidence could not be saved. Use `/case evidence case:%d` with the message link to try again.", item.CaseNumber)
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Signal("edit", message, true)))
		return err
	})
}
