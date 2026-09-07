package views

import (
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

// AuditMirrorMessage summarizes a redacted staff event; durable identifiers stay in a quiet footer.
func AuditMirrorMessage(message quack.AuditMirrorMessage) ui.Message {
	actor := "Quack"
	if message.ActorDiscordUserID != "" {
		actor = "<@" + message.ActorDiscordUserID + ">"
	}
	action := strings.NewReplacer("_", " ", ".", " ").Replace(message.Action)
	verb := auditPhrases[message.Action].verb
	if verb == "" {
		verb = "recorded **" + ui.PlainText(action) + "**"
	}
	body := fmt.Sprintf("%s %s.", actor, verb)
	if string(message.Result) != "success" {
		body = fmt.Sprintf("%s attempted **%s**.\nThe request was %s.", actor, ui.PlainText(action), ui.PlainText(string(message.Result)))
	}
	meta := []string{}
	if message.ResourceID != "" {
		meta = append(meta, ui.PlainText(message.ResourceType)+" `"+strings.ReplaceAll(message.ResourceID, "`", "")+"`")
	}
	meta = append(meta, "Audit "+ui.PlainText(message.AuditEntryID))
	if date := ui.RelativeTime(message.OccurredAt); date != "" {
		meta = append(meta, date)
	}
	icon := auditPhrases[message.Action].icon
	if icon == "" {
		icon = "shield"
	}
	if string(message.Result) != "success" {
		icon = "error"
	}
	return ui.Conversation(icon, body, ui.PlainText(message.FailureReason), "", strings.Join(meta, " · "), false)
}

// auditPhrase maps stable audit contracts to human actions and a matching icon.
type auditPhrase struct{ verb, icon string }

// auditPhrases covers the core mirror events without changing their redacted data contract.
var auditPhrases = map[string]auditPhrase{
	"case.create": {"added a case", "case_add"}, "case.void": {"voided a case", "case_void"}, "case.void.appeal": {"voided a case after an appeal", "case_void"},
	"case_template.create": {"created a template", "spark"}, "case_template.update": {"updated a template", "edit"}, "case_template.archive": {"archived a template", "lock"}, "case_template.restore": {"restored a template", "unlock"}, "case_template.import": {"imported templates", "case_add"}, "case_template.export": {"exported templates", "case"},
	"guild_settings.update": {"updated the server settings", "settings"},
	"case_action.attempt":   {"started an action attempt", "running"}, "case_action.succeeded": {"completed a Discord action", "success"}, "case_action.failed": {"recorded a failed Discord action", "error"}, "case_action.skipped": {"skipped a Discord action", "info"}, "case_action.retrying": {"scheduled another action attempt", "retry"}, "case_action.retry": {"queued another action attempt", "retry"}, "case_action.dismiss": {"dismissed an action failure", "review"}, "case_action.reverse": {"queued an action reversal", "retry"}, "case_action.recovered": {"recovered a stalled action", "retry"},
	"case_notification.sent": {"sent the member a DM", "message"}, "case_notification.failed": {"couldn’t deliver the member’s DM", "error"},
	"appeal.submit": {"submitted an appeal", "appeal"}, "appeal.information.submit": {"added information to an appeal", "reply"}, "appeal.information_requested": {"asked for more information on an appeal", "reply"}, "appeal.reopened": {"reopened an appeal", "appeal"}, "appeal.accepted": {"accepted an appeal", "accept"}, "appeal.rejected": {"declined an appeal", "decline"}, "appeal.close": {"closed an appeal", "lock"}, "appeal.closed": {"closed an appeal", "lock"}, "appeal.settings.update": {"updated the appeal settings", "settings"},
	"ticket.open": {"opened a ticket", "ticket"}, "ticket.reply": {"replied to a ticket", "reply"}, "ticket.resolve": {"closed a ticket", "lock"}, "ticket.cancel": {"cancelled a ticket", "lock"},
	"honeypot.trigger": {"recorded a honeypot trigger", "shield"}, "v4_import.batch": {"imported historical records", "history"},
}
