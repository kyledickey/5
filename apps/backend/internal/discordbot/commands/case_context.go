package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			_, err := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(resolveErr)))
			return err
		}
		if err := ctx.Services.Guilds.Authorize(taskCtx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return err
		}
		_, template, templateErr := resolveTemplate(taskCtx, ctx.Services, guildContext, data.Values[0])
		if templateErr != nil || template == nil {
			_, err := responder.EditOriginal(ui.ErrorEdit("That case template is not available."))
			return err
		}
		link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", ctx.Interaction.GuildID, parts[1], parts[2])
		values := messageLinkContext(template, link)
		created, createErr := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: parts[0], Source: model.CaseSourceDiscord, ContextChannelDiscordID: parts[1], ContextMessageDiscordID: parts[2], ContextValues: values, EvidenceLinks: []string{link}, IdempotencyKey: ctx.Interaction.ID})
		if createErr != nil {
			_, err := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(createErr)))
			return err
		}
		return publishPrivateContextCase(taskCtx, responder, ctx.Services, created, template)
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

// publishPrivateContextCase completes the private acknowledgement before posting
// a public case result. Discord otherwise makes the first followup inherit the
// private deferred response. A publication failure retains a usable private case.
func publishPrivateContextCase(ctx context.Context, responder ui.Responder, services *quack.Services, created *quack.CaseResponse, template *quack.TemplateResponse) error {
	result := views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: template})
	if _, err := establishPrivateCaseReceipt(ctx, responder, result); err != nil {
		// A failed edit may have reached Discord. Never publish a potentially
		// inherited private followup as though its visibility were confirmed.
		slog.WarnContext(ctx, "Could not establish private case receipt", "case_id", created.ID, "error_type", fmt.Sprintf("%T", err))
		result.Ephemeral = true
		result.Content += "\n\nThe case was created. Quack could not confirm public delivery. Do not create it again; use `/case view` to check its result."
		if _, fallbackErr := responder.Followup(result); fallbackErr != nil {
			slog.WarnContext(ctx, "Could not deliver committed case receipt", "case_id", created.ID, "error_type", fmt.Sprintf("%T", fallbackErr))
		}
		return nil
	}
	message, err := responder.Followup(result)
	if err != nil {
		// Moderation already committed. Do not let the dispatcher's generic error
		// replace this durable case receipt or suggest issuing the punishment again.
		slog.WarnContext(ctx, "Could not publish context-menu case result", "case_id", created.ID, "error_type", fmt.Sprintf("%T", err))
		result.Content += "\n\nThe case was created, but Quack could not post its result publicly. This private copy is still usable."
		if _, editErr := responder.EditOriginal(ui.EditMessage(result)); editErr != nil {
			slog.WarnContext(ctx, "Could not explain case publication failure", "case_id", created.ID, "error_type", fmt.Sprintf("%T", editErr))
		}
		return nil
	}
	if message != nil {
		if err := updatePublicCaseResult(ctx, responder, services, created, message.ID, message.ChannelID, template); err != nil {
			slog.WarnContext(ctx, "Could not persist public case receipt", "case_id", created.ID, "error_type", fmt.Sprintf("%T", err))
			result.Content += "\n\nThe case was created and posted publicly, but automatic result updates could not be saved. Use `/case view` to check the outcome."
			_, _ = responder.EditOriginal(ui.EditMessage(result))
			return nil
		}
	}
	if err := responder.DeleteOriginal(); err != nil {
		slog.WarnContext(ctx, "Could not remove private case receipt", "case_id", created.ID, "error_type", fmt.Sprintf("%T", err))
	}
	return nil
}

// establishPrivateCaseReceipt retries the idempotent original-message edit before
// public publication. It never repeats the committed moderation operation.
func establishPrivateCaseReceipt(ctx context.Context, responder ui.Responder, result ui.Message) (*discordgo.Message, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, lastErr
			case <-timer.C:
			}
		}
		message, err := responder.EditOriginal(ui.EditMessage(result))
		lastErr = err
		if err == nil {
			return message, nil
		}
	}
	return nil, lastErr
}
