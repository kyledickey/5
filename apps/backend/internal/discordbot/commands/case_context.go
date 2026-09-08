package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// handleMessageTemplateComponent applies the selected rule immediately to the message author.
func handleMessageTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)
	parts := strings.Split(parsed.Payload, "|")
	if err != nil || len(parts) != 3 || len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That message case flow is invalid."))
	}
	guildContext, resolveErr := resolveInteractionGuildContext(ctx.Context, ctx.Services, ctx.Interaction)
	if resolveErr != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(resolveErr)))
	}
	_, template, templateErr := resolveTemplate(ctx.Context, ctx.Services, guildContext, data.Values[0])
	if templateErr != nil || template == nil {
		return ui.Immediate(ui.Error("That case template is not available."))
	}
	link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", ctx.Interaction.GuildID, parts[1], parts[2])
	values := messageLinkContext(template, link)
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		created, createErr := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: parts[0], Source: model.CaseSourceDiscord, ContextChannelDiscordID: parts[1], ContextMessageDiscordID: parts[2], ContextValues: values, EvidenceLinks: []string{link}, IdempotencyKey: ctx.Interaction.ID})
		if createErr != nil {
			return createErr
		}
		message, followErr := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: template}))
		if followErr == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, created, message.ID, template)
		}
		return followErr
	})
}

// messageLinkContext pre-fills optional message fields from the selected message.
func messageLinkContext(template *quack.TemplateResponse, link string) []quack.CaseContextValueInput {
	values := []quack.CaseContextValueInput{}
	for _, field := range template.ContextFields {
		if field.FieldType != model.ContextFieldMessageLink {
			continue
		}
		raw, _ := json.Marshal(link)
		values = append(values, quack.CaseContextValueInput{Key: field.Key, Value: raw})
	}
	return values
}

// modalTextValue accepts Discord component decoding in both pointer and value form.
func modalTextValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, valueOK := component.(discordgo.ActionsRow); valueOK {
				row = &value
			} else {
				continue
			}
		}
		for _, child := range row.Components {
			input, ok := child.(*discordgo.TextInput)
			if ok && input.CustomID == customID {
				return input.Value
			}
			if input, ok := child.(discordgo.TextInput); ok && input.CustomID == customID {
				return input.Value
			}
		}
	}
	return ""
}
