package quack

import (
	"context"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// enrichCase resolves case references through guild-scoped records. It exposes
// only identifiers and the snapshotted rule name, never evidence or staff context.
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
	if execution != nil {
		message.ActionType = execution.ActionType
		if entry.Action == string(model.AuditActionActionFailed) && execution.Status == model.ActionExecutionFailed && execution.DismissedAt == nil && (item.Validity != model.CaseValidityVoided || execution.ReversalOfExecutionID != nil) {
			message.RetryExecutionID = execution.ID
		}
	}
	return nil
}
