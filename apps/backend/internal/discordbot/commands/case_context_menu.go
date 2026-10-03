package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// UserCaseCommandSpec exposes the native member context-menu entry point.
func UserCaseCommandSpec() CommandSpec {
	permissions := int64(discordgo.PermissionModerateMembers)
	dm := false
	return CommandSpec{
		Definition: &discordgo.ApplicationCommand{
			Type:                     discordgo.UserApplicationCommand,
			Name:                     "Add case for member",
			DefaultMemberPermissions: &permissions,
			DMPermission:             &dm,
		},
		Handler: HandleUserCaseInteraction,
	}
}

// HandleUserCaseInteraction selects a policy for the member chosen in Discord.
// No message evidence is fabricated; staff can attach context after creation.
func HandleUserCaseInteraction(ctx ui.Context) ui.HandlerResult {
	if ctx.Interaction == nil ||
		ctx.Interaction.Interaction == nil ||
		ctx.Interaction.GuildID == "" {
		return ui.Immediate(ui.Error("Select a server member to create a case."))
	}

	data := ctx.Interaction.ApplicationCommandData()
	if data.TargetID == "" ||
		data.Resolved == nil ||
		data.Resolved.Users[data.TargetID] == nil {
		return ui.Immediate(ui.Error("The selected member is unavailable."))
	}

	return ui.Async(ui.DeferEphemeral(),
		func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err == nil {
				err = ctx.Services.Guilds.Authorize(taskCtx, guild, model.PermissionActionCaseCreate, model.AuditSourceDiscord)
			}

			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
				return err
			}

			templates, err := ctx.Services.Templates.ListActive(taskCtx, guild)
			if err != nil || len(templates) == 0 {
				_, err = responder.EditOriginal(ui.ErrorEdit("No active case template is available."))
				return err
			}
			if len(templates) > 1 {
				_, err = responder.EditOriginal(ui.EditMessage(caseTemplatePicker(templates, "u", data.TargetID, 0)))
				return err
			}

			template := &templates[0]
			created, err := ctx.Services.Cases.Create(
				taskCtx,
				guild,
				quack.CaseInput{
					TemplateID:          template.ID,
					TargetDiscordUserID: data.TargetID,
					Source:              model.CaseSourceDiscord,
					IdempotencyKey:      ctx.Interaction.ID,
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

// handleUserTemplateComponent refreshes moderator authority when a policy is selected.
func handleUserTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)
	if err != nil ||
		parsed.Payload == "" ||
		len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That case selection is unavailable."))
	}

	return ui.Async(ui.DeferUpdate(),
		func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
				return err
			}

			if err := ctx.Services.Guilds.Authorize(taskCtx, guild, model.PermissionActionCaseCreate, model.AuditSourceDiscord); err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
				return err
			}

			_, template, err := resolveTemplate(taskCtx, ctx.Services, guild, data.Values[0])
			if err != nil || template == nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("That case template is not available."))
				return err
			}

			created, err := ctx.Services.Cases.Create(
				taskCtx,
				guild,
				quack.CaseInput{
					TemplateID:          template.ID,
					TargetDiscordUserID: parsed.Payload,
					Source:              model.CaseSourceDiscord,
					IdempotencyKey:      caseSelectionKey(ctx.Interaction),
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

// caseTemplatePicker shares bounded template navigation between message and user
// context menus while preserving the selected target in every component ID.
func caseTemplatePicker(templates []quack.TemplateResponse, kind, payload string, page int) ui.Message {
	if len(templates) == 0 {
		return ui.Signal("error", "There are no rules yet. Create one with `/template create`.", true)
	}

	pages := (len(templates) + 24) / 25
	page = max(0, min(page, pages-1))

	action := "user_template"
	if kind == "m" {
		action = "message_template"
	}

	id, err := ui.EncodeCustomID(ui.CustomID{
		Namespace: "case",
		Action:    action,
		Version:   "v1",
		Payload:   payload,
	})
	if err != nil {
		return ui.Signal("error", "Use `/case add` to choose a template for this target.", true)
	}

	options := make([]discordgo.SelectMenuOption, 0, 25)

	// what is this line dude?
	for _, template := range templates[page*25 : min((page+1)*25, len(templates))] {
		options = append(options, discordgo.SelectMenuOption{
			Label: templateAutocompleteLabel(template),
			Value: template.ID,
		})
	}

	menu := discordgo.SelectMenu{
		CustomID:    id,
		Placeholder: "Select a rule",
		MinValues:   intPointer(1),
		MaxValues:   1,
		Options:     options,
	}
	message := ui.Signal("case", "Which rule did they break?", true)
	message.Components = []discordgo.MessageComponent{ui.Row(menu)}

	if pages > 1 {
		makeButton := func(label string, next int, disabled bool) discordgo.Button {
			id, err := ui.EncodeCustomID(ui.CustomID{
				Namespace: "case",
				Action:    "template_page",
				Version:   "v1",
				Payload: fmt.Sprintf(
					"%s|%d|%s",
					kind, max(0, next), payload,
				)},
			)
			if err != nil {
				return ui.Button(ui.ButtonOpts{
					ID:       "case:template_page:v1:invalid",
					Label:    label,
					Style:    discordgo.SecondaryButton,
					Disabled: true,
				})
			}
			return ui.Button(ui.ButtonOpts{
				ID:       id,
				Label:    label,
				Style:    discordgo.SecondaryButton,
				Disabled: disabled,
			})
		}
		message.Content += fmt.Sprintf("\n-# Page %d of %d", page+1, pages)
		message.Components = append(
			message.Components,
			ui.Row(
				makeButton("Previous", page-1, page == 0),
				makeButton("Next", page+1, page == pages-1),
			),
		)
	}

	return message
}

// handleTemplatePickerPage acknowledges navigation before live authorization and
// reloads current templates so archive/create operations do not leave stale lists.
func handleTemplatePickerPage(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)

	parts := strings.SplitN(parsed.Payload, "|", 3)
	if err != nil ||
		len(parts) != 3 ||
		(parts[0] != "u" && parts[0] != "m") {
		return ui.Immediate(ui.Error("That template page is unavailable."))
	}

	page, err := strconv.Atoi(parts[1])
	if err != nil || page < 0 {
		return ui.Immediate(ui.Error("That template page is unavailable."))
	}

	return ui.Async(ui.DeferUpdate(),
		func(taskCtx context.Context, responder ui.Responder) error {
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
		},
	)
}

// handleMessageTemplateComponent applies the selected rule immediately to the message author.
func handleMessageTemplateComponent(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.MessageComponentData()
	parsed, err := ui.DecodeCustomID(data.CustomID)

	parts := strings.Split(parsed.Payload, "|")
	if err != nil ||
		len(parts) != 3 ||
		len(data.Values) != 1 {
		return ui.Immediate(ui.Error("That message case flow is invalid."))
	}

	return ui.Async(ui.DeferUpdate(),
		func(taskCtx context.Context, responder ui.Responder) error {
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

			link := fmt.Sprintf(
				"https://discord.com/channels/%s/%s/%s",
				ctx.Interaction.GuildID, parts[1], parts[2],
			)

			values := messageLinkContext(template, link)
			created, err := ctx.Services.Cases.Create(
				taskCtx,
				guildContext,
				quack.CaseInput{
					TemplateID:              template.ID,
					TargetDiscordUserID:     parts[0],
					Source:                  model.CaseSourceDiscord,
					ContextChannelDiscordID: parts[1],
					ContextMessageDiscordID: parts[2],
					ContextValues:           values,
					EvidenceLinks:           []string{link},
					IdempotencyKey:          caseSelectionKey(ctx.Interaction),
				},
			)
			if err != nil {
				_, err := responder.EditOriginal(ui.ErrorEdit(caseCreateErrorMessage(err)))
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

type publishCaseResultOpts struct {
	ctx       context.Context
	responder ui.Responder
	services  *quack.Services
	created   *quack.CaseResponse
	template  *quack.TemplateResponse
	original  bool
}

// publishCaseResult edits the slash-command response (original) or, for a private
// context-menu selector, publishes the result in the channel and removes the
// selector so no private success copy remains. The response includes staff
// context; moderators choose the destination channel.
func publishCaseResult(opts publishCaseResultOpts) error {
	projection := initialModeratorReceipt(opts.created, opts.template)
	if opts.services != nil && opts.services.Cases != nil {
		if loaded, err := opts.services.Cases.ReceiptForPublication(opts.ctx, opts.created.ID); err == nil {
			projection = loaded
		}
	}

	publicCase := *projection.Case
	rule := &quack.TemplateResponse{
		Name:           projection.RuleName,
		ReasonTemplate: projection.MemberReason,
	}
	public := views.CaseCreatedMessage(views.CaseCreated{
		MemberReason: projection.MemberReason,
		Case:         &publicCase,
		Template:     rule,
	})

	var message *discordgo.Message
	var err error
	if opts.original {
		message, err = establishPrivateCaseReceipt(opts.ctx, opts.responder, public)
	} else {
		message, err = opts.responder.PublishChannel(opts.ctx, public)
	}
	if err != nil {
		failure := ui.Signal("error",
			fmt.Sprintf(
				"Case #%d was saved, but I couldn't post the result. Check `/case view` before trying again.",
				opts.created.CaseNumber,
			),
			true,
		)
		_, _ = opts.responder.Followup(failure)

		return nil
	}
	if !opts.original {
		if err := opts.responder.DeleteOriginal(); err != nil {
			_, _ = opts.responder.EditOriginal(
				ui.EditMessage(
					ui.Content(fmt.Sprintf("Case #%d created.", opts.created.CaseNumber), true),
				),
			)
		}
	}
	if message != nil {
		if err := updatePublicCaseResult(updatePublicCaseResultOpts{
			ctx:       opts.ctx,
			responder: opts.responder,
			services:  opts.services,
			created:   &publicCase,
			messageID: message.ID,
			channelID: message.ChannelID,
			template:  rule,
		}); err != nil {
			_, _ = opts.responder.Followup(
				ui.Signal("error", fmt.Sprintf(
					"Case #%d was saved, but its live updates aren't working. Check `/case view` for the result.",
					opts.created.CaseNumber,
				), true),
			)
		}
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

// caseSelectionKey makes a selector single-use even if its cleanup fails or two
// clicks arrive together. Each fresh context-menu invocation has its own message.
func caseSelectionKey(interaction *discordgo.InteractionCreate) string {
	if interaction.Message != nil && interaction.Message.ID != "" {
		return "case-selector:" + interaction.Message.ID
	}
	return interaction.ID
}
