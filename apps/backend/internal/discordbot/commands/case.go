package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseCommandSpec binds the /case definition to its interaction handler for explicit runtime registration.
func CaseCommandSpec() CommandSpec {
	return CommandSpec{
		Definition: CaseCommandDefinition(),
		Handler:    HandleCaseInteraction,
	}
}

// MessageCaseCommandSpec starts the same evidence-backed flow from a live Discord message.
func MessageCaseCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{
		Definition: &discordgo.ApplicationCommand{
			Type:                     discordgo.MessageApplicationCommand,
			Name:                     "Add case",
			DefaultMemberPermissions: &permissions,
			DMPermission:             &dm,
		},
		Handler: HandleMessageCaseInteraction,
	}
}

// CaseCommandDefinition returns a fresh /case definition so Discord-side mutation cannot alter registry state.
func CaseCommandDefinition() *discordgo.ApplicationCommand {
	defaultPermissions := int64(discordgo.PermissionModerateMembers)
	dmPermission := false
	return &discordgo.ApplicationCommand{
		Name:                     "case",
		Description:              "Create and manage moderation cases.",
		DefaultMemberPermissions: &defaultPermissions,
		DMPermission:             &dmPermission,
		Options: []*discordgo.ApplicationCommandOption{

			// /case add <template> <user> [message_link] [file]
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "add",
				Description: "Create a moderation case from a template.",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:         discordgo.ApplicationCommandOptionString,
						Name:         "template",
						Description:  "Case template to apply.",
						Required:     true,
						Autocomplete: true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "User to moderate.",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message_link",
						Description: "Discord message link to capture as evidence.",
					},
					{
						Type:        discordgo.ApplicationCommandOptionAttachment,
						Name:        "file",
						Description: "Screenshot or file to save as evidence.",
					},
				},
			},

			// /case evidence <case> [file] [message_link]
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "evidence",
				Description: "Add evidence to an existing case.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "case",
						Description: "Case number or ID",
						Required:    true,
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionAttachment,
						Name:        "file",
						Description: "Screenshot or file to save.",
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "message_link",
						Description: "Discord message to preserve.",
					},
				},
			},

			// /case view <case>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "view",
				Description: "View case details.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "case",
						Description: "Case number or ID.",
						Required:    true,
					},
				},
			},

			// /case list
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "list",
				Description: "List recent guild cases.",
			},

			// /case useer <user>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "user",
				Description: "View a member's case history.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionUser,
						Name:        "user",
						Description: "Member to review.",
						Required:    true,
					},
				},
			},

			// /case failures
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "failuires",
				Description: "Review failed Discord actions.",
			},

			// /case retry <execution>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "retry",
				Description: "Retry the same failed action.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "execution",
						Description: "Failed execution ID.",
						Required:    true,
					},
				},
			},

			// /case dismiss <execution>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "dismiss",
				Description: "Dismiss a failure from active review.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "execution",
						Description: "Failed execution ID.",
						Required:    true,
					},
				},
			},

			// /case void <case> <reason> <confirm>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "void",
				Description: "Void an incorrect case.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "case",
						Description: "Case number or ID.",
						Required:    true,
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "reason",
						Description: "Required correction reason.",
						Required:    true,
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "confirm",
						Description: "Confirm this irreversible control.",
						Required:    true,
					},
				},
			},

			// /case reverse <case> <execution> <action> <confirm>
			&discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "reverse",
				Description: "Revmove a timeout or ban.",
				Options: []*discordgo.ApplicationCommandOption{
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "case",
						Description: "Case ID.",
						Required:    true,
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "execution",
						Description: "Original execution ID.",
						Required:    true,
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "action",
						Description: "Reversal action.",
						Required:    true,
						Choices: []*discordgo.ApplicationCommandOptionChoice{
							{Name: "Remove timeout", Value: string(model.ActionRemoveTimeout)},
							{Name: "Unban", Value: string(model.ActionUnbanUser)},
						},
					},
					&discordgo.ApplicationCommandOption{
						Type:        discordgo.ApplicationCommandOptionBoolean,
						Name:        "confirm",
						Description: "Confirm this irreversible control.",
						Required:    true,
					},
				},
			},
		},
	}
}

// HandleMessageCaseInteraction derives the target from the selected live message and applies the sole active policy, or directs staff to explicit template selection.
func HandleMessageCaseInteraction(ctx ui.Context) ui.HandlerResult {
	interaction := ctx.Interaction
	if interaction == nil ||
		interaction.Interaction == nil ||
		interaction.GuildID == "" {
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

	return ui.Async(ui.DeferEphemeral(),
		func(taskCtx context.Context, responder ui.Responder) error {
			guildContext, err := resolveInteractionGuildContext(taskCtx, ctx.Services, interaction)
			if err == nil {
				err = ctx.Services.Guilds.Authorize(
					taskCtx,
					guildContext,
					model.PermissionActionCaseCreate,
					model.AuditSourceDiscord,
				)
			}
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
				return err
			}

			templates, err := ctx.Services.Templates.ListActive(taskCtx, guildContext)
			if err != nil || len(templates) == 0 {
				_, err = responder.EditOriginal(ui.ErrorEdit("No active case template is available."))
				return err
			}
			if len(templates) > 1 {
				_, err = responder.EditOriginal(
					ui.EditMessage(
						caseTemplatePicker(
							templates,
							"m",
							strings.Join([]string{message.Author.ID, message.ChannelID, message.ID}, "|"),
							0,
						),
					),
				)
				return err
			}

			template := &templates[0]
			link := fmt.Sprintf(
				"https://discord.com/channels/%s/%s/%s",
				interaction.GuildID,
				message.ChannelID,
				message.ID,
			)

			created, err := ctx.Services.Cases.Create(
				taskCtx,
				guildContext,
				quack.CaseInput{
					TemplateID:              template.ID,
					TargetDiscordUserID:     message.Author.ID,
					Source:                  model.CaseSourceDiscord,
					ContextChannelDiscordID: message.ChannelID,
					ContextMessageDiscordID: message.ID,
					ContextValues:           messageLinkContext(template, link),
					EvidenceLinks:           []string{link},
					IdempotencyKey:          interaction.ID,
				},
			)
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
				return err
			}

			return publishCaseResult(publishCaseResultOpts{
				ctx:       taskCtx,
				responder: responder,
				services:  ctx.Services,
				created:   created,
				template:  template,
				original:  false,
			})
		},
	)
}

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
		return ui.Immediate(ui.Error(caseCreateErrorMessage(err)))
	}

	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		result, err := createCaseFromInteraction(taskCtx, ctx.Services, interaction, add)
		if err != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
			return editErr
		}

		return publishCaseResult(publishCaseResultOpts{
			ctx:       taskCtx,
			responder: responder,
			services:  ctx.Services,
			created:   result.Case,
			template:  result.Template,
			original:  true,
		})
	})
}

func validateCaseInteraction(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) error {
	if services == nil ||
		services.Guilds == nil ||
		services.Cases == nil {
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

type caseCommandCreateResult struct {
	Case     *quack.CaseResponse
	Template *quack.TemplateResponse
}

func createCaseFromInteraction(ctx context.Context, services *quack.Services, interaction *discordgo.InteractionCreate, add *discordgo.ApplicationCommandInteractionDataOption) (*caseCommandCreateResult, error) {
	templateOption := add.GetOption("template")
	userOption := add.GetOption("user")
	if templateOption == nil || userOption == nil {
		return nil, quack.ErrCaseValidation
	}
	guildContext, err := resolveInteractionGuildContext(ctx, services, interaction)
	if err != nil {
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
		ContextValues:           contextValues,
		EvidenceLinks:           evidenceLinksFromOption(add.GetOption("message_link")),
		IdempotencyKey:          interaction.ID,
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

	return []quack.DiscordAttachmentSnapshot{
		{
			ID:          file.ID,
			Filename:    file.Filename,
			ContentType: file.ContentType,
			URL:         file.URL,
			SizeBytes:   int64(file.Size),
		},
	}
}
