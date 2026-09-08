package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// AuditMirrorMessage summarizes a redacted staff event; durable identifiers stay in a quiet footer.
func AuditMirrorMessage(message quack.AuditMirrorMessage) ui.Message {
	actor := "Quack"
	if message.ActorDiscordUserID != "" && message.ActorDiscordUserID != "quack-system" {
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
	if message.ActionType != "" {
		label := ui.PlainText(message.ActionType.Label())
		switch message.Action {
		case "case_action.succeeded":
			body = fmt.Sprintf("%s completed **%s**.", actor, label)
		case "case_action.failed":
			body = fmt.Sprintf("%s could not complete **%s**.", actor, label)
		case "case_action.skipped":
			body = fmt.Sprintf("%s skipped **%s**.", actor, label)
		}
	}
	context := ""
	if message.CaseID != "" {
		context = fmt.Sprintf("Case #%d · <@%s>", message.CaseNumber, message.TargetDiscordUserID)
		if message.TemplateName != "" {
			context += " · " + ui.PlainText(message.TemplateName)
		}
	}
	if message.Action == string(model.AuditActionCaseCreate) && message.SelectedOutcome != "" {
		if message.SelectedLevelName != "" {
			context += "\nSelected level: **" + ui.PlainText(message.SelectedLevelName) + "**"
		}
		context += "\nSelected outcome: **" + ui.PlainText(message.SelectedOutcome) + "**"
	}
	meta := []string{}
	if message.ResourceID != "" && message.CaseID == "" {
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
	notice := ui.Conversation(icon, body, ui.PlainText(message.FailureReason), context, strings.Join(meta, " · "), false)
	if message.RetryExecutionID != "" && message.Result == model.AuditResultFailure {
		id, err := ui.EncodeCustomID(ui.CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: message.RetryExecutionID})
		if err == nil {
			notice.Components = []discordgo.MessageComponent{ui.Row(ui.Button(id, "Retry action", discordgo.SecondaryButton, false))}
		}
	}
	return notice
}

// auditPhrase maps stable audit contracts to human actions and a matching icon.
type auditPhrase struct{ verb, icon string }

// auditPhrases covers the core mirror events without changing their redacted data contract.
var auditPhrases = map[string]auditPhrase{
	"case.update": {"updated a case", "edit"}, "case.create": {"added a case", "case_add"}, "case.void": {"voided a case", "case_void"}, "case.void.appeal": {"voided a case after an appeal", "case_void"},
	"case_template.create": {"created a template", "spark"}, "case_template.update": {"updated a template", "edit"}, "case_template.archive": {"archived a template", "lock"}, "case_template.restore": {"restored a template", "unlock"}, "case_template.import": {"imported templates", "case_add"}, "case_template.export": {"exported templates", "case"},
	"guild_settings.update": {"updated the server settings", "settings"},
	"case_action.attempt":   {"started an action attempt", "running"}, "case_action.succeeded": {"completed a Discord action", "success"}, "case_action.failed": {"recorded a failed Discord action", "error"}, "case_action.skipped": {"skipped a Discord action", "info"}, "case_action.retrying": {"scheduled another action attempt", "retry"}, "case_action.retry": {"queued another action attempt", "retry"}, "case_action.dismiss": {"dismissed an action failure", "review"}, "case_action.reverse": {"queued an action reversal", "retry"}, "case_action.recovered": {"recovered a stalled action", "retry"},
	"case_notification.sent": {"sent the member a DM", "message"}, "case_notification.failed": {"couldn’t deliver the member’s DM", "error"},
	"appeal.submit": {"submitted an appeal", "appeal"}, "appeal.information.submit": {"added information to an appeal", "reply"}, "appeal.information_requested": {"asked for more information on an appeal", "reply"}, "appeal.reopened": {"reopened an appeal", "appeal"}, "appeal.accepted": {"accepted an appeal", "accept"}, "appeal.rejected": {"declined an appeal", "decline"}, "appeal.close": {"closed an appeal", "lock"}, "appeal.closed": {"closed an appeal", "lock"}, "appeal.settings.update": {"updated the appeal settings", "settings"},
	"ticket.open": {"opened a ticket", "ticket"}, "ticket.reply": {"replied to a ticket", "reply"}, "ticket.resolve": {"closed a ticket", "lock"}, "ticket.cancel": {"cancelled a ticket", "lock"},
	"honeypot.trigger": {"recorded a honeypot trigger", "shield"}, "v4_import.batch": {"imported historical records", "history"},
}
