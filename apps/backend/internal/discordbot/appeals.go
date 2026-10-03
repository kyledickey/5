package discordbot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterAppealComponents installs member form entry/submission and staff recovery controls.
func RegisterAppealComponents(registry *interactions.ComponentRegistry, services *quack.Services, appeals *quack.AppealService) error {
	if registry == nil || services == nil || services.Guilds == nil || services.Actions == nil || appeals == nil {
		return errors.New("appeal component dependencies are not configured")
	}
	if err := registry.RegisterComponent("appeal", "submit", appealSubmissionHandler(appeals)); err != nil {
		return err
	}
	if err := registry.RegisterModal("appeal", "submit", appealSubmissionModal(appeals)); err != nil {
		return err
	}
	for _, action := range []string{"accept", "reject"} {
		if err := registry.RegisterComponent("appeal", action, appealDecisionHandler(services, appeals, action)); err != nil {
			return err
		}
		if err := registry.RegisterModal("appeal", action+"_reason", appealDecisionModal(services, appeals, action)); err != nil {
			return err
		}
	}
	for action, delta := range map[string]int{"statement_prev": -1, "statement_next": 1} {
		if err := registry.RegisterComponent("appeal", action, appealStatementPage(services, appeals, delta)); err != nil {
			return err
		}
	}
	return registry.RegisterComponent("appeal", "reverse", appealReversalHandler(services, appeals))
}

// appealReversalHandler checks current review authority before queuing a ban or
// timeout removal linked to the accepted appeal.
func appealReversalHandler(services *quack.Services, appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to remove the punishment."))
		}
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That punishment button is broken. Open the case to try again."))
		}
		parts := strings.Split(parsed.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return ui.Immediate(ui.Error("That punishment button is broken. Open the case to try again."))
		}
		actionType := model.ActionType(parts[2])
		if actionType != model.ActionRemoveTimeout && actionType != model.ActionUnbanUser {
			return ui.Immediate(ui.Error("Only bans and timeouts can be removed here."))
		}
		appealID, executionID := parts[0], parts[1]
		guildID := ctx.Interaction.GuildID
		actor := ctx.Interaction.Member.User
		displayName := ctx.Interaction.Member.Nick
		if displayName == "" {
			displayName = actor.GlobalName
		}
		return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
			guildContext, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: guildID, DiscordUserID: actor.ID, DisplayName: displayName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return nil
			}
			appeal, err := appeals.GetStaff(taskCtx, guildContext, appealID)
			if err != nil || appeal.Status != model.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ui.ErrorEdit("Accept the appeal before removing its punishment."))
				return nil
			}
			linkedAppealID := appeal.ID
			if _, err := services.Actions.ReverseForAppeal(taskCtx, guildContext, appeal.CaseID, executionID, actionType, &linkedAppealID); err != nil {
				_, _ = responder.EditOriginal(ui.ErrorEdit("I couldn’t queue the punishment removal. Check your moderation permissions and try again."))
				return nil
			}
			message := ui.Signal("retry", "Punishment removal queued. Check the case for the result.", false)
			_, err = ui.Publish(responder, message)
			return err
		})
	}
}

// appealSubmissionHandler opens one case-linked statement form for its owner.
func appealSubmissionHandler(appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		memberID := appealInteractionMember(ctx.Interaction)
		if memberID == "" {
			return ui.Immediate(ui.Error("I couldn’t identify your Discord account. Try opening the appeal again."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal button is broken. Ask a moderator for help."))
		}
		if err := appeals.CanSubmit(ctx.Context, id.Payload, memberID); err != nil {
			return ui.Immediate(ui.Error(appealSubmissionError(err)))
		}
		return ui.Immediate(ui.Modal("Appeal this case", ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "submit", Version: "v1", Payload: id.Payload}), []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "What would you like staff to reconsider?", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 4000, Placeholder: "Explain what happened or share your apology. You can submit once."})}))
	}
}

// appealSubmissionModal saves a single statement with fresh case ownership and
// eligibility checks. No Discord guild membership is required after a ban.
func appealSubmissionModal(appeals *quack.AppealService) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		memberID := appealInteractionMember(ctx.Interaction)
		if memberID == "" {
			return ui.Immediate(ui.Error("I couldn’t identify your Discord account. Open the appeal form again."))
		}
		data := ctx.Interaction.ModalSubmitData()
		id, err := ui.DecodeCustomID(data.CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("I couldn’t read this appeal form. Open it again and try once more."))
		}
		var statement string
		for _, component := range data.Components {
			var children []discordgo.MessageComponent
			switch row := component.(type) {
			case discordgo.ActionsRow:
				children = row.Components
			case *discordgo.ActionsRow:
				children = row.Components
			}
			for _, child := range children {
				switch input := child.(type) {
				case discordgo.TextInput:
					if input.CustomID == "reason" {
						statement = input.Value
					}
				case *discordgo.TextInput:
					if input.CustomID == "reason" {
						statement = input.Value
					}
				}
			}
		}
		return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
			_, err := appeals.Submit(taskCtx, id.Payload, memberID, quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: statement}}})
			if err != nil {
				_, editErr := responder.EditOriginal(ui.ErrorEdit(appealSubmissionError(err)))
				return editErr
			}
			_, err = ui.Publish(responder, ui.Signal("appeal", "Your appeal was submitted. We’ll DM you when the moderators decide.", true))
			return err
		})
	}
}

// appealInteractionMember uses Discord's authenticated interaction identity, never
// a member ID supplied in a button or modal payload.
func appealInteractionMember(interaction *discordgo.InteractionCreate) string {
	if interaction == nil || interaction.Interaction == nil {
		return ""
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	return ""
}

// appealSubmissionError gives members actionable feedback without case details.
func appealSubmissionError(err error) string {
	switch {
	case errors.Is(err, quack.ErrAppealConflict):
		return "You already submitted an appeal for this case."
	case errors.Is(err, model.ErrAppealCaseIneligible):
		return "This case cannot be appealed."
	case errors.Is(err, quack.ErrAppealNotFound):
		return "This appeal is not available to you. Open the appeal button in your own case DM."
	case errors.Is(err, quack.ErrAppealValidation):
		return "Write your appeal in 1–4,000 characters."
	default:
		return "Your appeal could not be saved. Please try again."
	}
}

// appealDecisionHandler resolves current Discord authority for every queue click.
// Guilds that require decision reasons get a form instead of one-click execution;
// the form must be the initial interaction response, so the guild setting is read
// before any acknowledgement. The case/appeal transaction arbitrates competing
// moderators; editing the queue message is feedback only and cannot turn a failed
// decision into a success.
func appealDecisionHandler(services *quack.Services, appeals *quack.AppealService, action string) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to decide it."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal button is broken. Open /appeals to try again."))
		}
		required, err := appeals.ReviewReasonRequired(ctx.Context, ctx.Interaction.GuildID)
		if err != nil {
			return ui.Immediate(ui.Error("I couldn’t check the server’s appeal settings. Try again in a moment."))
		}
		if required {
			title := "Accept appeal"
			if action == "reject" {
				title = "Reject appeal"
			}
			modalID := ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: action + "_reason", Version: "v1", Payload: id.Payload})
			return ui.Immediate(ui.Modal(title, modalID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Reason sent to the member", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 2000, Placeholder: "Explain this decision. The member receives this reason."})}))
		}
		reason := "This case has been voided."
		if action == "reject" {
			reason = "Appeal rejected."
		}
		return ui.Async(ui.DeferUpdate(), appealDecisionTask(services, appeals, action, id.Payload, reason, ctx.Interaction, ui.SessionApplicationID(ctx.Session)))
	}
}

// appealDecisionModal completes a reason-required decision. Live authority is
// refreshed on submission because the moderator's permissions may have changed
// since the form was opened; the entered reason replaces the canned default.
func appealDecisionModal(services *quack.Services, appeals *quack.AppealService, action string) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in the server’s review queue to decide it."))
		}
		data := ctx.Interaction.ModalSubmitData()
		id, err := ui.DecodeCustomID(data.CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That appeal form is broken. Open /appeals to try again."))
		}
		reason := appealModalTextValue(data.Components, "reason")
		if strings.TrimSpace(reason) == "" {
			return ui.Immediate(ui.Error("Write a reason for this decision. The member receives it."))
		}
		return ui.Async(ui.DeferUpdate(), appealDecisionTask(services, appeals, action, id.Payload, reason, ctx.Interaction, ui.SessionApplicationID(ctx.Session)))
	}
}

// appealDecisionTask executes one decision with live authority and updates the
// queue message with the result.
func appealDecisionTask(services *quack.Services, appeals *quack.AppealService, action, appealID, reason string, interaction *discordgo.InteractionCreate, applicationID string) ui.Task {
	actor := interaction.Member.User
	guildID := interaction.GuildID
	privateQueue := interaction.Message != nil && interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	return func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: guildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
		if err != nil {
			_, err = responder.Followup(ui.Signal("error", "I couldn’t check your Discord permissions. Try again in a moment.", true))
			return err
		}
		var decided *quack.AppealResponse
		switch action {
		case "accept":
			decided, err = appeals.Accept(taskCtx, guild, appealID, reason)
		case "reject":
			decided, err = appeals.Reject(taskCtx, guild, appealID, reason)
		default:
			err = quack.ErrAppealValidation
		}
		if err != nil {
			text := "I couldn’t save your decision. Please try again."
			switch {
			case errors.Is(err, quack.ErrAppealConflict):
				text = "This appeal has already been decided or its case was voided."
			case errors.Is(err, quack.ErrAppealPermissionDenied):
				text = "You need Moderate Members permission to review appeals."
			case errors.Is(err, quack.ErrAppealNotFound):
				text = "I couldn’t find that appeal in this server. Open /appeals to see pending appeals."
			case errors.Is(err, quack.ErrAppealValidation):
				text = "Write a reason between 1 and 2,000 characters."
			}
			_, editErr := responder.Followup(ui.Signal("error", text, true))
			return editErr
		}
		message := views.AppealStaffPage(decided, 1, applicationID)
		if privateQueue {
			message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Next pending appeal", discordgo.SecondaryButton, false)))
		}
		_, err = ui.Publish(responder, message)
		return err
	}
}

// appealModalTextValue reads one text input from a submitted form without
// trusting component ordering.
func appealModalTextValue(components []discordgo.MessageComponent, customID string) string {
	for _, component := range components {
		var children []discordgo.MessageComponent
		switch row := component.(type) {
		case discordgo.ActionsRow:
			children = row.Components
		case *discordgo.ActionsRow:
			children = row.Components
		}
		for _, child := range children {
			switch input := child.(type) {
			case discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			case *discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			}
		}
	}
	return ""
}

// appealStatementPage opens shared review messages in the staff channel and rechecks live
// authority before every read. It never edits the shared queue's reading position.
func appealStatementPage(services *quack.Services, appeals *quack.AppealService, delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" || ctx.Interaction.Member == nil || ctx.Interaction.Member.User == nil {
			return ui.Immediate(ui.Error("Open this appeal in your server's review queue."))
		}
		id, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		parts := strings.SplitN(id.Payload, "|", 2)
		if err != nil || len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("I couldn’t open that page. Run /appeals to start again."))
		}
		page, err := strconv.Atoi(parts[0])
		if err != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("I couldn’t open that page. Run /appeals to start again."))
		}
		ack := ui.DeferPublic()
		if ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0 {
			ack = ui.DeferUpdate()
		}
		return ui.Async(ack, func(taskCtx context.Context, responder ui.Responder) error {
			actor := ctx.Interaction.Member.User
			guild, err := services.Guilds.ResolveDiscordStaffContext(taskCtx, quack.DiscordStaffContextInput{DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: actor.ID, DisplayName: actor.GlobalName, LastActiveAt: time.Now().UTC()})
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return err
			}
			appeal, err := appeals.GetStaff(taskCtx, guild, parts[1])
			if err != nil {
				_, err = responder.EditOriginal(ui.ErrorEdit("I couldn’t open that appeal. Check that you have Moderate Members permission, then try /appeals."))
				return err
			}
			message := views.AppealStaffPage(appeal, page+delta, ui.SessionApplicationID(ctx.Session))
			message.Ephemeral = false
			message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "page", Version: "v1", Payload: "1"}), "Pending appeals", discordgo.SecondaryButton, false)))
			_, err = responder.EditOriginal(ui.EditMessage(message))
			return err
		})
	}
}

// AppealStaffChannelResolver returns the configured staff-only destination for an appeal event.
type AppealStaffChannelResolver interface {
	AppealStaffChannel(context.Context, string) (string, error)
}

// AppealNotificationAdapter sends appeal outbox messages through Discord without embedding staff identity.
type AppealNotificationAdapter struct {
	Session  *discordgo.Session
	Resolver AppealStaffChannelResolver
}

// SendAppealMemberNotification delivers one member-owned status update through DM.
func (a *AppealNotificationAdapter) SendAppealMemberNotification(ctx context.Context, discordUserID string, notice quack.AppealMemberNotification) (string, error) {
	if a == nil || a.Session == nil || strings.TrimSpace(discordUserID) == "" {
		return "", fmt.Errorf("%w: member adapter unavailable", quack.ErrAppealDeliveryDeferred)
	}
	channel, err := a.Session.UserChannelCreate(discordUserID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", appealMemberSendError(err)
	}
	message, err := a.Session.ChannelMessageSendComplex(channel.ID, appealMemberNotificationMessage(notice).SendParams(ui.SessionApplicationID(a.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", appealMemberSendError(err)
	}
	return message.ID, nil
}

// SendAppealStaffNotification delivers one queue entry only to a configured staff destination.
func (a *AppealNotificationAdapter) SendAppealStaffNotification(ctx context.Context, guildID string, appeal *quack.AppealResponse, receipt quack.AppealQueueReceipt) (quack.AppealQueueReceipt, error) {
	if a == nil || a.Session == nil || a.Resolver == nil {
		return receipt, fmt.Errorf("%w: staff adapter unavailable", quack.ErrAppealDeliveryDeferred)
	}
	channelID, err := a.Resolver.AppealStaffChannel(ctx, guildID)
	if err != nil {
		return receipt, fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	if strings.TrimSpace(channelID) == "" {
		return receipt, fmt.Errorf("%w: staff channel unavailable", quack.ErrAppealDeliveryDeferred)
	}
	if receipt.ChannelID == channelID && receipt.MessageID != "" {
		message := views.AppealStaffPage(appeal, 1, ui.SessionApplicationID(a.Session)).ForApplication(ui.SessionApplicationID(a.Session))
		emptyEmbeds := []*discordgo.MessageEmbed{}
		emptyAttachments := []*discordgo.MessageAttachment{}
		_, err := a.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: receipt.MessageID, Channel: channelID, Content: &message.Content, Components: &message.Components, Embeds: &emptyEmbeds, Attachments: &emptyAttachments, Files: message.Files, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err == nil {
			return receipt, nil
		}
		var rest *discordgo.RESTError
		if !errors.As(err, &rest) || rest.Message == nil || rest.Message.Code != discordgo.ErrCodeUnknownMessage {
			// Editing a known message is idempotent, even after an uncertain response.
			return receipt, fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
		}
	}
	message, err := a.Session.ChannelMessageSendComplex(channelID, views.AppealStaffPage(appeal, 1, ui.SessionApplicationID(a.Session)).SendParams(ui.SessionApplicationID(a.Session)), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return receipt, appealSendError(err)
	}
	return quack.AppealQueueReceipt{ChannelID: channelID, MessageID: message.ID}, nil
}

// appealSendError retries only explicit Discord rejections, never ambiguous
// network errors after a send may have reached Discord.
func appealSendError(err error) error {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		switch rest.Response.StatusCode {
		case http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests:
			return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
		}
	}
	return err
}

// appealMemberSendError leaves blocked/closed DMs as recorded failures rather
// than probing the member indefinitely. Rate limits remain safe to retry.
func appealMemberSendError(err error) error {
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil && rest.Response.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	return err
}
