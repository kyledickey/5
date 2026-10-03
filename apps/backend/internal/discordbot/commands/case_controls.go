package commands

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterCaseComponents installs case browsing, context editing and recovery controls.
func RegisterCaseComponents(registry *interactions.ComponentRegistry) error {
	components := map[string]ui.Handler{
		"template_page":    handleTemplatePickerPage,
		"list_prev":        pageCases(-1, false),
		"list_next":        pageCases(1, false),
		"user_prev":        pageCases(-1, true),
		"user_next":        pageCases(1, true),
		"failures_prev":    pageFailures(-1),
		"failures_next":    pageFailures(1),
		"retry":            handleRetryComponent,
		"dismiss":          handleDismissComponent,
		"void":             handleVoidComponent,
		"reverse":          handleReverseComponent,
		"message_template": handleMessageTemplateComponent,
		"user_template":    handleUserTemplateComponent,
		"edit_context":     handleEditContextComponent,
		"view":             handleCaseViewComponent,
		"evidence":         handleCaseEvidenceComponent,
		"evidence_prev":    pageEvidence(-1),
		"evidence_next":    pageEvidence(1),
		"detail_prev":      pageCaseRecord(-1, views.CaseDetailPage),
		"detail_next":      pageCaseRecord(1, views.CaseDetailPage),
		"user_detail":      handleCaseUserComponent,
	}
	for action, handler := range components {
		if err := registry.RegisterComponent("case", action, handler); err != nil {
			return err
		}
	}
	for action, handler := range map[string]ui.Handler{"void_submit": handleVoidModal, "reverse_submit": handleReverseModal, "edit_context_submit": handleEditContextModal} {
		if err := registry.RegisterModal("case", action, handler); err != nil {
			return err
		}
	}
	return nil
}

// handleRetryComponent retries an authorized failed action without replacing its source.
func handleRetryComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "retry")
}

// handleDismissComponent records staff dismissal with private feedback.
func handleDismissComponent(ctx ui.Context) ui.HandlerResult {
	return actionControlComponent(ctx, "dismiss")
}

// actionControlComponent publishes recovery feedback in the invoking channel,
// while existing ephemeral views refresh in place.
// Once mutation succeeds, read or delivery failures must
// not replace its committed outcome with a generic operation failure.
func actionControlComponent(ctx ui.Context, operation string) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That action control is invalid."))
	}
	privateSource := ctx.Interaction.Message != nil && ctx.Interaction.Message.Flags&discordgo.MessageFlagsEphemeral != 0
	acknowledgement := ui.DeferPublic()
	if privateSource {
		acknowledgement = ui.DeferUpdate()
	}
	return ui.Async(acknowledgement, func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		var controlErr error
		if operation == "retry" {
			_, controlErr = ctx.Services.Actions.Retry(taskCtx, guildContext, parsed.Payload)
		} else {
			_, controlErr = ctx.Services.Actions.Dismiss(taskCtx, guildContext, parsed.Payload)
		}
		if controlErr != nil {
			return controlErr
		}
		message := "The action retry is queued."
		if operation == "dismiss" {
			message = "The action failure was dismissed."
		}
		receipt := ui.Conversation("retry", message, "", "Use `/case failures` to review remaining failures.", "", false)
		result, listErr := ctx.Services.Actions.ListFailures(taskCtx, guildContext, 10, 0)
		if listErr == nil {
			receipt = views.FailedActionMessage(result, 1)
			receipt.Content = message + "\n\n" + receipt.Content
		}
		if privateSource {
			if _, err := responder.UpdateMessage(ui.EditMessage(receipt)); err != nil {
				// The action already committed. Keep its success in a private
				// fallback rather than returning a generic operation failure.
				receipt.Ephemeral = true
				_, _ = responder.Followup(receipt)
			}
		} else {
			retainRecoveryReceipt(taskCtx, responder, receipt)
		}
		return nil
	})
}

// handleVoidComponent opens the required correction reason form.
func handleVoidComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || strings.TrimSpace(parsed.Payload) == "" {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Void case", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "reason", Label: "Why are you voiding this case?", Style: discordgo.TextInputParagraph, Required: true, MinLength: 3, MaxLength: 500})}))
}

// handleVoidModal authorizes and saves a void before publishing its success.
func handleVoidModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case control is invalid."))
	}
	reason := modalTextValue(ctx.Interaction.ModalSubmitData(), "reason")
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		item, voidErr := ctx.Services.Cases.Void(taskCtx, guildContext, parsed.Payload, reason, nil)
		if voidErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(voidErr)))
			return editErr
		}
		retainRecoveryReceipt(taskCtx, responder, views.CaseVoidedMessage(item))
		return nil
	})
}

// handleReverseComponent opens an explicit reversal confirmation.
func handleReverseComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || len(strings.Split(parsed.Payload, "|")) != 3 {
		return ui.Immediate(ui.Error("That reversal control is invalid."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "reverse_submit", Version: "v1", Payload: parsed.Payload})
	return ui.Immediate(ui.Modal("Confirm reversal", customID, []discordgo.MessageComponent{ui.Row(discordgo.TextInput{CustomID: "confirm", Label: "Type REVERSE to confirm", Style: discordgo.TextInputShort, Required: true, MinLength: 7, MaxLength: 7})}))
}

// handleReverseModal checks current authority and keeps rejected reversal requests private.
func handleReverseModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	parts := strings.Split(parsed.Payload, "|")
	if err != nil || len(parts) != 3 || modalTextValue(ctx.Interaction.ModalSubmitData(), "confirm") != "REVERSE" {
		return ui.Immediate(ui.Error("Reversal confirmation did not match."))
	}
	return ui.AsyncPublic(func(taskCtx context.Context, responder ui.Responder) error {
		guildContext, resolveErr := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if resolveErr != nil {
			return resolveErr
		}
		_, reverseErr := ctx.Services.Actions.Reverse(taskCtx, guildContext, parts[0], parts[1], model.ActionType(parts[2]))
		if reverseErr != nil {
			_, editErr := responder.EditOriginal(ui.ErrorEdit(caseCommandErrorMessage(reverseErr)))
			return editErr
		}
		retainRecoveryReceipt(taskCtx, responder, ui.Conversation("retry", "The reversal is queued.", "", "The original action stays in the case history.", "", false))
		return nil
	})
}

// retainRecoveryReceipt updates one committed result without repeating moderation.
// Normal commands defer publicly; private recovery browsers keep their own source.
func retainRecoveryReceipt(ctx context.Context, responder ui.Responder, receipt ui.Message) {
	if _, err := establishPrivateCaseReceipt(ctx, responder, receipt); err != nil {
		slog.WarnContext(ctx, "Could not edit committed recovery receipt", "error_type", "discord_response")
		_, _ = responder.Followup(ui.Signal("error", "The change was saved, but I couldn’t update this message. Check `/case view` for the result.", true))
	}
}

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
	detail, err := ctx.Services.Cases.GetNativeDetail(ctx.Context, guild, parsed.Payload)
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

// handleEditContextModal updates staff context without re-running moderation.
func handleEditContextModal(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.ModalSubmitData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That context form is invalid."))
	}
	text := modalTextValue(ctx.Interaction.ModalSubmitData(), "context")
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
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
		result := views.CaseDetailPage(item, 1, ui.SessionApplicationID(ctx.Session))
		result.Content = message + "\n\n" + result.Content
		_, err = responder.EditOriginal(ui.EditMessage(result))
		return err
	})
}

// handleCaseEvidenceComponent shows captured content and context in the staff channel.
func handleCaseEvidenceComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That case button is invalid."))
	}
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		detail, err := ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parsed.Payload, 1)
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseWebLink(views.CaseEvidenceSnapshotPage(detail, 1, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
		return err
	})
}

// pageEvidence reloads the case through live staff authorization on every click;
// component payloads carry navigation only, never captured content or authority.
func pageEvidence(delta int) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		coordinates := strings.SplitN(parts[0], ":", 2)
		position, page := 1, 1
		if len(coordinates) == 1 {
			page, err = strconv.Atoi(coordinates[0])
		} else {
			position, err = strconv.Atoi(coordinates[0])
			if err == nil {
				page, err = strconv.Atoi(coordinates[1])
			}
		}
		if err != nil || position < 1 || position > 1000000 || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				return err
			}
			detail, err := ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], position)
			if err != nil {
				return err
			}
			applicationID := ui.SessionApplicationID(ctx.Session)
			last := len(views.CaseEvidenceSnapshotPages(detail, applicationID))
			if page > last {
				page = last
			}
			page += delta
			if page < 1 && detail.Position > 1 {
				detail, err = ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], detail.Position-1)
				page = 1000000
			} else if page > last && int64(detail.Position) < detail.Total {
				detail, err = ctx.Services.Cases.GetEvidencePage(taskCtx, guild, parts[1], detail.Position+1)
				page = 1
			}
			if err != nil {
				return err
			}
			_, err = responder.UpdateMessage(ui.EditMessage(caseWebLink(views.CaseEvidenceSnapshotPage(detail, page, applicationID), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
			return err
		})
	}
}

// pageCaseRecord shares navigation and authorization for staff case record views.
// Every click reloads the case under live authority; the component payload
// carries only the page number and case ID.
func pageCaseRecord(delta int, render func(*quack.CaseDetailResponse, int, string) ui.Message) ui.Handler {
	return func(ctx ui.Context) ui.HandlerResult {
		parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
		if err != nil {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		parts := strings.SplitN(parsed.Payload, "|", 2)
		if len(parts) != 2 || parts[1] == "" {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		page, err := strconv.Atoi(parts[0])
		if err != nil || page < 1 || page > 1000000 {
			return ui.Immediate(ui.Error("That case page is no longer available."))
		}
		return ui.Async(ui.DeferUpdate(), func(taskCtx context.Context, responder ui.Responder) error {
			guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
			if err != nil {
				return err
			}
			detail, err := ctx.Services.Cases.GetNativeDetail(taskCtx, guild, parts[1])
			if err != nil {
				return err
			}
			_, err = responder.UpdateMessage(ui.EditMessage(caseWebLink(render(detail, page+delta, ui.SessionApplicationID(ctx.Session)), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "cases", detail.ID)))
			return err
		})
	}
}

// handleCaseUserComponent opens member history from a case in the invoking channel.
func handleCaseUserComponent(ctx ui.Context) ui.HandlerResult {
	parsed, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil {
		return ui.Immediate(ui.Error("That user button is invalid."))
	}
	return ui.Async(ui.DeferPublic(), func(taskCtx context.Context, responder ui.Responder) error {
		guild, err := resolveInteractionGuildContext(taskCtx, ctx.Services, ctx.Interaction)
		if err != nil {
			return err
		}
		profile, err := ctx.Services.Cases.UserHistory(taskCtx, guild, parsed.Payload, quack.CaseListInput{Limit: "10"})
		if err != nil {
			return err
		}
		_, err = responder.EditOriginal(ui.EditMessage(caseWebLink(views.CaseProfileMessage(profile, 1, parsed.Payload), ctx.Services.Config.ApplicationBaseURL, guild.Guild.DiscordGuildID, "members", parsed.Payload)))
		return err
	})
}
