package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealStaffPage paginates a complete statement while retaining decision controls.
// Shared queue messages use page one; browsing opens a private copy for each staff
// member so one reader cannot change another reader's place in the statement.
func AppealStaffPage(appeal *quack.AppealResponse, page int, applicationID string) ui.Message {
	message := AppealStaffMessage(appeal)
	if appeal == nil {
		return message
	}
	pages := ui.TextPages(discordtext.Resolve(message.Content, applicationID), 1700)
	page = max(1, min(page, len(pages)))
	message.Content = pages[page-1]
	if len(pages) > 1 {
		message.Content += fmt.Sprintf("\n\n-# Case #%d · Statement page %d/%d", appeal.CaseNumber, page, len(pages))
		controls, _ := ui.Pagination("appeal", "statement", fmt.Sprintf("%d|%s", page, appeal.ID), page, len(pages))
		message.Components = append(message.Components, controls...)
	}
	return message
}

// AppealStaffMessage renders the submitted statement and current staff controls.
func AppealStaffMessage(appeal *quack.AppealResponse) ui.Message {
	if appeal == nil {
		return ui.Signal("error", "That appeal couldn’t be found.", true)
	}
	lead := fmt.Sprintf("Received an appeal from <@%s>.", appeal.TargetDiscordUserID)
	switch appeal.Status {
	case model.AppealStatusAccepted, model.AppealStatusRejected:
		lead = fmt.Sprintf("Appeal %s · <@%s>", appeal.Status, appeal.TargetDiscordUserID)
		if appeal.ReviewedByDiscordUserID != "" {
			lead += fmt.Sprintf("\nReviewed by <@%s>.", appeal.ReviewedByDiscordUserID)
		}
	case model.AppealStatusNeedsInformation:
		lead = fmt.Sprintf("Waiting for more information from <@%s>.", appeal.TargetDiscordUserID)
	case model.AppealStatusClosed:
		lead = fmt.Sprintf("Appeal closed · <@%s>", appeal.TargetDiscordUserID)
	}
	body := []string{}
	if appeal.DecisionReason != "" {
		body = append(body, ui.PlainText(appeal.DecisionReason))
	}
	for _, answer := range appeal.Answers {
		body = append(body, ui.Quote(ui.PlainText(fmt.Sprint(answer.Value))))
	}
	meta := fmt.Sprintf("Case #%d · %s", appeal.CaseNumber, ui.PlainText(appeal.TemplateName))
	message := ui.Conversation("appeal", lead, "", strings.Join(body, "\n\n"), meta, false)
	message.Components = []discordgo.MessageComponent{}
	if appeal.Status == model.AppealStatusPending {
		message.Components = []discordgo.MessageComponent{ui.Row(
			ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "accept", Version: "v1", Payload: appeal.ID}), "Accept", discordgo.SuccessButton, false),
			ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "appeal", Action: "reject", Version: "v1", Payload: appeal.ID}), "Reject", discordgo.DangerButton, false),
		)}
	}
	for _, offer := range appeal.ReversalOffers {
		customID, err := ui.EncodeCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: appeal.ID + "," + offer.OriginalExecutionID + "," + string(offer.ActionType)})
		if err != nil {
			continue
		}
		message.Components = append(message.Components, ui.Row(ui.Button(customID, "Confirm "+strings.ToLower(offer.ActionType.Label()), discordgo.DangerButton, false)))
	}
	return message
}

// AppealEntryMessage opens the Discord form without depending on a dashboard URL.
// Ownership is checked again when opening and submitting the case-linked form.
func AppealEntryMessage(baseURL, guildID, caseID string) (ui.Message, error) {
	id, err := ui.EncodeCustomID(ui.CustomID{Namespace: "appeal", Action: "submit", Version: "v1", Payload: caseID})
	if err != nil {
		return ui.Message{}, err
	}
	message := ui.Signal("appeal", "You can ask staff to review this decision.", false)
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "Appeal decision", discordgo.PrimaryButton, false))}
	return message, nil
}
