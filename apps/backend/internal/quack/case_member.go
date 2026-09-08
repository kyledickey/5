package quack

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// MemberCaseDetail is the privacy-safe projection available only to the target Discord identity.
type MemberCaseDetail struct {
	TemplateName string                    `json:"template_name"`
	ID           string                    `json:"id"`
	GuildID      string                    `json:"guild_id"`
	CaseNumber   uint64                    `json:"case_number"`
	TemplateID   *string                   `json:"template_id"`
	Reason       string                    `json:"official_reason"`
	Validity     model.CaseValidity        `json:"validity"`
	CreatedAt    time.Time                 `json:"created_at"`
	Enforcement  *MemberEnforcementOutcome `json:"enforcement,omitempty"`
	Appealable   bool                      `json:"appealable"`
	AppealID     string                    `json:"appeal_id,omitempty"`
	AppealStatus model.AppealStatus        `json:"appeal_status,omitempty"`
}

// MemberCaseSummary is the deliberately small list projection that cannot expose moderator or adapter internals.
type MemberCaseSummary struct {
	TemplateName string                    `json:"template_name"`
	Enforcement  *MemberEnforcementOutcome `json:"enforcement,omitempty"`
	ID           string                    `json:"id"`
	GuildID      string                    `json:"guild_id"`
	CaseNumber   uint64                    `json:"case_number"`
	Reason       string                    `json:"official_reason"`
	Validity     model.CaseValidity        `json:"validity"`
	CreatedAt    time.Time                 `json:"created_at"`
	Appealable   bool                      `json:"appealable"`
	AppealID     string                    `json:"appeal_id,omitempty"`
	AppealStatus model.AppealStatus        `json:"appeal_status,omitempty"`
}

// MemberCaseListResponse returns only target-owned privacy-safe summaries.
type MemberCaseListResponse struct {
	Cases  []MemberCaseSummary `json:"cases"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

// MemberEnforcementOutcome exposes only the configured action and public result.
type MemberEnforcementOutcome struct {
	ActionType model.ActionType            `json:"action_type"`
	Status     model.ActionExecutionStatus `json:"status"`
}

// ListMemberCases returns only cases targeting the authenticated Discord identity and does not require current guild membership.
func (s *CaseService) ListMemberCases(ctx context.Context, guildID, memberDiscordUserID string, input CaseListInput) (*MemberCaseListResponse, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
	guildID = strings.TrimSpace(guildID)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	if guildID == "" || memberDiscordUserID == "" {
		return nil, validationCaseError("guild and member identity are required")
	}
	limit, offset, err := pagination(input.Limit, input.Offset)
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListCasesFiltered(ctx, model.ListCasesParams{GuildID: guildID, TargetDiscordUserID: memberDiscordUserID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	caseIDs := make([]string, 0, len(result.Cases))
	for _, item := range result.Cases {
		caseIDs = append(caseIDs, item.ID)
	}
	actions, err := s.store.ListCaseActionsForCases(ctx, caseIDs)
	if err != nil {
		return nil, err
	}
	byCase := make(map[string][]model.CaseActionExecution)
	for _, action := range actions {
		byCase[action.CaseID] = append(byCase[action.CaseID], action)
	}
	responses := make([]MemberCaseSummary, 0, len(result.Cases))
	for _, item := range result.Cases {
		appeal, appealErr := s.store.GetAppealByCaseID(ctx, item.ID)
		if appealErr != nil {
			return nil, appealErr
		}
		appealID, appealStatus := "", model.AppealStatus("")
		if appeal != nil {
			appealID, appealStatus = appeal.ID, appeal.Status
		}
		responses = append(responses, MemberCaseSummary{ID: item.ID, GuildID: item.GuildID, CaseNumber: item.CaseNumber, Reason: item.Reason, Validity: item.Validity, CreatedAt: item.CreatedAt, TemplateName: memberTemplateName(item), Enforcement: memberEnforcement(byCase[item.ID]), Appealable: caseSnapshotAppealable(item.TemplateSnapshotJSON) && item.Validity == model.CaseValidityValid && appeal == nil, AppealID: appealID, AppealStatus: appealStatus})
	}
	return &MemberCaseListResponse{Cases: responses, Total: result.Total, Limit: limit, Offset: offset}, nil
}

// GetMemberCase returns a privacy-safe case detail only when the authenticated identity owns the case.
func (s *CaseService) GetMemberCase(ctx context.Context, caseID, memberDiscordUserID string) (*MemberCaseDetail, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
	item, err := s.store.GetCaseByID(ctx, strings.TrimSpace(caseID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.TargetDiscordUserID != strings.TrimSpace(memberDiscordUserID) {
		return nil, ErrCaseNotFound
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	appeal, err := s.store.GetAppealByCaseID(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	result := &MemberCaseDetail{
		ID: item.ID, GuildID: item.GuildID, CaseNumber: item.CaseNumber, TemplateID: item.TemplateID,
		TemplateName: memberTemplateName(*item), Reason: item.Reason, Validity: item.Validity, CreatedAt: item.CreatedAt,
		Enforcement: memberEnforcement(actions),
		Appealable:  caseSnapshotAppealable(item.TemplateSnapshotJSON) && item.Validity == model.CaseValidityValid && appeal == nil,
	}
	if appeal != nil {
		result.AppealID, result.AppealStatus = appeal.ID, appeal.Status
	}
	return result, nil
}

// memberTemplateName exposes the rule name fixed at creation, never a staff level label.
func memberTemplateName(item model.Case) string {
	if snapshot := templateSnapshotResponse(item.TemplateSnapshotJSON); snapshot != nil {
		return snapshot.Template.Name
	}
	return ""
}

// memberEnforcement excludes execution identifiers, attempts, errors and staff actors.
func memberEnforcement(actions []model.CaseActionExecution) *MemberEnforcementOutcome {
	for _, action := range actions {
		if action.ReversalOfExecutionID == nil && action.ActionType != model.ActionSendDM {
			return &MemberEnforcementOutcome{ActionType: action.ActionType, Status: action.Status}
		}
	}
	return nil
}
