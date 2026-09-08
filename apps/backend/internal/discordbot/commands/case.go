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
	if interaction == nil || interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Select a server message to create a case."))
	}
	data := interaction.ApplicationCommandData()
	message := data.Resolved.Messages[data.TargetID]
	if message == nil || message.Author == nil {
		return ui.Immediate(ui.Error("The selected message is unavailable."))
	}
	guildContext, err := resolveInteractionGuildContext(ctx.Context, ctx.Services, interaction)
	if err != nil {
		return ui.Immediate(ui.Error(caseCommandErrorMessage(err)))
	}
	templates, err := ctx.Services.Templates.ListActive(ctx.Context, guildContext)
	if err != nil || len(templates) == 0 {
		return ui.Immediate(ui.Error("No active case template is available."))
	}
	if len(templates) > 1 {
		options := make([]discordgo.SelectMenuOption, 0, len(templates))
		for _, template := range templates {
			options = append(options, discordgo.SelectMenuOption{Label: templateAutocompleteLabel(template), Value: template.ID})
			if len(options) == 25 {
				break
			}
		}
		customID, encodeErr := ui.EncodeCustomID(ui.CustomID{Namespace: "case", Action: "message_template", Version: "v1", Payload: strings.Join([]string{message.Author.ID, message.ChannelID, message.ID}, "|")})
		if encodeErr != nil {
			return ui.Immediate(ui.Error("Use `/case add` to select a template for this message."))
		}
		selectMenu := discordgo.SelectMenu{CustomID: customID, Placeholder: "Choose an active case template", MinValues: intPointer(1), MaxValues: 1, Options: options}
		return ui.Immediate(ui.Ephemeral(ui.Message{Content: "{{quack:case}} Choose the template that matches this message.", Components: []discordgo.MessageComponent{ui.Row(selectMenu)}, Ephemeral: true}))
	}
	template := templates[0]
	link := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", interaction.GuildID, message.ChannelID, message.ID)
	values := messageLinkContext(&template, link)
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		created, createErr := ctx.Services.Cases.Create(taskCtx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: message.Author.ID, Source: model.CaseSourceDiscord, ContextChannelDiscordID: message.ChannelID, ContextMessageDiscordID: message.ID, ContextValues: values, EvidenceLinks: []string{link}, IdempotencyKey: interaction.ID})
		if createErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(createErr)))
			return editErr
		}
		message, followErr := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{Case: created, Template: &template}))
		if followErr == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, created, message.ID, &template)
		}
		return followErr
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

		message, err := ui.Publish(responder, views.CaseCreatedMessage(views.CaseCreated{
			Case:     result.Case,
			Template: result.Template,
		}))
		if err == nil && message != nil {
			updatePublicCaseResult(taskCtx, responder, ctx.Services, result.Case, message.ID, result.Template)
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
