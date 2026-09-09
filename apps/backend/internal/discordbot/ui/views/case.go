package views

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseCreated groups the case created state used to keep this package's responsibilities explicit.
type CaseCreated struct {
	MemberReason string
	Case         *quack.CaseResponse
	Template     *quack.TemplateResponse
}

// CaseCreatedMessage announces the saved decision without exposing staff identity,
// private context, evidence, or internal action diagnostics to the public channel.
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
	if created.EvidenceIncomplete {
		status += "\nSome evidence couldn’t be saved. Staff can check **View evidence**."
	}
	message := ui.Conversation(icon, FormatCaseCreated(result), ui.PlainText(ui.TruncateRunes(result.MemberReason, 350)), status, strings.Join(meta, " · "), false)
	message.Components = []discordgo.MessageComponent{ui.Row(casePrimaryControls(created.ID, created.TargetDiscordUserID, created.Validity == model.CaseValidityVoided)...)}
	return message
}

// FormatCaseCreated puts the affected member and rule in a single natural sentence.
func FormatCaseCreated(result CaseCreated) string {
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
