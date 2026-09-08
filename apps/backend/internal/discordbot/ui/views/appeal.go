package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealStaffMessage renders the submitted statement and current staff controls.
func AppealStaffMessage(appeal *quack.AppealResponse) ui.Message {
	if appeal == nil {
		return ui.Signal("error", "That appeal couldn’t be found.", true)
	}
	status := map[model.AppealStatus]string{model.AppealStatusPending: "is waiting for review", model.AppealStatusNeedsInformation: "needs more information", model.AppealStatusAccepted: "was accepted", model.AppealStatusRejected: "was declined", model.AppealStatusClosed: "was closed"}[appeal.Status]
	if status == "" {
		status = "is available for review"
	}
	lead := fmt.Sprintf("The appeal from <@%s> %s.", appeal.TargetDiscordUserID, status)
	body := []string{}
	for _, answer := range appeal.Answers {
		body = append(body, ui.Quote(ui.PlainText(fmt.Sprint(answer.Value))))
	}
	if appeal.DecisionReason != "" {
		body = append(body, "Decision: "+ui.PlainText(appeal.DecisionReason))
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
