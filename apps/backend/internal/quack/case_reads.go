package quack

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// List returns list subject to authorization, ordering, and filtering constraints.
func (s *CaseService) List(ctx context.Context, guildContext *GuildStaffContext, input CaseListInput) (*CaseListResponse, error) {
	params, limit, offset, err := s.caseListParams(guildContext, input)
	if err != nil {
		if errors.Is(err, ErrCasePermissionDenied) {
			_ = s.audit(ctx, guildContext, string(model.AuditActionCaseSearch), "case", "list", model.AuditResultDenied, "permission_denied")
		}
		return nil, err
	}

	result, err := s.store.ListCasesFiltered(ctx, params)
	if err != nil {
		return nil, err
	}

	responses, err := s.caseResponsesForModels(ctx, result.Cases)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guildContext, "case.search", "case", "list", model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}

	return &CaseListResponse{
		Cases:  responses,
		Total:  result.Total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// Get retrieves get without exposing the underlying adapter implementation.
func (s *CaseService) Get(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	if err := s.requireCaseRead(guildContext); err != nil {
		_ = s.audit(ctx, guildContext, string(model.AuditActionCaseRead), "case", strings.TrimSpace(caseRef), model.AuditResultDenied, "permission_denied")
		return nil, err
	}
	caseRef = strings.TrimSpace(caseRef)
	if caseRef == "" {
		return nil, validationCaseError("case reference is required")
	}

	caseModel, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if caseModel == nil {
		return nil, ErrCaseNotFound
	}

	actions, err := s.store.ListCaseActionExecutions(ctx, caseModel.ID)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListCaseEvents(ctx, caseModel.ID)
	if err != nil {
		return nil, err
	}

	executionIDs := make([]string, 0, len(actions))
	for _, action := range actions {
		executionIDs = append(executionIDs, action.ID)
	}
	attempts, err := s.store.ListCaseActionAttempts(ctx, executionIDs)
	if err != nil {
		return nil, err
	}

	evidence, attachments, err := s.store.ListCaseEvidence(ctx, caseModel.ID)
	if err != nil {
		return nil, err
	}
	notification, err := s.store.GetCaseNotification(ctx, caseModel.ID)
	if err != nil {
		return nil, err
	}
	base := caseResponseFromModel(*caseModel, actions)
	base.EvidenceIncomplete = evidenceIncomplete(evidence)
	if err := s.audit(ctx, guildContext, "case.read", "case", caseModel.ID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &CaseDetailResponse{
		CaseResponse:     base,
		TemplateSnapshot: templateSnapshotResponse(caseModel.TemplateSnapshotJSON),
		Actions:          caseActionDetailResponses(actions, attempts),
		Events:           caseEventResponses(events),
		Evidence:         caseEvidenceResponses(evidence, attachments, false),
		Notification:     caseNotificationResponse(notification, false),
	}, nil
}

// UserHistory encapsulates the user history rule so callers share one consistent package implementation.
func (s *CaseService) UserHistory(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, input CaseListInput) (*CaseProfileResponse, error) {
	targetDiscordUserID = strings.TrimSpace(targetDiscordUserID)
	if targetDiscordUserID == "" {
		return nil, validationCaseError("target discord user id is required")
	}
	input.TargetDiscordUserID = targetDiscordUserID

	list, err := s.List(ctx, guildContext, input)
	if err != nil {
		return nil, err
	}
	summary, err := s.store.TargetCaseSummary(ctx, guildContext.Guild.ID, targetDiscordUserID)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guildContext, "case.history.read", "member", targetDiscordUserID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}

	return &CaseProfileResponse{
		Cases:  list.Cases,
		Total:  list.Total,
		Limit:  list.Limit,
		Offset: list.Offset,
		Summary: CaseProfileSummary{
			Total:      summary.Total,
			ByValidity: caseValiditySummary(summary.ByValidity),
			ByTemplate: summary.ByTemplate,
		},
	}, nil
}

// caseListParams encapsulates the case list params rule so callers share one consistent package implementation.
func (s *CaseService) caseListParams(guildContext *GuildStaffContext, input CaseListInput) (model.ListCasesParams, int, int, error) {
	if err := s.requireCaseRead(guildContext); err != nil {
		return model.ListCasesParams{}, 0, 0, err
	}

	limit, offset, err := pagination(input.Limit, input.Offset)
	if err != nil {
		return model.ListCasesParams{}, 0, 0, err
	}

	validity := model.CaseValidity(strings.TrimSpace(input.Validity))
	if validity != "" && !validCaseValidity(validity) {
		return model.ListCasesParams{}, 0, 0, validationCaseError("validity is invalid")
	}
	caseNumber := strings.TrimSpace(input.CaseNumber)
	if caseNumber != "" {
		parsed, parseErr := strconv.ParseUint(caseNumber, 10, 64)
		if parseErr != nil || parsed == 0 {
			return model.ListCasesParams{}, 0, 0, validationCaseError("case_number is invalid")
		}
	}
	actionResult := strings.TrimSpace(input.ActionResult)
	if actionResult != "" && !validActionExecutionStatus(model.ActionExecutionStatus(actionResult)) {
		return model.ListCasesParams{}, 0, 0, validationCaseError("action_result is invalid")
	}
	appealStatus := strings.TrimSpace(input.AppealStatus)
	if appealStatus != "" && !validAppealStatus(model.AppealStatus(appealStatus)) {
		return model.ListCasesParams{}, 0, 0, validationCaseError("appeal_status is invalid")
	}
	createdAfter, err := normalizeOptionalTime(input.CreatedAfter)
	if err != nil {
		return model.ListCasesParams{}, 0, 0, err
	}
	createdBefore, err := normalizeOptionalTime(input.CreatedBefore)
	if err != nil {
		return model.ListCasesParams{}, 0, 0, err
	}

	return model.ListCasesParams{
		GuildID:                guildContext.Guild.ID,
		TargetDiscordUserID:    strings.TrimSpace(input.TargetDiscordUserID),
		ModeratorDiscordUserID: strings.TrimSpace(input.ModeratorDiscordUserID),
		TemplateID:             strings.TrimSpace(input.TemplateID),
		Validity:               validity,
		CaseNumber:             caseNumber, ActionResult: actionResult, AppealStatus: appealStatus, CreatedAfter: createdAfter, CreatedBefore: createdBefore,
		Limit:  limit,
		Offset: offset,
	}, limit, offset, nil
}
