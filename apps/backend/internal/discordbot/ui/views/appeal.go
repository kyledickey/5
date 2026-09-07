package views

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealStaffMessage renders a staff-only appeal timeline and explicit reversal offers.
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
	for _, event := range appeal.Events {
		actor := "Staff"
		if event.ActorType == "member" {
			actor = "Member"
		}
		at := ""
		if date := ui.RelativeTime(event.CreatedAt); date != "" {
			at = " " + date
		}
		body = append(body, actor+" wrote"+at+":\n"+ui.Quote(ui.PlainText(event.Body)))
	}
	message := ui.Conversation("appeal", lead, "", strings.Join(body, "\n\n"), "Case "+ui.PlainText(appeal.CaseID), false)
	for _, offer := range appeal.ReversalOffers {
		customID, err := ui.EncodeCustomID(ui.CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: appeal.ID + "," + offer.OriginalExecutionID + "," + string(offer.ActionType)})
		if err != nil {
			continue
		}
		message.Components = append(message.Components, ui.Row(ui.Button(customID, "Confirm "+strings.ToLower(offer.ActionType.Label()), discordgo.DangerButton, false)))
	}
	return message
}

// AppealEntryMessage creates a secure dashboard link for an eligible case notification.
func AppealEntryMessage(baseURL, guildID, caseID string) (ui.Message, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ui.Message{}, fmt.Errorf("secure dashboard base URL is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/guilds/" + url.PathEscape(guildID) + "/cases/" + url.PathEscape(caseID) + "/appeal"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	message := ui.Signal("appeal", "You can ask staff to review this decision.", false)
	message.Components = []discordgo.MessageComponent{ui.Row(ui.LinkButton(parsed.String(), "Appeal decision", false))}
	return message, nil
}
