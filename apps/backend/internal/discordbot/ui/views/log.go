package views

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// logAttachment renders available HTTPS download links without implying that
// the logging module archives binaries or extends Discord's URL lifetime.
type logAttachment struct {
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// label keeps missing or malformed URLs as plain filenames and prevents URL or
// filename content from escaping the intended Markdown link.
func (a logAttachment) label() string {
	name := ui.PlainText(a.Filename)
	parsed, err := url.Parse(a.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || strings.ContainsAny(a.URL, "<>\r\n\t ") {
		return name
	}
	return "[" + name + "](<" + parsed.String() + ">)"
}

// StaffLogMessage presents the logging module's already-redacted payload at the Discord boundary.
// The module retains its structured JSON contract and controls which content may be included.
func StaffLogMessage(payload string) ui.Message {
	var event struct {
		Messages []struct {
			MessageID   string          `json:"message_id"`
			ActorID     string          `json:"actor_id"`
			Content     string          `json:"content"`
			Attachments []logAttachment `json:"attachments"`
			EmbedTypes  []string        `json:"embed_types"`
		} `json:"messages"`
		BeforeKnown       *bool             `json:"before_known"`
		BeforeAttachments []logAttachment   `json:"before_attachments"`
		Type              string            `json:"event"`
		ChannelID         string            `json:"channel_id"`
		MessageID         string            `json:"message_id"`
		ActorID           string            `json:"actor_id"`
		Before            string            `json:"before"`
		After             string            `json:"after"`
		Attachments       []logAttachment   `json:"attachments"`
		EmbedTypes        []string          `json:"embed_types"`
		Metadata          map[string]string `json:"metadata"`
	}
	if json.Unmarshal([]byte(payload), &event) != nil {
		return ui.Signal("info", "An event was recorded, but its details are unavailable.", false)
	}
	actor := "A member"
	if event.ActorID != "" {
		actor = "<@" + event.ActorID + ">"
	}
	target := "A member"
	if event.Metadata["target_id"] != "" {
		target = "<@" + event.Metadata["target_id"] + ">"
	}
	var lead string
	switch event.Type {
	case "message_edit":
		lead = "A message from " + actor + " was edited."
	case "message_delete":
		lead = "A message from " + actor + " was deleted."
	case "message_bulk_delete":
		lead = "Messages were deleted."
		if count := event.Metadata["message_count"]; count != "" {
			lead = ui.PlainText(count) + " messages were deleted."
		}
	case "member_join":
		lead = actor + " joined the server."
	case "member_leave":
		lead = actor + " left the server."
	case "discord_ban":
		lead = target + " was banned by " + actor + "."
	case "discord_unban":
		lead = target + " was unbanned by " + actor + "."
	case "guild_change":
		lead = "Server settings changed."
	case "channel_change":
		lead = "A channel changed."
		switch event.Metadata["operation"] {
		case "created":
			lead = "A channel was created."
		case "deleted":
			lead = "A channel was deleted."
		case "updated":
			lead = "Channel settings changed."
		}
		if name := event.Metadata["name"]; name != "" {
			lead = strings.TrimSuffix(lead, ".") + ": " + ui.PlainText(name) + "."
		}
	default:
		lead = "Server activity was recorded."
	}
	if event.ChannelID != "" && event.Type != "channel_change" {
		lead = strings.TrimSuffix(lead, ".") + " in <#" + event.ChannelID + ">."
	}
	parts := []string{}
	if event.Before != "" && len(event.Messages) == 0 {
		before := ui.Quote(ui.PlainText(event.Before))
		if event.Type == "message_edit" {
			before = "Before:\n" + before
		}
		parts = append(parts, before)
	}
	if event.Type == "message_edit" && event.Before == "" && event.BeforeKnown != nil {
		if *event.BeforeKnown {
			parts = append(parts, "Before: no text.")
		} else {
			parts = append(parts, "Previous text was not available.")
		}
	}
	if event.After != "" {
		parts = append(parts, "After:\n"+ui.Quote(ui.PlainText(event.After)))
	}
	if event.Type == "message_edit" && event.After == "" && event.BeforeKnown != nil {
		parts = append(parts, "After: no text.")
	}
	if event.Type == "message_edit" && len(event.BeforeAttachments) > 0 {
		var names []string
		for _, attachment := range event.BeforeAttachments {
			names = append(names, attachment.label())
		}
		parts = append(parts, "Files before: "+strings.Join(names, ", "))
		if len(event.Attachments) == 0 {
			parts = append(parts, "Files after: none.")
		}
	}
	if len(event.Attachments) > 0 && len(event.Messages) == 0 {
		names := []string{}
		for _, attachment := range event.Attachments {
			names = append(names, attachment.label())
		}
		label := "Files: "
		if event.Type == "message_edit" {
			label = "Files after: "
		}
		parts = append(parts, label+strings.Join(names, ", "))
	}
	if len(event.EmbedTypes) > 0 && len(event.Messages) == 0 {
		parts = append(parts, "Included embeds: "+ui.PlainText(strings.Join(event.EmbedTypes, ", "))+".")
	}
	for _, message := range event.Messages {
		author := "Unknown author"
		if message.ActorID != "" {
			author = "<@" + message.ActorID + ">"
		}
		record := author + " · Message " + ui.PlainText(message.MessageID)
		if message.Content != "" {
			record += "\n" + ui.Quote(ui.PlainText(message.Content))
		}
		if len(message.Attachments) > 0 {
			names := make([]string, 0, len(message.Attachments))
			for _, attachment := range message.Attachments {
				names = append(names, attachment.label())
			}
			record += "\nFiles: " + strings.Join(names, ", ")
		}
		if len(message.EmbedTypes) > 0 {
			record += "\nIncluded embeds: " + ui.PlainText(strings.Join(message.EmbedTypes, ", ")) + "."
		}
		parts = append(parts, record)
	}
	if reason := event.Metadata["reason"]; reason != "" {
		parts = append(parts, "Reason:\n"+ui.Quote(ui.PlainText(reason)))
	}
	if event.Type == "guild_change" && event.Metadata["name"] != "" {
		parts = append(parts, "Server: "+ui.PlainText(event.Metadata["name"]))
	}
	if event.Type == "message_bulk_delete" && event.Metadata["cached_count"] != "" {
		parts = append(parts, "Messages with saved content: "+ui.PlainText(event.Metadata["cached_count"])+".")
	}
	meta := ""
	if event.MessageID != "" {
		meta = "Message " + ui.PlainText(event.MessageID)
	}
	icon := map[string]string{"message_edit": "edit", "message_delete": "delete", "message_bulk_delete": "delete", "member_join": "join", "member_leave": "leave", "discord_ban": "ban", "discord_unban": "unban", "guild_change": "settings", "channel_change": "settings"}[event.Type]
	if icon == "" {
		icon = "info"
	}
	return ui.Conversation(icon, lead, "", strings.Join(parts, "\n\n"), meta, false)
}

// AuditMirrorMessage summarizes a redacted staff event with human case context.
// Internal storage identifiers stay in delivery state and controls, not the copy.
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
		body = fmt.Sprintf("%s couldn’t %s.", actor, ui.PlainText(action))
	}
	if message.ActionType != "" {
		label := ui.PlainText(message.ActionType.Label())
		switch message.Action {
		case "case_action.succeeded":
			body = fmt.Sprintf("%s action completed.", label)
		case "case_action.failed":
			body = fmt.Sprintf("%s action failed.", label)
		case "case_action.skipped":
			body = fmt.Sprintf("%s action skipped.", label)
		}
	}
	if message.ReversalNoop && message.Action == "case_action.succeeded" {
		body = "The punishment had already ended."
	}
	context := ""
	if message.CaseID != "" {
		context = fmt.Sprintf("Case #%d", message.CaseNumber)
		if message.TargetDiscordUserID != "" {
			context += " · <@" + message.TargetDiscordUserID + ">"
		}
		if message.TemplateName != "" {
			context += " · " + ui.PlainText(message.TemplateName)
		}
	}
	if message.Action == string(model.AuditActionCaseCreate) && message.SelectedOutcome != "" {
		if message.SelectedLevelName != "" {
			context += "\nLevel: " + ui.PlainText(message.SelectedLevelName)
		}
		context += "\nOutcome: " + ui.PlainText(message.SelectedOutcome)
	}
	meta := []string{}
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
	// Every supporting line stays directly beneath the event in Discord subtext.
	// A single content block avoids the visual gap of the general conversation layout.
	details := []string{context, ui.PlainText(message.FailureReason)}
	if strings.HasPrefix(message.Action, "case_action.") {
		details = append(details, "By "+actor)
	}
	details = append(details, strings.Join(meta, " · "))
	for _, detail := range details {
		for _, line := range strings.Split(detail, "\n") {
			if strings.TrimSpace(line) != "" {
				body += "\n-# " + line
			}
		}
	}
	notice := ui.Signal(icon, body, false)
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
	"guild_settings.update": {"updated Quack settings", "settings"},
	"case_action.attempt":   {"started an action attempt", "running"}, "case_action.succeeded": {"completed a Discord action", "success"}, "case_action.failed": {"recorded a failed Discord action", "error"}, "case_action.skipped": {"skipped a Discord action", "info"}, "case_action.retrying": {"scheduled another action attempt", "retry"}, "case_action.retry": {"queued another action attempt", "retry"}, "case_action.dismiss": {"dismissed an action failure", "review"}, "case_action.reverse": {"queued an action reversal", "retry"}, "case_action.recovered": {"recovered a stalled action", "retry"},
	"case_notification.sent": {"sent the member a DM", "message"}, "case_notification.failed": {"couldn’t deliver the member’s DM", "error"},
	"appeal.submit": {"submitted an appeal", "appeal"}, "appeal.information.submit": {"added information to an appeal", "reply"}, "appeal.information_requested": {"asked for more information on an appeal", "reply"}, "appeal.reopened": {"reopened an appeal", "appeal"}, "appeal.accepted": {"accepted an appeal", "accept"}, "appeal.rejected": {"declined an appeal", "decline"}, "appeal.close": {"closed an appeal", "lock"}, "appeal.closed": {"closed an appeal", "lock"}, "appeal.settings.update": {"updated the appeal settings", "settings"},
	"ticket.open": {"opened a ticket", "ticket"}, "ticket.reply": {"replied to a ticket", "reply"}, "ticket.resolve": {"closed a ticket", "lock"}, "ticket.cancel": {"cancelled a ticket", "lock"},
	"honeypot.trigger": {"recorded a honeypot trigger", "shield"}, "v4_import.batch": {"imported historical records", "history"},
}
