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
	Case     *quack.CaseResponse
	Template *quack.TemplateResponse
}

// CaseCreatedMessage announces the saved decision without exposing staff identity,
// private context, evidence, or internal action diagnostics to the public channel.
func CaseCreatedMessage(result CaseCreated) ui.Message {
	if result.Case == nil {
		return ui.Signal("case_add", "Case added.", false)
	}
	created := result.Case
	meta := []string{fmt.Sprintf("Case #%d", created.CaseNumber)}
	if created.SelectedLevel != nil && strings.TrimSpace(created.SelectedLevel.Name) != "" {
		meta = append(meta, ui.PlainText(created.SelectedLevel.Name))
	}
	if date := ui.RelativeTime(created.CreatedAt); date != "" {
		meta = append(meta, date)
	}
	icon := "case_add"
	if len(created.Actions) == 0 {
		icon = "note"
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
		status += "\n{{quack:warn}} Some evidence could not be saved."
	}
	message := ui.Conversation(icon, FormatCaseCreated(result), "", status, strings.Join(meta, " · "), false)
	message.Components = []discordgo.MessageComponent{ui.Row(ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "edit_context", Version: "v1", Payload: created.ID}), "Add context", discordgo.SecondaryButton, false))}
	return message
}

// FormatCaseCreated puts the affected member and rule in a single natural sentence.
func FormatCaseCreated(result CaseCreated) string {
	if result.Case == nil {
		return "Case added."
	}
	lead := fmt.Sprintf("Case added for <@%s>", result.Case.TargetDiscordUserID)
	if name := caseTemplateDisplayName(result.Template); name != "" {
		lead += " for **" + ui.PlainText(name) + "**"
	}
	return lead + "."
}

// publicActionStatus never mistakes a queued action for completed enforcement.
func publicActionStatus(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return "Recorded without a Discord action."
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, ui.ActionSentence(action.ActionType, action.Status))
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
