package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseCreated is the moderator-facing input for a freshly saved case.
type CaseCreated struct {
	MemberReason string
	Case         *quack.CaseResponse
	Template     *quack.TemplateResponse
}

// CaseCreatedMessage announces the saved decision and moderator context in the
// invoking channel. Member notifications use their own member-facing renderer.
func CaseCreatedMessage(result CaseCreated) ui.Message {
	if result.Case == nil {
		return ui.Signal("case_add", "Case added.", false)
	}
	created := result.Case
	meta := []string{fmt.Sprintf("Case #%d", created.CaseNumber)}
	if date := ui.RelativeTime(created.CreatedAt); date != "" {
		meta = append(meta, date)
	}
	icon := "case_add"
	if len(created.Actions) == 0 {
		icon = "warn"
	}
	for _, action := range created.Actions {
		if action.Status == model.ActionExecutionFailed {
			icon = "error"
			break
		}
		if action.Status == model.ActionExecutionPending || action.Status == model.ActionExecutionRunning || action.Status == model.ActionExecutionRetrying {
			icon = "pending"
		}
	}
	status := publicActionStatus(created.Actions)
	if created.ModeratorDiscordUserID != "" {
		status += "\nModerator: <@" + created.ModeratorDiscordUserID + ">"
	}
	if context := contextSummary(created.ContextValues); context != "" {
		status += "\n\n" + context
	}
	if created.EvidenceIncomplete {
		status += "\nSome evidence couldn’t be saved. Staff can check **View evidence**."
	}
	message := ui.Conversation(icon, formatCaseCreated(result), ui.PlainText(caseReceiptReason(result)), status, strings.Join(meta, " · "), false)
	message.Components = []discordgo.MessageComponent{ui.Row(casePrimaryControls(created.ID, created.TargetDiscordUserID, created.Validity == model.CaseValidityVoided)...)}
	pages := ui.TextPages(message.Content, 1750)
	if len(pages) > 1 {
		message.Content = pages[0]
		message.Components = append(message.Components, ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "view", Version: "v1", Payload: created.ID}), "View full case", discordgo.SecondaryButton, false)))
	}
	return message
}

// formatCaseCreated puts the affected member and rule in a single natural sentence.
func formatCaseCreated(result CaseCreated) string {
	if result.Case == nil {
		return "Case added."
	}
	lead := fmt.Sprintf("Case #%d · <@%s>", result.Case.CaseNumber, result.Case.TargetDiscordUserID)
	if name := caseTemplateDisplayName(result.Template); name != "" {
		lead += " · **" + ui.PlainText(name) + "**"
	}
	if result.Case.Validity == model.CaseValidityVoided {
		lead = "**Voided** · " + lead
	}
	return lead
}

// publicActionStatus never mistakes a queued action for completed enforcement.
func publicActionStatus(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return "Warning recorded."
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, caseActionSentence(action))
	}
	return strings.Join(parts, "\n")
}

// caseTemplateDisplayName prefers the admin-provided rule name, falling back to its slug.
func caseTemplateDisplayName(template *quack.TemplateResponse) string {
	if template == nil {
		return ""
	}
	name := strings.TrimSpace(template.Name)
	slug := strings.TrimSpace(template.Slug)
	switch {
	case name != "":
		return name
	default:
		return slug
	}
}

// casePrimaryControls keeps the same navigation and edit controls on case receipts and detail.
func casePrimaryControls(caseID, targetID string, voided bool) []discordgo.MessageComponent {
	button := func(action, payload, label string, style discordgo.ButtonStyle, disabled bool) discordgo.MessageComponent {
		return ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: action, Version: "v1", Payload: payload}), label, style, disabled)
	}
	return []discordgo.MessageComponent{
		button("edit_context", caseID, "Edit context", discordgo.SecondaryButton, false),
		button("evidence", caseID, "View evidence", discordgo.SecondaryButton, false),
		button("user_detail", targetID, "History", discordgo.SecondaryButton, false),
		button("void", caseID, "Void case", discordgo.DangerButton, voided),
	}
}

// CaseVoidedMessage distinguishes a saved void from asynchronous punishment removal.
func CaseVoidedMessage(item *quack.CaseResponse) ui.Message {
	status := "It stays in history and no longer counts toward escalation."
	for _, action := range item.Actions {
		if action.ActionType == model.ActionRemoveTimeout || action.ActionType == model.ActionUnbanUser {
			status += "\n" + publicActionStatus([]quack.CaseActionResponse{action})
		} else if action.Status == model.ActionExecutionRunning {
			status += "\nEnforcement is still finishing. Quack will try to undo any ban or timeout that succeeds."
		}
	}

	return ui.Conversation("case_void", fmt.Sprintf("Case #%d was voided.", item.CaseNumber), "", status, "", false)
}

// caseReceiptReason retains moderator reasons, falling back to the rule for older receipts.
func caseReceiptReason(result CaseCreated) string {
	if result.Case != nil && result.Case.Reason != "" {
		return result.Case.Reason
	}
	return result.MemberReason
}

// caseActionSentence adds the recorded expiry to completed timeouts so Discord
// localizes it for each reader. Other actions retain their accurate progress.
func caseActionSentence(action quack.CaseActionResponse) string {
	if action.ActionType == model.ActionTimeoutUser && action.Status == model.ActionExecutionSucceeded && action.TimeoutUntil != nil {
		return fmt.Sprintf("Timed out until <t:%d:f> (<t:%d:R>).", action.TimeoutUntil.Unix(), action.TimeoutUntil.Unix())
	}
	return ui.ActionSentence(action.ActionType, action.Status)
}

// CaseModeratorReceipt renders staff decision, context, and delivery feedback.
// View case rechecks live authority and exposes the complete paginated record.
func CaseModeratorReceipt(receipt *quack.CaseReceiptResponse) ui.Message {
	item := receipt.Case
	level := ""
	if item.SelectedLevel != nil {
		level = item.SelectedLevel.Name
	}
	lines := []string{staffActionSummary(receipt.Actions)}
	if context := contextSummary(item.ContextValues); context != "" {
		lines = append(lines, context)
	}
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
	message := ui.Conversation("case_add", lead, "", strings.Join(lines, "\n"), ui.PlainText(level), false)
	message.Content = ui.TextPages(message.Content, 1750)[0]
	message.Components = []discordgo.MessageComponent{ui.Row(casePrimaryControls(item.ID, item.TargetDiscordUserID, voided)...), ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "view", Version: "v1", Payload: item.ID}), "View case", discordgo.SecondaryButton, false))}
	return message
}
