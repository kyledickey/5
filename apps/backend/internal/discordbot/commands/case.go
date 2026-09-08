package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// HandleMessageCaseInteraction derives the target from the selected live message and applies the sole active policy, or directs staff to explicit template selection.
func HandleMessageCaseInteraction(ctx ui.Context) ui.HandlerResult {
	interaction := ctx.Interaction
	if interaction == nil || interaction.Interaction == nil || interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Select a server message to create a case."))
	}
	data := interaction.ApplicationCommandData()
	if data.Resolved == nil {
		return ui.Immediate(ui.Error("The selected message is unavailable."))
	}
	message := data.Resolved.Messages[data.TargetID]
	if message == nil || message.Author == nil {
		return ui.Immediate(ui.Error("The selected message is unavailable."))
	}
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, err := resolveInteractionGuildContext(taskCtx, ctx.Services, interaction)
		if err == nil {
			err = ctx.Services.Guilds.Authorize(taskCtx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord)
		}
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return err
		}
		templates, err := ctx.Services.Templates.ListActive(taskCtx, guildContext)
		if err != nil || len(templates) == 0 {
			_, err = responder.EditOriginal(ui.ErrorEdit("No active case template is available."))
			return err
		}
		if len(templates) > 1 {
			_, err = responder.EditOriginal(ui.EditMessage(caseTemplatePicker(templates, "m", strings.Join([]string{message.Author.ID, message.ChannelID, message.ID}, "|"), 0)))
			return err
		}
		template := &templates[0]
		link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", interaction.GuildID, message.ChannelID, message.ID)
		created, err := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: message.Author.ID, Source: model.CaseSourceDiscord, ContextChannelDiscordID: message.ChannelID, ContextMessageDiscordID: message.ID, ContextValues: messageLinkContext(template, link), EvidenceLinks: []string{link}, IdempotencyKey: interaction.ID})
		if err != nil {
			_, err = responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			return err
		}
		return publishPrivateContextCase(taskCtx, responder, ctx.Services, created, template)
	})
}

// HandleCaseInteraction handles case interaction and translates it into the package's application or response contract.
func HandleCaseInteraction(ctx ui.Context) ui.HandlerResult {
	interaction := ctx.Interaction
	if interaction == nil || interaction.Interaction == nil {
		return ui.HandlerResult{}
	}
	if interaction.Type == discordgo.InteractionApplicationCommandAutocomplete {
		return ui.Immediate(handleTemplateAutocomplete(ctx.Context, ctx.Services, interaction))
	}

	data := interaction.ApplicationCommandData()
	add := data.GetOption("add")
	if add == nil {
		return handleCaseStaffSubcommand(ctx, data)
	}

	if err := validateCaseInteraction(ctx.Context, ctx.Services, interaction, add); err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}

	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		result, err := createCaseFromInteraction(taskCtx, ctx.Services, interaction, add)
		if err != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(err)))
			if editErr != nil {
				return editErr
			}
			return nil
		}

		receipt := views.CaseCreatedMessage(views.CaseCreated{Case: result.Case, Template: result.Template})
		message, err := ui.Publish(responder, receipt)
		if err != nil {
			// A committed case must never be replaced by the dispatcher's
			// generic command failure. Retrying this edit cannot repeat it.
			message, err = establishPrivateCaseReceipt(taskCtx, responder, receipt)
			if err != nil {
				receipt.Ephemeral = true
				receipt.Content += "\n\nThe case was created. Do not create it again; use `/case view` to check its result."
				_, _ = responder.Followup(receipt)
				return nil
			}
		}
		if err == nil && message != nil {
			if refreshErr := updatePublicCaseResult(taskCtx, responder, ctx.Services, result.Case, message.ID, message.ChannelID, result.Template); refreshErr != nil {
				_, _ = responder.Followup(ui.Content("The case was created, but automatic result updates could not be saved. Use `/case view` to check the outcome.", true))
			}
		}
		return err
	})
}

// validateCaseInteraction checks case interaction before state is read or changed.
func validateCaseInteraction(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) error {
	if services == nil || services.Guilds == nil || services.Cases == nil {
		return errors.New("case command services are not configured")
	}
	if interaction.GuildID == "" {
		return errors.New("case commands must be used in a server")
	}

	templateOption := add.GetOption("template")
	userOption := add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return quack.ErrCaseValidation
	}
	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil {
		return err
	}
	return services.Guilds.Authorize(ctx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord)
}
