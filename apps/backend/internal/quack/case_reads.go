package quack

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// List returns one page of the guild's cases, newest first, filtered by the
// validated CaseListInput. Denied reads are audited; successful searches write
// a case.search audit row whose failure fails the request.
func (s *CaseService) List(ctx context.Context, guildContext *GuildStaffContext, input CaseListInput) (*CaseListResponse, error) {
	params, limit, offset, err := s.caseListParams(guildContext, input)
	if err != nil {
		if errors.Is(err, ErrCasePermissionDenied) {
			// best-effort: the denial is already being returned to the caller
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

// Get returns the complete authorized staff detail, including all events and action attempts.
func (s *CaseService) Get(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	return s.getDetail(ctx, guildContext, caseRef, false)
}

// GetNativeDetail returns the native staff detail with the latest six timeline
// events and no action attempts. Actions, evidence, and recovery controls retain
// their full detail; guild authorization and read auditing match Get.
func (s *CaseService) GetNativeDetail(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	return s.getDetail(ctx, guildContext, caseRef, true)
}

// getDetail shares authorization and response construction while limiting reads
// that the native renderer cannot display. The HTTP detail remains complete.
func (s *CaseService) getDetail(
	ctx context.Context,
	guildContext *GuildStaffContext,
	caseRef string,
	native bool,
) (*CaseDetailResponse, error) {
	if err := s.requireCaseRead(guildContext); err != nil {
		// best-effort: the denial is already being returned to the caller
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
	var events []model.CaseEvent
	if native {
		events, err = s.store.ListRecentCaseEvents(ctx, caseModel.ID, 6)
	} else {
		events, err = s.store.ListCaseEvents(ctx, caseModel.ID)
	}
	if err != nil {
		return nil, err
	}

	executionIDs := make([]string, 0, len(actions))
	for _, action := range actions {
		if !native || (action.ActionType == model.ActionTimeoutUser && action.Status == model.ActionExecutionSucceeded) {
			executionIDs = append(executionIDs, action.ID)
		}
	}
	var attempts []model.CaseActionAttempt
	if len(executionIDs) > 0 {
		attempts, err = s.store.ListCaseActionAttempts(ctx, executionIDs)
		if err != nil {
			return nil, err
		}
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
	for i := range base.Actions {
		if base.Actions[i].ActionType == model.ActionTimeoutUser && base.Actions[i].Status == model.ActionExecutionSucceeded {
			base.Actions[i].TimeoutUntil = RecordedTimeoutUntil(base.Actions[i].ID, attempts)
		}
	}
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

// GetEvidenceView returns the native staff evidence projection without loading
// unrelated action attempts, timeline events, or notification state. Authorization
// and read auditing match Get; the full API detail contract remains unchanged.
func (s *CaseService) GetEvidenceView(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	if err := s.requireCaseRead(guildContext); err != nil {
		// best-effort: the denial is already being returned to the caller
		_ = s.audit(ctx, guildContext, string(model.AuditActionCaseRead), "case", strings.TrimSpace(caseRef), model.AuditResultDenied, "permission_denied")
		return nil, err
	}
	caseRef = strings.TrimSpace(caseRef)
	if caseRef == "" {
		return nil, validationCaseError("case reference is required")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	evidence, attachments, err := s.store.ListCaseEvidence(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guildContext, "case.read", "case", item.ID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &CaseDetailResponse{
		CaseResponse: CaseResponse{ID: item.ID, CaseNumber: item.CaseNumber},
		Evidence:     caseEvidenceResponses(evidence, attachments, false),
	}, nil
}

// UserHistory returns one page of the cases targeting a member together with
// the member's all-time summary counts. It reuses List (and its authorization
// and audit) with the target filter forced, then adds a case.history.read audit.
func (s *CaseService) UserHistory(
	ctx context.Context,
	guildContext *GuildStaffContext,
	targetDiscordUserID string,
	input CaseListInput,
) (*CaseProfileResponse, error) {
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

// caseListParams checks read permission, then validates and normalizes every
// filter in CaseListInput into the repository query. It also returns the
// effective limit and offset so the response can echo them.
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
		CaseNumber:             caseNumber,
		ActionResult:           actionResult,
		AppealStatus:           appealStatus,
		CreatedAfter:           createdAfter,
		CreatedBefore:          createdBefore,
		Limit:                  limit,
		Offset:                 offset,
	}, limit, offset, nil
}

// CaseEvidencePageResponse contains one authorized snapshot and navigation counts;
// the ordinary API detail and evidence collection contracts remain unchanged.
type CaseEvidencePageResponse struct {
	CaseDetailResponse
	Position int
	Total    int64
}

// GetEvidencePage checks current staff/guild access before fetching any captured
// content. It preserves read audit behavior while bounding native snapshot reads.
// Positions below 1 or beyond the total are clamped into range.
func (s *CaseService) GetEvidencePage(
	ctx context.Context,
	guild *GuildStaffContext,
	caseRef string,
	position int,
) (*CaseEvidencePageResponse, error) {
	if err := s.requireCaseRead(guild); err != nil {
		// best-effort: the denial is already being returned to the caller
		_ = s.audit(ctx, guild, string(model.AuditActionCaseRead), "case", strings.TrimSpace(caseRef), model.AuditResultDenied, "permission_denied")
		return nil, err
	}
	caseRef = strings.TrimSpace(caseRef)
	if caseRef == "" {
		return nil, validationCaseError("case reference is required")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guild.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	snapshot, attachments, total, err := s.store.GetCaseEvidencePage(ctx, item.ID, position)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guild, "case.read", "case", item.ID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	if position < 1 {
		position = 1
	}
	if int64(position) > total {
		position = max(1, int(total))
	}
	var evidence []CaseEvidenceResponse
	if snapshot != nil {
		evidence = caseEvidenceResponses([]model.CaseEvidenceSnapshot{*snapshot}, attachments, false)
	}
	return &CaseEvidencePageResponse{
		CaseDetailResponse: CaseDetailResponse{
			CaseResponse: CaseResponse{
				ID:            item.ID,
				CaseNumber:    item.CaseNumber,
				ContextValues: parseCaseContextValues(item.ContextValuesJSON),
			},
			Evidence: evidence,
		},
		Position: position,
		Total:    total,
	}, nil
}

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

// ListMemberCases returns only cases targeting the authenticated Discord
// identity and does not require current guild membership. A case is appealable
// when its snapshot allows it, it is still valid, and no appeal exists yet.
func (s *CaseService) ListMemberCases(
	ctx context.Context,
	guildID, memberDiscordUserID string,
	input CaseListInput,
) (*MemberCaseListResponse, error) {
	guildID = strings.TrimSpace(guildID)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	if guildID == "" || memberDiscordUserID == "" {
		return nil, validationCaseError("guild and member identity are required")
	}
	limit, offset, err := pagination(input.Limit, input.Offset)
	if err != nil {
		return nil, err
	}
	result, err := s.store.ListCasesFiltered(ctx, model.ListCasesParams{
		GuildID:             guildID,
		TargetDiscordUserID: memberDiscordUserID,
		Limit:               limit,
		Offset:              offset,
	})
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
		responses = append(responses, MemberCaseSummary{
			ID:           item.ID,
			GuildID:      item.GuildID,
			CaseNumber:   item.CaseNumber,
			Reason:       item.Reason,
			Validity:     item.Validity,
			CreatedAt:    item.CreatedAt,
			TemplateName: memberTemplateName(item),
			Enforcement:  memberEnforcement(byCase[item.ID]),
			Appealable:   memberCaseAppealable(item, appeal),
			AppealID:     appealID,
			AppealStatus: appealStatus,
		})
	}
	return &MemberCaseListResponse{Cases: responses, Total: result.Total, Limit: limit, Offset: offset}, nil
}

// GetMemberCase returns a privacy-safe case detail only when the authenticated identity owns the case.
func (s *CaseService) GetMemberCase(ctx context.Context, caseID, memberDiscordUserID string) (*MemberCaseDetail, error) {
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
		ID:           item.ID,
		GuildID:      item.GuildID,
		CaseNumber:   item.CaseNumber,
		TemplateID:   item.TemplateID,
		TemplateName: memberTemplateName(*item),
		Reason:       item.Reason,
		Validity:     item.Validity,
		CreatedAt:    item.CreatedAt,
		Enforcement:  memberEnforcement(actions),
		Appealable:   memberCaseAppealable(*item, appeal),
	}
	if appeal != nil {
		result.AppealID, result.AppealStatus = appeal.ID, appeal.Status
	}
	return result, nil
}

// memberCaseAppealable reports whether the member may open an appeal: the
// snapshotted rule allows it, the case is still valid, and none exists yet.
func memberCaseAppealable(item model.Case, appeal *model.Appeal) bool {
	return caseSnapshotAppealable(item.TemplateSnapshotJSON) && item.Validity == model.CaseValidityValid && appeal == nil
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

// CaseReceiptResponse carries the minimum committed decision and delivery state
// for a moderator receipt. It includes staff context; evidence bodies are loaded by case detail views.
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
	base := &CaseResponse{
		ID:                     item.ID,
		CaseNumber:             item.CaseNumber,
		CreatedAt:              item.CreatedAt,
		TargetDiscordUserID:    item.TargetDiscordUserID,
		ModeratorDiscordUserID: item.ModeratorDiscordUserID,
		Reason:                 item.Reason,
		Validity:               item.Validity,
		ContextValues:          parseCaseContextValues(item.ContextValuesJSON),
		SelectedLevel:          selectedLevelResponse(item.TemplateSnapshotJSON),
	}
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
// A nil receipt is reported as pending so callers keep polling rather than
// treating a failed read as a finished delivery.
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

// casePublicationWriter is the optional durable receipt capability. Case
// persistence adapters need not implement Discord publication storage to serve
// other case use cases; missing support retains the adapter's bounded fallback.
type casePublicationWriter interface {
	SaveCasePublication(context.Context, model.CasePublication) error
}

// RecordPublicReceipt registers a message already delivered for a committed case.
// It is an adapter delivery use case, not moderator authorization or enforcement:
// the caller must have obtained the case through creation/authorized access and
// supply only its public presentation. The idempotent store preserves the first
// snapshot. Three immediate attempts retain the existing registration behavior;
// failure leaves the caller responsible for private feedback and bounded refresh.
func (s *CaseService) RecordPublicReceipt(ctx context.Context, receipt model.CasePublication) error {
	writer, ok := s.store.(casePublicationWriter)
	if !ok {
		return errors.New("durable case publication storage unavailable")
	}
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = writer.SaveCasePublication(ctx, receipt)
		if err == nil || ctx.Err() != nil {
			break
		}
	}
	return err
}

// PublicReceiptActionStatuses returns only IDs and public execution status for
// an already committed case receipt. It performs no action, audit write or live
// moderator check: publication recovery continues after the initiating staff
// interaction ends, using the previously authorized public case projection.
func (s *CaseService) PublicReceiptActionStatuses(ctx context.Context, caseID string) ([]CaseActionResponse, error) {
	actions, err := s.store.ListCaseActionExecutions(ctx, caseID)
	if err != nil {
		return nil, err
	}
	result := make([]CaseActionResponse, 0, len(actions))
	for _, action := range actions {
		result = append(result, CaseActionResponse{ID: action.ID, Status: action.Status})
	}
	return result, nil
}
