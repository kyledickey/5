package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// enrichCase resolves case references through guild-scoped records. It exposes
// only identifiers and the snapshotted policy decision, never evidence or staff context.
func (w *AuditMirrorWorker) enrichCase(ctx context.Context, entry model.AuditLogEntry, message *AuditMirrorMessage) error {
	var caseID string
	var execution *model.CaseActionExecution
	switch entry.ResourceType {
	case "case":
		caseID = entry.ResourceID
	case "case_action_execution":
		var err error
		execution, err = w.store.GetCaseActionExecution(ctx, entry.GuildID, entry.ResourceID)
		if err != nil {
			return err
		}
		if execution != nil {
			caseID = execution.CaseID
		}
	case "appeal":
		appeal, err := w.store.GetAppealByID(ctx, entry.ResourceID)
		if err != nil {
			return err
		}
		if appeal != nil && appeal.GuildID == entry.GuildID && appeal.CaseID != nil {
			caseID = *appeal.CaseID
		}
	}
	if caseID == "" {
		return nil
	}
	item, err := w.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return err
	}
	if item == nil || item.GuildID != entry.GuildID {
		return nil
	}
	message.CaseID, message.CaseNumber = item.ID, item.CaseNumber
	message.TargetDiscordUserID = item.TargetDiscordUserID
	message.TemplateName = memberTemplateName(*item)
	if entry.Action == string(model.AuditActionCaseCreate) {
		message.SelectedLevelName, message.SelectedOutcome = auditSelectedOutcome(item.TemplateSnapshotJSON)
	}
	if execution != nil {
		message.ActionType = execution.ActionType
		if entry.Action == string(model.AuditActionActionSucceeded) && execution.ReversalOfExecutionID != nil {
			var metadata struct {
				ReversalNoop bool `json:"reversal_noop"`
			}
			if json.Unmarshal([]byte(entry.MetadataJSON), &metadata) == nil {
				message.ReversalNoop = metadata.ReversalNoop
			}
		}
		if entry.Action == string(model.AuditActionActionFailed) && execution.Status == model.ActionExecutionFailed && execution.DismissedAt == nil && (item.Validity != model.CaseValidityVoided || execution.ReversalOfExecutionID != nil) {
			message.RetryExecutionID = execution.ID
		}
	}
	return nil
}

// auditSelectedOutcome summarizes the policy chosen when a case was created.
// Later template edits and action completion do not rewrite this decision. Old
// records without a selected-level snapshot omit it rather than invent a warning.
func auditSelectedOutcome(snapshotJSON string) (string, string) {
	snapshot := templateSnapshotResponse(snapshotJSON)
	if snapshot == nil || snapshot.SelectedLevel.ID == "" {
		return "", ""
	}
	outcomes := make([]string, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		label := action.ActionType.Label()
		if action.ActionType == model.ActionTimeoutUser && action.TimeoutDurationSeconds > 0 {
			seconds := action.TimeoutDurationSeconds
			switch {
			case seconds%3600 == 0:
				label += fmt.Sprintf(" (%dh)", seconds/3600)
			case seconds%60 == 0:
				label += fmt.Sprintf(" (%dm)", seconds/60)
			default:
				label += fmt.Sprintf(" (%ds)", seconds)
			}
		}
		outcomes = append(outcomes, label)
	}
	if len(outcomes) == 0 {
		outcomes = append(outcomes, "Warning")
	}
	return snapshot.SelectedLevel.Name, strings.Join(outcomes, ", ")
}
