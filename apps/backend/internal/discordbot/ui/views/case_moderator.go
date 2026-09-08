package views

import (
	"fmt"
	"math"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

const casePageSize = 10

// CaseDetailMessage retains authorized context and recovery controls while
// describing the decision as a conversation rather than a field grid.
func CaseDetailMessage(detail *quack.CaseDetailResponse) ui.Message {
	if detail == nil {
		return ui.Signal("error", "That case couldn’t be found. Check its number and try again.", true)
	}
	lead := "Case for <@" + detail.TargetDiscordUserID + ">"
	if detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Name != "" {
		lead += " for **" + ui.PlainText(detail.TemplateSnapshot.Template.Name) + "**"
	}
	icon := "case"
	parts := []string{}
	if detail.Validity == model.CaseValidityVoided {
		icon = "case_void"
		parts = append(parts, "This case was voided and no longer counts toward escalation.")
		if detail.VoidedReason != "" {
			parts = append(parts, ui.Quote(ui.PlainText(detail.VoidedReason)))
		}
	}
	outcome := []string{staffActionSummary(detail.Actions)}
	if detail.Notification != nil {
		outcome = append(outcome, notificationDeliverySentence(string(detail.Notification.Status)))
	}
	if detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Appealable {
		outcome = append(outcome, "The member can appeal this case.")
	}
	parts = append(parts, strings.Join(outcome, " "))
	for _, context := range []string{contextSummary(detail.ContextValues), evidenceSummary(detail.Evidence), eventSummary(detail.Events)} {
		if context != "" {
			parts = append(parts, context)
		}
	}
	meta := []string{fmt.Sprintf("Case #%d", detail.CaseNumber)}
	if detail.SelectedLevel != nil {
		meta = append(meta, ui.PlainText(detail.SelectedLevel.Name))
	}
	if date := ui.RelativeTime(detail.CreatedAt); date != "" {
		meta = append(meta, "Created "+date)
	}
	message := ui.Conversation(icon, lead+".", ui.PlainText(detail.Reason), strings.Join(parts, "\n\n"), strings.Join(meta, " · "), false)
	message.Components = caseDetailComponents(detail)
	return message
}

// notificationDeliverySentence distinguishes delivery from enforcement.
func notificationDeliverySentence(status string) string {
	switch status {
	case "sent":
		return "The member was sent a DM."
	case "failed":
		return "The member’s DM couldn’t be delivered."
	case "pending", "prepared", "claimed":
		return "The member’s DM is queued."
	case "sending", "running":
		return "The member’s DM is being sent."
	case "skipped":
		return "No DM was sent to the member."
	default:
		return "The member’s DM status is **" + ui.PlainText(strings.ReplaceAll(status, "_", " ")) + "**."
	}
}

// CaseListMessage renders one stable case page and its navigation controls.
func CaseListMessage(list *quack.CaseListResponse, page int, targetID string) ui.Message {
	if page < 1 {
		page = 1
	}
	rows := make([]string, 0)
	if list != nil {
		for _, item := range list.Cases {
			summary := "Case recorded"
			if item.SelectedLevel != nil {
				summary = ui.PlainText(ui.TruncateRunes(item.SelectedLevel.Name, 100))
			}
			row := fmt.Sprintf("**#%d**  <@%s> · %s", item.CaseNumber, item.TargetDiscordUserID, summary)
			if item.Validity == model.CaseValidityVoided {
				row += " · **Voided**"
			}
			if date := ui.RelativeTime(item.CreatedAt); date != "" {
				row += "\n" + date
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No cases found.")
	}
	total := int64(0)
	if list != nil {
		total = list.Total
	}
	totalPages := int(math.Ceil(float64(total) / casePageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	payload := fmt.Sprintf("%d|%s", page, targetID)
	prefix := "list"
	if targetID != "" {
		prefix = "user"
	}
	components, _ := ui.Pagination("case", prefix, payload, page, totalPages)
	lead := "Here are the latest cases."
	if targetID != "" {
		lead = "Here’s the case history for <@" + targetID + ">."
	}
	if total == 0 {
		lead = "No cases yet."
		if targetID != "" {
			lead = "No cases found for <@" + targetID + ">."
		}
		rows = nil
	}
	message := ui.Conversation("history", lead, "", strings.Join(rows, "\n\n"), fmt.Sprintf("Page %d/%d · %d total", page, totalPages, total), false)
	message.Components = components
	return message
}

// FailedActionMessage renders the active recovery queue with real retry, dismiss, and void controls.
func FailedActionMessage(result *model.FailedCaseActionResult, page int) ui.Message {
	if page < 1 {
		page = 1
	}
	rows := []string{}
	components := []discordgo.MessageComponent{}
	if result != nil {
		for index, item := range result.Executions {
			rows = append(rows, fmt.Sprintf("`%s` · %s · %s", item.ID, item.ActionType.Label(), safeFailure(item.LastErrorCode)))
			if index == 0 {
				retryID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: item.ID})
				dismissID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "dismiss", Version: "v1", Payload: item.ID})
				voidID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void", Version: "v1", Payload: item.CaseID})
				components = append(components, ui.Row(ui.Button(retryID, "Retry first", discordgo.SecondaryButton, false), ui.Button(dismissID, "Dismiss first", discordgo.SecondaryButton, false), ui.Button(voidID, "Void case", discordgo.DangerButton, false)))
			}
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No action failures need review.")
	}
	total := int64(0)
	if result != nil {
		total = result.Total
	}
	totalPages := int(math.Ceil(float64(total) / casePageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	pagination, _ := ui.Pagination("case", "failures", fmt.Sprintf("%d", page), page, totalPages)
	components = append(components, pagination...)
	icon, lead := "success", "No action failures need review."
	if result != nil && len(result.Executions) > 0 {
		icon, lead = "error", "These actions need a hand."
	} else {
		rows = nil
	}
	message := ui.Conversation(icon, lead, "", strings.Join(rows, "\n\n"), fmt.Sprintf("Page %d/%d · %d active", page, totalPages, total), false)
	message.Components = components
	return message
}

// caseDetailComponents retains explicit recovery controls for authorized staff.
func caseDetailComponents(detail *quack.CaseDetailResponse) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{ui.Row(casePrimaryControls(detail.ID, detail.TargetDiscordUserID, detail.Validity == model.CaseValidityVoided)...)}
	buttons := []discordgo.MessageComponent{}
	for _, action := range detail.Actions {
		if action.Status == model.ActionExecutionFailed {
			buttons = append(buttons, ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: action.ID}), "Retry", discordgo.SecondaryButton, false), ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "dismiss", Version: "v1", Payload: action.ID}), "Dismiss", discordgo.SecondaryButton, false))
			break
		}
		if action.Status == model.ActionExecutionSucceeded && (action.ActionType == model.ActionTimeoutUser || action.ActionType == model.ActionBanUser) {
			reversal := model.ActionRemoveTimeout
			if action.ActionType == model.ActionBanUser {
				reversal = model.ActionUnbanUser
			}
			payload := strings.Join([]string{detail.ID, action.ID, string(reversal)}, "|")
			buttons = append(buttons, ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "reverse", Version: "v1", Payload: payload}), "Reverse action", discordgo.SecondaryButton, false))
			break
		}
	}
	if len(buttons) > 0 {
		rows = append(rows, ui.Row(buttons...))
	}
	return rows
}

// staffActionSummary preserves enforcement and failure details in readable sentences.
func staffActionSummary(actions []quack.CaseActionDetailResponse) string {
	if len(actions) == 0 {
		return "Recorded without a Discord action."
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := ui.ActionSentence(action.ActionType, action.Status)
		if action.LastErrorCode != "" {
			row += "\n" + safeFailure(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// contextSummary keeps member-visible context bounded and escapes user formatting.
func contextSummary(values []quack.CaseContextValueResponse) string {
	rows := make([]string, 0, len(values))
	for _, value := range values {
		rows = append(rows, ui.Quote(ui.PlainText(value.Label)+" — "+ui.PlainText(fmt.Sprint(value.Value))))
	}
	return strings.Join(rows, "\n")
}

// evidenceSummary uses descriptive links while preserving capture outcomes.
func evidenceSummary(evidence []quack.CaseEvidenceResponse) string {
	rows := make([]string, 0, len(evidence))
	for _, item := range evidence {
		label := ""
		if item.MessageURL != "" {
			label = "[View message](" + item.MessageURL + ")"
		}
		if label == "" {
			label = item.CaptureOutcome
		}
		rows = append(rows, label)
		if item.Content != "" {
			rows = append(rows, ui.Quote(ui.PlainText(item.Content)))
		}
		if item.CaptureWarning != "" {
			rows = append(rows, "{{quack:warn}} "+ui.PlainText(item.CaptureWarning))
		}
		for _, attachment := range item.Attachments {
			url := attachment.PreservedURL
			if url == "" {
				url = attachment.OriginalURL
			}
			rows = append(rows, fmt.Sprintf("[%s](%s) · %s", ui.PlainText(attachment.Filename), url, ui.PlainText(attachment.CopyOutcome)))
			if attachment.Warning != "" {
				rows = append(rows, "{{quack:warn}} "+ui.PlainText(attachment.Warning))
			}
		}
	}
	return strings.Join(rows, "\n")
}

// eventSummary shows the latest six history entries with localized event times.
func eventSummary(events []quack.CaseEventResponse) string {
	start := 0
	if len(events) > 6 {
		start = len(events) - 6
	}
	rows := make([]string, 0, len(events)-start)
	for _, event := range events[start:] {
		rows = append(rows, "-# "+strings.TrimSpace(ui.RelativeTime(event.CreatedAt)+" · "+ui.PlainText(event.Body)))
	}
	return strings.Join(rows, "\n")
}

// safeFailure exposes only a bounded diagnostic code, never raw Discord responses.
func safeFailure(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Discord action failed"
	}
	return ui.PlainText(strings.ReplaceAll(ui.TruncateRunes(strings.TrimSpace(code), 160), "_", " "))
}

// CaseEvidenceMessage shows the preserved staff record with an explicit upload entry point.
func CaseEvidenceMessage(detail *quack.CaseDetailResponse) ui.Message {
	if detail == nil {
		return ui.Signal("error", "That case could not be found.", true)
	}
	body := evidenceSummary(detail.Evidence)
	if body == "" {
		body = "No evidence has been added yet."
	}
	body += fmt.Sprintf("\n\nAdd a screenshot with `/case evidence case:%d file:` or use its `message_link` option.", detail.CaseNumber)
	return ui.Conversation("evidence", fmt.Sprintf("Evidence for case #%d", detail.CaseNumber), "", body, "", true)
}
