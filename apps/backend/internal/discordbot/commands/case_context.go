package commands

import (
	"context"
	"encoding/json"
	"fmt"
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
			_, err := responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(resolveErr)))
			return err
		}
		if err := ctx.Services.Guilds.Authorize(taskCtx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord); err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
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
			_, err := responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(createErr)))
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

// publishPrivateContextCase retains a private moderator receipt before sending
// a standalone public notice. The notice never references the hidden receipt;
// failed or uncertain publication cannot discard the committed case.
func publishPrivateContextCase(ctx context.Context, responder ui.Responder, services *quack.Services, created *quack.CaseResponse, template *quack.TemplateResponse) error {
	projection := initialModeratorReceipt(created, template)
	if services != nil && services.Cases != nil {
		if loaded, err := services.Cases.ReceiptForPublication(ctx, created.ID); err == nil {
			loaded.Case.EvidenceIncomplete = loaded.Case.EvidenceIncomplete || created.EvidenceIncomplete
			projection = loaded
		}
	}
	private := views.CaseModeratorReceipt(projection)
	if _, err := establishPrivateCaseReceipt(ctx, responder, private); err != nil {
		private.Content += "\n\nThe case was created. Use View case to check its result; Do not create it again."
		private.Ephemeral = true
		_, _ = responder.Followup(private)
		return nil
	}
	// Public presentation is built independently: even a private delivery failure
	// cannot accidentally copy staff fields into the channel.
	publicCase := *projection.Case
	publicCase.Reason = ""
	public := views.CaseCreatedMessage(views.CaseCreated{MemberReason: projection.MemberReason, Case: &publicCase, Template: &quack.TemplateResponse{Name: projection.RuleName}})
	message, err := responder.PublishChannel(ctx, public)
	if err != nil {
		private.Content += "\n\nThe case was created, but Quack could not post its result publicly. This private copy is still usable."
		_, _ = responder.EditOriginal(ui.EditMessage(private))
	} else if message != nil {
		if err := updatePublicCaseResult(ctx, responder, services, &publicCase, message.ID, message.ChannelID, &quack.TemplateResponse{Name: projection.RuleName, ReasonTemplate: projection.MemberReason}); err != nil {
			private.Content += "\n\nThe case was created, but automatic public result updates could not be saved. Use View case to check the outcome."
			_, _ = responder.EditOriginal(ui.EditMessage(private))
		}
	}
	if services != nil && services.Cases != nil && projection.Pending() {
		go refreshPrivateCaseReceipt(ctx, responder, services.Cases.ReceiptForPublication, created.ID, 2*time.Second, 14*time.Minute)
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
