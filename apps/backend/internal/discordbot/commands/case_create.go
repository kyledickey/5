package commands

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// caseCommandCreateResult captures the outcome of case command create result for the caller.
type caseCommandCreateResult struct {
	Case     *quack.CaseResponse
	Template *quack.TemplateResponse
}

// createCaseFromInteraction creates case from interaction while preserving validation, authorization, and persistence invariants.
func createCaseFromInteraction(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) (*caseCommandCreateResult, error) {
	if services == nil || services.Guilds == nil || services.Cases == nil {
		return nil, errors.New("case command services are not configured")
	}
	if interaction.GuildID == "" {
		return nil, errors.New("case commands must be used in a server")
	}

	templateOption := add.GetOption("template")
	userOption := add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return nil, quack.ErrCaseValidation
	}

	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil {
		return nil, err
	}
	if err := services.Guilds.Authorize(ctx, guildContext, model.PermissionActionCaseCreate, model.AuditSourceDiscord); err != nil {
		return nil, err
	}

	templateID, template, err := resolveTemplate(ctx, services, guildContext, templateOption.StringValue())
	if err != nil {
		return nil, err
	}

	contextValues := []quack.CaseContextValueInput{}
	if link := optionStringValue(add.GetOption("message_link")); link != "" {
		contextValues = messageLinkContext(template, link)
	}
	created, err := services.Cases.Create(ctx, guildContext, quack.CaseInput{
		Attachments:             interactionEvidenceFiles(interaction, add.GetOption("file")),
		TemplateID:              templateID,
		TargetDiscordUserID:     optionStringValue(userOption),
		Source:                  model.CaseSourceDiscord,
		ContextChannelDiscordID: interaction.ChannelID,
		ContextValues:           contextValues, EvidenceLinks: evidenceLinksFromOption(add.GetOption("message_link")), IdempotencyKey: interaction.ID,
	})
	if err != nil {
		return nil, err
	}

	return &caseCommandCreateResult{Case: created, Template: template}, nil
}

// evidenceLinksFromOption forwards pasted links into the shared capture service.
func evidenceLinksFromOption(option *discordgo.ApplicationCommandInteractionDataOption) []string {
	if option == nil || strings.TrimSpace(option.StringValue()) == "" {
		return nil
	}
	return []string{strings.TrimSpace(option.StringValue())}
}

// interactionEvidenceFiles accepts only attachment metadata resolved by Discord.
func interactionEvidenceFiles(interaction *discordgo.InteractionCreate, option *discordgo.ApplicationCommandInteractionDataOption) []quack.DiscordAttachmentSnapshot {
	if option == nil || interaction == nil {
		return nil
	}
	resolved := interaction.ApplicationCommandData().Resolved
	if resolved == nil {
		return nil
	}
	file := resolved.Attachments[optionStringValue(option)]
	if file == nil {
		return nil
	}
	return []quack.DiscordAttachmentSnapshot{{ID: file.ID, Filename: file.Filename, ContentType: file.ContentType, URL: file.URL, SizeBytes: int64(file.Size)}}
}
