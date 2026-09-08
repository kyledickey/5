package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseModeratorReceipt renders compact private decision and delivery feedback.
// Error codes use the same safe explanations as case detail; raw Discord errors
// and staff evidence never enter this receipt. View case rechecks live authority.
func CaseModeratorReceipt(receipt *quack.CaseReceiptResponse) ui.Message {
	item := receipt.Case
	level := ""
	if item.SelectedLevel != nil {
		level = item.SelectedLevel.Name
	}
	lines := []string{staffActionSummary(receipt.Actions)}
	if receipt.Notification == nil {
		lines = append(lines, "Member notification is disabled.")
	} else {
		lines = append(lines, notificationDeliverySentence(string(receipt.Notification.Status)))
	}
	if receipt.Appealable {
		lines = append(lines, "The member can appeal this case.")
	} else {
		lines = append(lines, "This case cannot be appealed.")
	}
	if item.EvidenceIncomplete {
		lines = append(lines, "Some evidence could not be saved. Open View evidence to inspect it.")
	}
	lead := fmt.Sprintf("Case #%d added for <@%s> · **%s**", item.CaseNumber, item.TargetDiscordUserID, ui.PlainText(receipt.RuleName))
	voided := item.Validity == model.CaseValidityVoided
	if voided {
		lead = fmt.Sprintf("Case #%d was voided · <@%s> · **%s**", item.CaseNumber, item.TargetDiscordUserID, ui.PlainText(receipt.RuleName))
	}
	message := ui.Conversation("case_add", lead, "", strings.Join(lines, "\n"), ui.PlainText(level), true)
	message.Components = []discordgo.MessageComponent{ui.Row(casePrimaryControls(item.ID, item.TargetDiscordUserID, voided)...), ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "view", Version: "v1", Payload: item.ID}), "View case", discordgo.SecondaryButton, false))}
	return message
}
