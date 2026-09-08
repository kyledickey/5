package quack

import (
	"context"
	"errors"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseReceiptResponse carries the minimum committed decision and delivery state
// for a moderator receipt. It excludes context, evidence bodies, and raw errors.
type CaseReceiptResponse struct {
	Case         *CaseResponse
	RuleName     string
	MemberReason string
	Appealable   bool
	Notification *CaseNotificationResponse
	Actions      []CaseActionDetailResponse
}

// ReceiptForPublication reads delivery progress for an already-authorized,
// committed case. Like public receipt recovery, this is a delivery continuation,
// not a case discovery API. Adapters must never accept an arbitrary user's case
// reference here; interactive case navigation uses GetNativeDetail instead.
func (s *CaseService) ReceiptForPublication(ctx context.Context, caseID string) (*CaseReceiptResponse, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("case receipt storage unavailable")
	}
	item, err := s.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, caseID)
	if err != nil {
		return nil, err
	}
	notification, err := s.store.GetCaseNotification(ctx, caseID)
	if err != nil {
		return nil, err
	}
	base := &CaseResponse{ID: item.ID, CaseNumber: item.CaseNumber, CreatedAt: item.CreatedAt, TargetDiscordUserID: item.TargetDiscordUserID, Validity: item.Validity, SelectedLevel: selectedLevelResponse(item.TemplateSnapshotJSON)}
	result := &CaseReceiptResponse{Case: base, Notification: caseNotificationResponse(notification, false)}
	base.EvidenceIncomplete, err = s.store.CasePublicationEvidenceIncomplete(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if result.Notification != nil {
		result.Notification.LastError = ""
	}
	if snapshot := templateSnapshotResponse(item.TemplateSnapshotJSON); snapshot != nil {
		result.RuleName = snapshot.Template.Name
		result.MemberReason = snapshot.Template.ReasonTemplate
		result.Appealable = snapshot.Template.Appealable && item.Validity != model.CaseValidityVoided
	}
	for _, action := range actions {
		projected := CaseActionResponse{ID: action.ID, ActionType: action.ActionType, Status: action.Status}
		base.Actions = append(base.Actions, projected)
		result.Actions = append(result.Actions, CaseActionDetailResponse{CaseActionResponse: projected, LastErrorCode: action.LastErrorCode})
	}
	return result, nil
}

// Pending reports whether either enforcement or the independent member DM still
// needs a receipt update. Missing notification means this level has DMs disabled.
func (r *CaseReceiptResponse) Pending() bool {
	if r == nil {
		return true
	}
	for _, action := range r.Actions {
		switch action.Status {
		case model.ActionExecutionPending, model.ActionExecutionRunning, model.ActionExecutionRetrying:
			return true
		}
	}
	if r.Notification != nil {
		switch r.Notification.Status {
		case model.NotificationSent, model.NotificationFailed, model.NotificationStatus("skipped"):
			return false
		default:
			return true
		}
	}
	return false
}
