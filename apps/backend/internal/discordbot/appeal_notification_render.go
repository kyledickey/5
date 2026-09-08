package discordbot

import (
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// appealMemberNotificationBody keeps review decisions distinct from enforcement and
// quotes staff-authored context without disclosing the reviewing staff member.
func appealMemberNotificationBody(notice quack.AppealMemberNotification) string {
	if notice.Intent == nil {
		return notice.LegacyBody
	}
	status, reason := notice.Intent.Status, notice.Intent.Reason
	icon, lead, next := "appeal", "Your appeal was closed.", ""
	switch status {
	case model.AppealStatusNeedsInformation:
		icon, lead, next = "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."
	case model.AppealStatusAccepted:
		icon, lead, next = "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."
	case model.AppealStatusRejected:
		icon, lead = "decline", "Your appeal was declined."
	}
	body := discordtext.Conversation(icon, lead, discordtext.Plain(reason), next, "")
	if notice.Intent.RejoinURL != "" {
		body += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + notice.Intent.RejoinURL
	}
	return body
}
