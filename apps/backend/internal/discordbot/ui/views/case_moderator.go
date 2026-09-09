package views

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

const casePageSize = 10

// CaseDetailPage keeps long staff context and history in Discord while retaining
// correction and recovery controls on each page. Render before measuring because
// application emoji expansion contributes to Discord's message limit.
func CaseDetailPage(detail *quack.CaseDetailResponse, page int, applicationID string) ui.Message {
	message := CaseDetailMessage(detail)
	message.Ephemeral = true
	if detail == nil {
		return message
	}
	pages := ui.TextPages(discordtext.Resolve(message.Content, applicationID), 1750)
	if page < 1 {
		page = 1
	}
	if page > len(pages) {
		page = len(pages)
	}
	message.Content = pages[page-1]
	if len(pages) > 1 {
		message.Content += fmt.Sprintf("\n\n-# Case #%d · Page %d/%d", detail.CaseNumber, page, len(pages))
		controls, _ := ui.Pagination("case", "detail", fmt.Sprintf("%d|%s", page, detail.ID), page, len(pages))
		message.Components = append(message.Components, controls...)
	}
	return message
}

// CaseDetailMessage retains authorized context and recovery controls while
// describing the decision as a conversation rather than a field grid.
func CaseDetailMessage(detail *quack.CaseDetailResponse) ui.Message {
	if detail == nil {
		return ui.Signal("error", "That case couldn’t be found. Check its number and try again.", true)
	}
	lead := fmt.Sprintf("Case #%d · <@%s>", detail.CaseNumber, detail.TargetDiscordUserID)
	if detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Name != "" {
		lead += " · **" + ui.PlainText(detail.TemplateSnapshot.Template.Name) + "**"
	}
	icon := "case"
	parts := []string{}
	if detail.Validity == model.CaseValidityVoided {
		icon = "case_void"
		lead = "**Voided** · " + lead
		parts = append(parts, "This case was voided and no longer counts toward escalation.")
		if detail.VoidedReason != "" {
			parts = append(parts, ui.Quote(ui.PlainText(detail.VoidedReason)))
		}
	}
	outcome := []string{staffActionSummary(detail.Actions)}
	if detail.Source == model.CaseSourceV4Import {
		outcome = []string{"Imported v4 history: " + historicalCaseLabel(detail.CaseResponse) + ". No new action was performed."}
		if detail.ModeratorDiscordUserID != "" {
			parts = append(parts, "Original moderator: <@"+detail.ModeratorDiscordUserID+">")
		}
		if link := historicalContextLink(detail.ContextURL); link != "" {
			parts = append(parts, link)
		}
	}
	if detail.Notification != nil {
		outcome = append(outcome, notificationDeliverySentence(string(detail.Notification.Status)))
	}
	if detail.Validity != model.CaseValidityVoided && detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Appealable {
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
	return boundedCaseHistoryMessage(list, page, targetID, "")
}

// boundedCaseHistoryMessage budgets the complete page after Markdown escaping,
// including profile totals. Only display names shrink; all ten rows and controls
// remain native. Each icon token reserves 64 extra units for application emojis.
func boundedCaseHistoryMessage(list *quack.CaseListResponse, page int, targetID, summary string) ui.Message {
	for limit := 100; ; limit-- {
		message := caseHistoryMessageWithLabels(list, page, targetID, limit)
		message.Content += summary
		units := len(utf16.Encode([]rune(message.Content))) + 64*strings.Count(message.Content, "{{quack:")
		if units <= 2000 || limit == 0 {
			return message
		}
	}
}

// caseHistoryMessageWithLabels renders a complete page using a bounded display
// label; original names remain available in case detail and are never modified.
func caseHistoryMessageWithLabels(list *quack.CaseListResponse, page int, targetID string, labelLimit int) ui.Message {
	if page < 1 {
		page = 1
	}
	rows := make([]string, 0)
	if list != nil {
		for _, item := range list.Cases {
			summary := "Case recorded"
			if item.RuleName != "" {
				summary = ui.PlainText(ui.TruncateRunes(item.RuleName, labelLimit))
				if len([]rune(item.RuleName)) > labelLimit {
					summary += "…"
				}
			}
			row := fmt.Sprintf("**#%d**  <@%s> · %s", item.CaseNumber, item.TargetDiscordUserID, summary)
			if item.Source == model.CaseSourceV4Import {
				row = fmt.Sprintf("**#%d**  <@%s> · %s · **Imported v4**", item.CaseNumber, item.TargetDiscordUserID, historicalCaseLabel(item))
			}
			if item.Validity == model.CaseValidityVoided {
				row += " · **Voided**"
			}
			if date := ui.RelativeTime(item.CreatedAt); date != "" {
				row += "\n-# " + date
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

// CaseProfileMessage combines a paginated staff history with persisted all-time
// counts. Imported history remains visible but is never described as escalation.
func CaseProfileMessage(profile *quack.CaseProfileResponse, page int, targetID string) ui.Message {
	if profile == nil {
		message := CaseListMessage(nil, page, targetID)
		message.Ephemeral = true
		return message
	}
	summary := fmt.Sprintf("\n-# %d total · %d active · %d voided", profile.Summary.Total, profile.Summary.ByValidity[string(model.CaseValidityValid)], profile.Summary.ByValidity[string(model.CaseValidityVoided)])
	message := boundedCaseHistoryMessage(&quack.CaseListResponse{Cases: profile.Cases, Total: profile.Total, Limit: profile.Limit, Offset: profile.Offset}, page, targetID, summary)
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
		return "Warning recorded."
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := caseActionSentence(action.CaseActionResponse)
		if action.LastErrorCode != "" {
			row += "\n" + safeFailure(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// contextSummary formats staff-only context and escapes user formatting.
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
			label = evidenceCaptureLabel(item.CaptureOutcome)
		}
		rows = append(rows, label)
		if item.Content != "" {
			rows = append(rows, ui.Quote(ui.PlainText(item.Content)))
		}
		if warning := evidenceSnapshotWarning(item); warning != "" {
			rows = append(rows, "{{quack:warn}} "+ui.PlainText(warning))
		}
		for _, attachment := range item.Attachments {
			url := attachment.PreservedURL
			if url == "" {
				url = attachment.OriginalURL
			}
			rows = append(rows, fmt.Sprintf("[%s](%s) · %s", ui.PlainText(attachment.Filename), url, evidenceCopyLabel(attachment.CopyOutcome)))
			if attachment.Warning != "" {
				rows = append(rows, "{{quack:warn}} "+ui.PlainText(evidenceWarningCopy(attachment.Warning)))
			}
		}
	}
	return strings.Join(rows, "\n")
}

// evidenceCopyLabel describes only statuses produced by managed copying; unknown
// historical values cannot establish that a durable file was saved.
func evidenceCopyLabel(outcome string) string {
	switch outcome {
	case "preserved":
		return "Saved copy"
	case "metadata_only":
		return "No confirmed copy"
	default:
		return "Copy status unavailable"
	}
}

// evidenceSnapshotWarning removes full warning duplicates repeated beneath their
// attachments. Delimiter boundaries retain distinct snapshot failures, including
// truncation, without discarding useful unknown historical diagnostics.
func evidenceSnapshotWarning(item quack.CaseEvidenceResponse) string {
	warning := "; " + strings.TrimSpace(item.CaptureWarning) + "; "
	for _, attachment := range item.Attachments {
		if attachment.Warning == "" {
			continue
		}
		duplicate := "; " + attachment.Warning + "; "
		for strings.Contains(warning, duplicate) {
			warning = strings.Replace(warning, duplicate, "; ", 1)
		}
	}
	warning = strings.TrimSuffix(strings.TrimPrefix(warning, "; "), "; ")
	if warning == "" {
		return ""
	}
	if mapped := evidenceWarningCopy(warning); mapped != warning {
		return mapped
	}
	// Capture joins independent warnings with semicolons. Map those known parts
	// while preserving unknown text and its original delimiters.
	parts := strings.Split(warning, "; ")
	for i := range parts {
		parts[i] = evidenceWarningCopy(parts[i])
	}
	return strings.Join(parts, "; ")
}

// evidenceWarningCopy translates known capture diagnostics at the presentation
// boundary. Unrecognized warnings remain visible rather than becoming generic.
func evidenceWarningCopy(warning string) string {
	switch warning {
	case "managed evidence channel is unavailable":
		return "The evidence channel is unavailable. Ask an administrator to check it."
	case "attachment exceeds the managed copy size limit":
		return "This file is too large to save."
	case "attachment type is not eligible for managed copying":
		return "This file type cannot be saved."
	case "attachment copy failed; original metadata retained":
		return "Quack could not save this file. The original link may stop working."
	case "attachment copy could not be confirmed; original metadata retained":
		return "Quack could not confirm a saved copy. The original link may stop working."
	case "message content snapshot was truncated":
		return "Only part of the message text was saved."
	case "embed snapshot was truncated":
		return "Some message embeds were not saved."
	case "attachment snapshot was truncated", "total attachment snapshot limit reached", "total attachment snapshot was truncated":
		return "Some files were not saved because the capture limit was reached."
	case "linked message was deleted or does not exist":
		return "The original message was deleted or could not be found."
	case "linked message is unavailable":
		return "The original message is unavailable."
	default:
		return warning
	}
}

// evidenceCaptureLabel describes stored capture outcomes without exposing raw
// storage values. Unknown historical outcomes retain a neutral evidence heading.
func evidenceCaptureLabel(outcome string) string {
	switch outcome {
	case "uploaded":
		return "Uploaded file"
	case "captured":
		return "Captured message"
	case "unavailable":
		return "Capture unavailable"
	case "deleted":
		return "Message deleted or missing"
	case "inaccessible":
		return "Message inaccessible"
	default:
		return "Evidence"
	}
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

// CaseEvidencePage keeps preserved evidence in native staff-only pages. Icons are
// resolved before measuring so custom emoji do not unexpectedly force a file.
func CaseEvidencePage(detail *quack.CaseDetailResponse, page int, applicationID string) ui.Message {
	if detail == nil {
		return ui.Signal("error", "That case could not be found.", true)
	}
	body := evidenceSummary(detail.Evidence)
	if body == "" {
		body = "No evidence has been added yet."
	}
	pages := ui.TextPages(discordtext.Resolve(body, applicationID), 1600)
	if page < 1 {
		page = 1
	}
	if page > len(pages) {
		page = len(pages)
	}
	body = pages[page-1] + fmt.Sprintf("\n\nAdd a screenshot with `/case evidence case:%d file:` or use its `message_link` option.", detail.CaseNumber)
	message := ui.Conversation("evidence", fmt.Sprintf("Evidence for case #%d", detail.CaseNumber), "", body, fmt.Sprintf("Page %d/%d", page, len(pages)), true)
	if len(pages) > 1 {
		message.Components, _ = ui.Pagination("case", "evidence", fmt.Sprintf("%d|%s", page, detail.ID), page, len(pages))
	}
	return message
}

// historicalCaseLabel describes the original recorded event without manufacturing
// an execution result. Imported metadata is display-only and cannot trigger work.
func historicalCaseLabel(item quack.CaseResponse) string {
	metadata, _ := item.Metadata.(map[string]any)
	legacy, _ := metadata["v4"].(map[string]any)
	action, _ := legacy["action_type"].(string)
	switch action {
	case "warning":
		return "Warning"
	case "ban":
		return "Ban"
	case "kick":
		return "Kick"
	case "unban":
		return "Unban"
	case "timeout":
		return "Timeout"
	case "message_delete":
		return "Message deletion"
	default:
		return "Historical case"
	}
}

// historicalContextLink exposes the preserved source URL without allowing its
// contents to break Markdown or create a non-web link. Storage retains the original.
func historicalContextLink(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	safe := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(parsed.String())
	return "[View original context](" + safe + ")"
}
