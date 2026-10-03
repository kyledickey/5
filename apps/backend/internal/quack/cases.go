package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/idutil"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// CaseRepository is the persistence CaseService needs: creation under the
// guild case lock, authorized reads, evidence, and immutable corrections.
type CaseRepository interface {
	AppendCaseEvidence(
		context.Context, string, string, []model.CaseEvidenceSnapshot, []model.CaseEvidenceAttachment, *model.AuditLogEntry,
	) error
	UpdateCaseContext(context.Context, string, string, string, *model.AuditLogEntry) (*model.Case, error)
	ListCaseActionsForCases(context.Context, []string) ([]model.CaseActionExecution, error)
	CountTemplateCasesForTarget(context.Context, model.CountTemplateCasesForTargetParams) (int64, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	CreateCase(context.Context, model.CreateCaseParams) (*model.CreatedCase, error)
	GetAppealByCaseID(context.Context, string) (*model.Appeal, error)
	GetCaseByID(context.Context, string) (*model.Case, error)
	GetCaseByIDOrNumber(context.Context, string, string) (*model.Case, error)
	GetCaseByIdempotencyKey(context.Context, string, string) (*model.Case, error)
	GetCaseNotification(context.Context, string) (*model.CaseNotification, error)
	GetCaseTemplateExpanded(context.Context, string, string) (*model.ExpandedCaseTemplate, error)
	GetGuildByID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	ListCaseActionAttempts(context.Context, []string) ([]model.CaseActionAttempt, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	ListCaseEvents(context.Context, string) ([]model.CaseEvent, error)
	ListRecentCaseEvents(context.Context, string, int) ([]model.CaseEvent, error)
	CasePublicationEvidenceIncomplete(context.Context, string) (bool, error)
	GetCaseEvidencePage(context.Context, string, int) (*model.CaseEvidenceSnapshot, []model.CaseEvidenceAttachment, int64, error)
	ListCaseEvidence(context.Context, string) ([]model.CaseEvidenceSnapshot, []model.CaseEvidenceAttachment, error)
	ListCasesFiltered(context.Context, model.ListCasesParams) (*model.ListCasesResult, error)
	TargetCaseSummary(context.Context, string, string) (*model.TargetCaseSummary, error)
	VoidCase(context.Context, model.VoidCaseParams) (*model.Case, error)
	WithGuildCaseLock(context.Context, string, func(CaseRepository) error) error
}

var (
	// ErrCaseValidation wraps every rejected case input; the suffix after the
	// colon is safe to show to staff.
	ErrCaseValidation = errors.New("case validation failed")
	// ErrCaseTemplateNotAvailable reports a template that is missing from the
	// guild or archived, so it cannot be applied to new cases.
	ErrCaseTemplateNotAvailable = errors.New("case template not available")
	// ErrCasePermissionDenied reports a staff context that lacks the permission
	// bit the operation requires.
	ErrCasePermissionDenied = errors.New("case permission denied")
	// ErrCaseNotFound reports a case reference that does not resolve inside the
	// caller's guild; cross-guild lookups deliberately look identical.
	ErrCaseNotFound = errors.New("case not found")
	// errCasePreflightStale signals that the template, escalation level, or
	// action changed between preflight and the locked transaction, so the
	// preflight must be recomputed.
	errCasePreflightStale = errors.New("case preflight became stale")
)

// CaseService creates, reads, corrects, and annotates moderation cases. It owns
// the guild-scoped creation lock, escalation level selection, the immutable
// template snapshot, case audit rows, and the post-commit hand-off to the action
// scheduler. Live Discord authorization (authorizer) and evidence capture
// (evidence) are optional collaborators: without them cases are created from
// stored permissions alone and evidence is recorded as unavailable.
type CaseService struct {
	store      CaseRepository
	scheduler  CaseWorkScheduler
	authorizer *GuildService
	evidence   *EvidenceService
}

// NewCaseService wires case persistence and an optional scheduler. A nil
// scheduler means committed cases wait for the durable poller instead of being
// submitted immediately.
func NewCaseService(store CaseRepository, scheduler CaseWorkScheduler) *CaseService {
	return &CaseService{store: store, scheduler: scheduler}
}

// WithEvidenceCapture installs the evidence service used to snapshot message
// links and uploads before a case commits. It returns the receiver for chaining.
func (s *CaseService) WithEvidenceCapture(evidence *EvidenceService) *CaseService {
	s.evidence = evidence
	return s
}

// evidenceCapture returns the configured evidence service or, when none was
// installed, a zero-value service whose captures produce "unavailable" snapshots
// and metadata-only upload records. Case creation therefore never depends on
// Discord evidence access.
func (s *CaseService) evidenceCapture() *EvidenceService {
	if s.evidence != nil {
		return s.evidence
	}
	return &EvidenceService{}
}

// Create applies a staff-attributed template to a user inside the guild-scoped
// transaction boundary. The lock keeps escalation history and case numbering
// consistent, while scheduling occurs only after the transaction commits.
func (s *CaseService) Create(ctx context.Context, guildContext *GuildStaffContext, input CaseInput) (*CaseResponse, error) {
	if input.Source == model.CaseSourceHoneypot {
		return nil, validationCaseError("honeypot cases require the system application boundary")
	}
	attribution := caseCreateAttribution{actorType: "staff"}
	return s.createWithAttribution(ctx, guildContext, input, attribution)
}

// CreateSystemHoneypot applies one honeypot template through the ordinary case
// transaction while attributing the operation to Quack itself. It is intended
// only for the injected optional-module adapter and rejects every other source.
func (s *CaseService) CreateSystemHoneypot(ctx context.Context, guildID string, input CaseInput) (*CaseResponse, error) {
	if s.authorizer == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if input.Source != model.CaseSourceHoneypot {
		return nil, validationCaseError("system case creation is restricted to the honeypot source")
	}
	guild, err := s.store.GetGuildByID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return nil, err
	}
	if guild == nil || !guild.IsActive {
		return nil, validationCaseError("active guild is required")
	}
	systemContext := &GuildStaffContext{
		Guild: guild,
		Staff: &model.StaffMember{},
		Permissions: map[model.PermissionAction]bool{
			model.PermissionActionCaseCreate: true,
		},
	}
	attribution := caseCreateAttribution{actorType: "system", system: true}
	return s.createWithAttribution(ctx, systemContext, input, attribution)
}

// createWithAttribution owns the shared moderation path for staff and the
// narrowly scoped honeypot system boundary. It answers idempotent replays from
// storage, then runs preflight outside the lock and the create inside it,
// retrying a bounded number of times when the preflight goes stale.
func (s *CaseService) createWithAttribution(
	ctx context.Context,
	guildContext *GuildStaffContext,
	input CaseInput,
	attribution caseCreateAttribution,
) (*CaseResponse, error) {
	ctx = ensureTraceContext(ctx)
	ctx = ContextWithAuditSource(ctx, AuditSourceForCaseSource(input.Source))
	if guildContext == nil || guildContext.Guild == nil {
		return nil, validationCaseError("missing guild context")
	}
	if guildContext.Staff == nil || !guildContext.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	key := strings.TrimSpace(input.IdempotencyKey)
	if len(key) > 191 {
		return nil, validationCaseError("idempotency key is too long")
	}
	input.IdempotencyKey = key
	if key != "" {
		existing, getErr := s.store.GetCaseByIdempotencyKey(ctx, guildContext.Guild.ID, key)
		if getErr != nil {
			return nil, getErr
		}
		if existing != nil {
			sameTarget := existing.TargetDiscordUserID == strings.TrimSpace(input.TargetDiscordUserID)
			sameTemplate := existing.TemplateID == nil || *existing.TemplateID == strings.TrimSpace(input.TemplateID)
			if !sameTarget || !sameTemplate {
				return nil, validationCaseError("idempotency key was already used for another case request")
			}
			actions, listErr := s.store.ListCaseActionExecutions(ctx, existing.ID)
			if listErr != nil {
				return nil, listErr
			}
			evidence, _, evidenceErr := s.store.ListCaseEvidence(ctx, existing.ID)
			if evidenceErr != nil {
				return nil, evidenceErr
			}
			response := caseResponseFromModel(*existing, actions)
			response.EvidenceIncomplete = evidenceIncomplete(evidence)
			return &response, nil
		}
	}
	var created *model.CreatedCase
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		var preflight *caseCreatePreflight
		preflight, err = s.preflightCreate(ctx, guildContext, input, attribution)
		if err != nil {
			break
		}
		err = s.store.WithGuildCaseLock(ctx, guildContext.Guild.ID, func(transactionalStore CaseRepository) error {
			transactionalService := *s
			transactionalService.store = transactionalStore
			var createErr error
			created, createErr = transactionalService.create(ctx, guildContext, input, preflight, attribution)
			return createErr
		})
		if errors.Is(err, errCasePreflightStale) {
			continue
		}
		break
	}
	if err != nil {
		var authorizationErr *AuthorizationError
		if errors.As(err, &authorizationErr) && s.authorizer != nil {
			// best-effort: the denial is already being returned to the caller
			_ = s.authorizer.auditAuthorizationDenial(
				ctx,
				guildContext,
				authorizationErr.Capability,
				AuditSourceFromContext(ctx),
				authorizationErr.Reason,
				authorizationErr.MetadataJSON,
			)
		}
		if errors.Is(err, ErrCaseValidation) ||
			errors.Is(err, ErrCasePermissionDenied) ||
			errors.Is(err, ErrCaseTemplateNotAvailable) ||
			errors.Is(err, errCasePreflightStale) {
			// best-effort: the rejection is already being returned to the caller
			_ = s.auditWithAttribution(ctx, guildContext, attribution, "case.create", "case", "unknown", model.AuditResultFailure, err.Error())
		}
		if errors.Is(err, errCasePreflightStale) {
			err = validationCaseError("case state changed repeatedly; retry the request")
		}
		return nil, err
	}

	if s.scheduler != nil {
		if !s.scheduler.Submit(ctx, created.Case.ID) {
			slog.WarnContext(ctx, "Immediate action scheduling deferred to durable polling", "case_id", created.Case.ID)
		}
	}
	slog.InfoContext(ctx, "Case created", "guild_id", created.Case.GuildID,
		"case_id", created.Case.ID, "case_number", created.Case.CaseNumber,
		"template_id", input.TemplateID, "source", created.Case.Source)

	response := caseResponseFromModel(created.Case, created.ActionExecutions)
	response.EvidenceIncomplete = evidenceIncomplete(created.Evidence)
	return &response, nil
}

// preflightCreate performs the work that must not run under the guild lock:
// template lookup, escalation selection, live Discord authorization, context
// validation, and evidence capture. Its result is verified again inside the
// transaction and rejected with errCasePreflightStale if anything moved.
func (s *CaseService) preflightCreate(
	ctx context.Context,
	guildContext *GuildStaffContext,
	input CaseInput,
	attribution caseCreateAttribution,
) (*caseCreatePreflight, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	templateID, targetID := strings.TrimSpace(input.TemplateID), strings.TrimSpace(input.TargetDiscordUserID)
	if templateID == "" || targetID == "" {
		return nil, validationCaseError("template_id and target_discord_user_id are required")
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrCaseTemplateNotAvailable
	}
	selected, err := s.selectTemplateLevel(ctx, guildContext.Guild.ID, targetID, template)
	if err != nil {
		return nil, err
	}
	actionType := model.ActionType("")
	if len(selected.Actions) == 1 {
		actionType = selected.Actions[0].ActionType
	}
	if s.authorizer != nil {
		var err error
		if attribution.system {
			err = s.authorizer.PreflightSystemCase(ctx, guildContext, targetID, actionType)
		} else {
			err = s.authorizer.PreflightCase(ctx, guildContext, targetID, actionType)
		}
		if err != nil {
			return nil, err
		}
	}
	valuesJSON, links, err := validateCaseContextValues(template.ContextFields, input.ContextValues)
	if err != nil {
		return nil, err
	}
	links = append(links, input.EvidenceLinks...)
	if strings.TrimSpace(input.ContextURL) != "" {
		links = append(links, input.ContextURL)
	}
	result := &caseCreatePreflight{
		TemplateID:        template.Template.ID,
		TemplateVersion:   template.Template.Version,
		SelectedLevelID:   selected.Level.ID,
		ActionType:        actionType,
		ContextValuesJSON: valuesJSON,
	}
	if len(links) > 0 || len(input.Attachments) > 0 {
		settings, settingsErr := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
		if settingsErr != nil {
			slog.WarnContext(ctx, "Evidence storage settings unavailable", "guild_id", guildContext.Guild.ID)
		}
		channelID := ""
		if settings != nil {
			channelID = settings.ManagedEvidenceChannelDiscordID
		}
		actorID := guildContext.ActorDiscordUserID
		if attribution.system {
			actorID = ""
		} else if actorID == "" {
			return nil, validationCaseError("evidence actor is required")
		}
		evidence := s.evidenceCapture()
		captured, captureErr := evidence.capture(ctx, guildContext.Guild.DiscordGuildID, actorID, targetID, channelID, links)
		if captureErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			warning := "Evidence could not be saved. Add it to the case later."
			slog.WarnContext(ctx, "Case evidence capture failed", "guild_id", guildContext.Guild.ID)
			result.Captured = CapturedEvidence{
				Snapshots: []model.CaseEvidenceSnapshot{{
					CaptureOutcome:   "unavailable",
					CaptureWarning:   warning,
					MessageCreatedAt: time.Now().UTC(),
					EmbedsJSON:       "[]",
				}},
				Warnings: []string{warning},
			}
		}
		if captured != nil {
			result.Captured = *captured
		}
		uploads, uploadErr := evidence.CaptureUploads(ctx, guildContext.Guild.DiscordGuildID, actorID, channelID, input.Attachments)
		if uploadErr != nil {
			return nil, uploadErr
		}
		result.Captured.Snapshots = append(result.Captured.Snapshots, uploads.Snapshots...)
		result.Captured.Attachments = append(result.Captured.Attachments, uploads.Attachments...)
		result.Captured.Warnings = append(result.Captured.Warnings, uploads.Warnings...)
	}
	return result, nil
}

// Void preserves the case and correction reason while removing it from future escalation.
// Any failure after the guild context is known is audited before it is returned.
func (s *CaseService) Void(
	ctx context.Context,
	guildContext *GuildStaffContext,
	caseRef, reason string,
	replacementCaseID *string,
) (response *CaseResponse, err error) {
	defer func() {
		if err == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
			return
		}
		result := model.AuditResultFailure
		if errors.Is(err, ErrCasePermissionDenied) || errors.Is(err, ErrAuthorizationDenied) {
			result = model.AuditResultDenied
		}
		// best-effort: the failure is already being returned to the caller
		_ = s.audit(ctx, guildContext, string(model.AuditActionCaseVoid), "case", strings.TrimSpace(caseRef), result, err.Error())
	}()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, validationCaseError("missing guild context")
	}
	if !guildContext.Can(model.PermissionActionCaseVoid) {
		return nil, ErrCasePermissionDenied
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, validationCaseError("void reason is required")
	}
	if replacementCaseID != nil {
		return nil, validationCaseError("create the replacement after voiding this case")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, strings.TrimSpace(caseRef))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	voided, err := s.store.VoidCase(ctx, model.VoidCaseParams{
		GuildID:            guildContext.Guild.ID,
		CaseID:             item.ID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Reason:             reason,
		ReplacementCaseID:  replacementCaseID,
		Audit:              s.auditEntry(ctx, guildContext, "case.void", "case", item.ID, model.AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	if voided == nil {
		return nil, ErrCaseNotFound
	}
	if s.scheduler != nil {
		s.scheduler.Submit(ctx, voided.ID)
	}
	slog.InfoContext(ctx, "Case voided", "guild_id", voided.GuildID, "case_id", voided.ID, "case_number", voided.CaseNumber)
	actions, err := s.store.ListCaseActionExecutions(ctx, voided.ID)
	if err != nil {
		return nil, err
	}
	result := caseResponseFromModel(*voided, actions)
	return &result, nil
}

// requireCaseRead rejects a missing guild context or a staff member without the
// case read permission before any case data is loaded.
func (s *CaseService) requireCaseRead(guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return validationCaseError("missing guild context")
	}
	if !guildContext.Can(model.PermissionActionCaseRead) {
		return ErrCasePermissionDenied
	}
	return nil
}

// create materializes a case inside the already-locked guild transaction. It
// re-reads the template and re-selects the escalation level so the persisted
// snapshot reflects the state under the lock, rejects the request with
// errCasePreflightStale when that differs from the preflight, and writes the
// case, its initial event, pending actions, evidence, notification, and audit
// rows in one CreateCase call.
func (s *CaseService) create(
	ctx context.Context,
	guildContext *GuildStaffContext,
	input CaseInput,
	preflight *caseCreatePreflight,
	attribution caseCreateAttribution,
) (*model.CreatedCase, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, validationCaseError("missing guild context")
	}
	if !guildContext.Can(model.PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	if input.IdempotencyKey != "" {
		existing, err := s.store.GetCaseByIdempotencyKey(ctx, guildContext.Guild.ID, input.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			sameTarget := existing.TargetDiscordUserID == strings.TrimSpace(input.TargetDiscordUserID)
			sameTemplate := existing.TemplateID != nil && *existing.TemplateID == strings.TrimSpace(input.TemplateID)
			if !sameTarget || !sameTemplate {
				return nil, validationCaseError("idempotency key was already used for another case request")
			}
			actions, actionErr := s.store.ListCaseActionExecutions(ctx, existing.ID)
			if actionErr != nil {
				return nil, actionErr
			}
			evidence, attachments, evidenceErr := s.store.ListCaseEvidence(ctx, existing.ID)
			if evidenceErr != nil {
				return nil, evidenceErr
			}
			return &model.CreatedCase{
				Case:             *existing,
				ActionExecutions: actions,
				Evidence:         evidence,
				Attachments:      attachments,
			}, nil
		}
	}

	templateID := strings.TrimSpace(input.TemplateID)
	if templateID == "" {
		return nil, validationCaseError("template_id is required")
	}
	targetDiscordUserID := strings.TrimSpace(input.TargetDiscordUserID)
	if targetDiscordUserID == "" {
		return nil, validationCaseError("target_discord_user_id is required")
	}

	source := input.Source
	if source == "" {
		source = model.CaseSourceDashboard
	}
	if !validCaseSource(source) {
		return nil, validationCaseError("source is invalid")
	}

	metadataJSON, err := normalizeJSONObject(input.Metadata)
	if err != nil {
		return nil, validationCaseError("metadata must be a JSON object")
	}

	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrCaseTemplateNotAvailable
	}

	reason := strings.TrimSpace(template.Template.ReasonTemplate)
	if reason == "" {
		return nil, validationCaseError("reason is required")
	}

	selectedLevel, err := s.selectTemplateLevel(ctx, guildContext.Guild.ID, targetDiscordUserID, template)
	if err != nil {
		return nil, err
	}
	if preflight == nil {
		return nil, validationCaseError("case preflight is required")
	}
	actualAction := model.ActionType("")
	if len(selectedLevel.Actions) == 1 {
		actualAction = selectedLevel.Actions[0].ActionType
	}
	if preflight.TemplateID != template.Template.ID ||
		preflight.TemplateVersion != template.Template.Version ||
		preflight.SelectedLevelID != selectedLevel.Level.ID ||
		preflight.ActionType != actualAction {
		return nil, errCasePreflightStale
	}
	contextValuesJSON := preflight.ContextValuesJSON
	captured := preflight.Captured

	snapshotJSON, err := buildTemplateSnapshot(template.Template, template.ContextFields, contextValuesJSON, *selectedLevel)
	if err != nil {
		return nil, err
	}
	_, correlationID := idutil.TraceIDsFromContext(ctx)

	actorDiscordUserID := guildContext.Staff.DiscordUserID
	if attribution.system {
		actorDiscordUserID = ""
	}
	caseModel := model.Case{
		GuildID:                 guildContext.Guild.ID,
		TemplateID:              &template.Template.ID,
		TemplateVersion:         template.Template.Version,
		TemplateSnapshotJSON:    snapshotJSON,
		TargetDiscordUserID:     targetDiscordUserID,
		ModeratorDiscordUserID:  actorDiscordUserID,
		Reason:                  reason,
		Validity:                model.CaseValidityValid,
		Source:                  source,
		CorrelationID:           correlationID,
		ContextChannelDiscordID: strings.TrimSpace(input.ContextChannelDiscordID),
		ContextMessageDiscordID: strings.TrimSpace(input.ContextMessageDiscordID),
		ContextURL:              strings.TrimSpace(input.ContextURL),
		MetadataJSON:            metadataJSON,
		ContextValuesJSON:       contextValuesJSON,
	}
	if input.IdempotencyKey != "" {
		key := input.IdempotencyKey
		caseModel.IdempotencyKey = &key
	}
	if replacement := strings.TrimSpace(input.ReplacesCaseID); replacement != "" {
		prior, getErr := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, replacement)
		if getErr != nil {
			return nil, getErr
		}
		if prior == nil || prior.Validity != model.CaseValidityVoided {
			return nil, validationCaseError("replacement must reference a voided case in this guild")
		}
		caseModel.ReplacesCaseID = &prior.ID
	}

	actionExecutions := make([]model.CaseActionExecution, 0, len(selectedLevel.Actions))
	for _, action := range selectedLevel.Actions {
		templateActionID := action.ID
		actionExecutions = append(actionExecutions, model.CaseActionExecution{
			TemplateActionID:   &templateActionID,
			Position:           0,
			ActionType:         action.ActionType,
			Status:             model.ActionExecutionPending,
			ConfigSnapshotJSON: action.ConfigJSON,
			MaxRetries:         action.MaxRetries,
			RetryBackoffMS:     1000,
			SafeForRetry:       true,
			Irreversible:       irreversibleAction(action.ActionType),
			CorrelationID:      correlationID,
		})
	}
	var notification *model.CaseNotification
	if selectedLevel.Level.NotifyUser {
		notification = &model.CaseNotification{Status: model.NotificationPending}
	}

	event := model.CaseEvent{
		EventType:          model.CaseEventCreated,
		ActorDiscordUserID: actorDiscordUserID,
		ActorType:          attribution.actorType,
		Visibility:         model.EventVisibilityPublic,
		Body:               fmt.Sprintf("Case created from template %s", template.Template.Slug),
		MetadataJSON:       "{}",
	}

	params := model.CreateCaseParams{
		Case:             caseModel,
		Event:            event,
		ActionExecutions: actionExecutions,
		Evidence:         captured.Snapshots,
		Attachments:      captured.Attachments,
		Notification:     notification,
		Audit:            s.auditEntryWithAttribution(ctx, guildContext, attribution, "case.create", "case", "", model.AuditResultSuccess, ""),
	}
	if len(captured.Snapshots) > 0 {
		result := model.AuditResultSuccess
		failure := ""
		if len(captured.Warnings) > 0 {
			result = model.AuditResultFailure
			failure = "partial evidence capture"
		}
		entry := s.auditEntryWithAttribution(ctx, guildContext, attribution, "evidence.capture", "case_evidence", "", result, failure)
		if entry != nil {
			entry.MetadataJSON = mustMarshalJSONObject(map[string]any{
				"snapshot_count":   len(captured.Snapshots),
				"attachment_count": len(captured.Attachments),
				"partial":          len(captured.Warnings) > 0,
			})
			params.AdditionalAudits = append(params.AdditionalAudits, *entry)
		}
	}
	return s.store.CreateCase(ctx, params)
}

// selectTemplateLevel chooses the highest escalation whose configured
// case-count threshold is met by the target's matching history (including the
// case being created), falling back to the default level. Templates whose
// levels carry more than one action or a non-positive trigger are rejected.
func (s *CaseService) selectTemplateLevel(
	ctx context.Context,
	guildID, targetDiscordUserID string,
	template *model.ExpandedCaseTemplate,
) (*selectedTemplateLevel, error) {
	if template == nil {
		return nil, validationCaseError("template is required")
	}

	var fallback *selectedTemplateLevel
	var best *selectedTemplateLevel
	matchedCaseCount, err := s.matchingTemplateCaseCount(ctx, guildID, targetDiscordUserID, template.Template)
	if err != nil {
		return nil, err
	}
	for _, expandedLevel := range template.Levels {
		level := expandedLevel.Level
		if len(expandedLevel.Actions) > 1 {
			return nil, validationCaseError("template level has more than one enforcement action")
		}
		if level.IsDefault {
			fallback = &selectedTemplateLevel{
				Level:            level,
				Actions:          expandedLevel.Actions,
				MatchedCaseCount: matchedCaseCount,
			}
			continue
		}

		if level.TriggerCaseCount <= 0 {
			return nil, validationCaseError("escalation level trigger_case_count must be positive")
		}

		if matchedCaseCount < int64(level.TriggerCaseCount) {
			continue
		}
		candidate := &selectedTemplateLevel{
			Level:            level,
			Actions:          expandedLevel.Actions,
			MatchedCaseCount: matchedCaseCount,
		}
		if best == nil || level.TriggerCaseCount > best.Level.TriggerCaseCount {
			best = candidate
		}
	}

	if fallback == nil {
		return nil, validationCaseError("template has no default level")
	}
	if best != nil {
		return best, nil
	}

	return fallback, nil
}

// matchingTemplateCaseCount returns the target's valid prior cases for this
// template (within the decay window when one is configured) plus one for the
// case currently being created, matching the user-facing meaning of a trigger count.
func (s *CaseService) matchingTemplateCaseCount(
	ctx context.Context,
	guildID, targetDiscordUserID string,
	template model.CaseTemplate,
) (int64, error) {
	var since *time.Time
	if template.CaseDecayDays > 0 {
		cutoff := time.Now().UTC().Add(-time.Duration(template.CaseDecayDays) * 24 * time.Hour)
		since = &cutoff
	}
	priorCount, err := s.store.CountTemplateCasesForTarget(ctx, model.CountTemplateCasesForTargetParams{
		GuildID:             guildID,
		TemplateID:          template.ID,
		CreatedAtOrAfter:    since,
		TargetDiscordUserID: targetDiscordUserID,
	})
	if err != nil {
		return 0, err
	}
	return priorCount + 1, nil
}

// buildTemplateSnapshot serializes the immutable policy record stored on a
// case: the template identity and reason, the selected level with the count
// that chose it, the level's action settings, and the context schema and values.
// Later template edits never change what a historical case displays.
func buildTemplateSnapshot(
	template model.CaseTemplate,
	fields []model.CaseTemplateContextField,
	valuesJSON string,
	selectedLevel selectedTemplateLevel,
) (string, error) {
	snapshot := CaseTemplateSnapshotResponse{
		Template: templateSnapshotTemplate{
			ID:             template.ID,
			Slug:           template.Slug,
			Name:           template.Name,
			Version:        template.Version,
			ReasonTemplate: template.ReasonTemplate,
			CaseDecayDays:  template.CaseDecayDays,
			Appealable:     template.Appealable,
		},
		SelectedLevel: CaseSelectedLevel{
			TemplateLevelDetails: templateLevelDetails(selectedLevel.Level),
			MatchedCaseCount:     selectedLevel.MatchedCaseCount,
		},
		Actions:       make([]templateSnapshotAction, 0, len(selectedLevel.Actions)),
		ContextFields: make([]TemplateContextFieldResponse, 0, len(fields)),
	}
	// valuesJSON was produced by validateCaseContextValues, so it always decodes;
	// a malformed value would only leave ContextValues empty in the snapshot.
	_ = json.Unmarshal([]byte(valuesJSON), &snapshot.ContextValues)
	for _, field := range fields {
		snapshot.ContextFields = append(snapshot.ContextFields, TemplateContextFieldResponse{
			ID:        field.ID,
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Position:  field.Position,
			Required:  field.Required,
		})
	}

	for _, action := range selectedLevel.Actions {
		settings := templateActionResponse(action)
		snapshot.Actions = append(snapshot.Actions, templateSnapshotAction{
			ID:                     action.ID,
			ActionType:             action.ActionType,
			TimeoutDurationSeconds: settings.TimeoutDurationSeconds,
			DeleteMessageSeconds:   settings.DeleteMessageSeconds,
			MaxRetries:             action.MaxRetries,
		})
	}

	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal case template snapshot: %w", err)
	}
	return string(body), nil
}

// audit writes one staff-attributed audit row for a case operation. It returns
// the storage error so callers can decide whether a lost audit fails the request.
func (s *CaseService) audit(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) error {
	attribution := caseCreateAttribution{actorType: "staff"}
	return s.auditWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
}

// auditWithAttribution appends case evidence without inventing a Discord actor
// for system automation. A missing guild or staff context records nothing.
func (s *CaseService) auditWithAttribution(
	ctx context.Context,
	guildContext *GuildStaffContext,
	attribution caseCreateAttribution,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) error {
	entry := s.auditEntryWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
	if entry == nil {
		return nil
	}
	return recordAudit(ctx, s.store, entry)
}

// auditEntry builds, without persisting, the staff-attributed audit row that
// repository writes attach to their transaction.
func (s *CaseService) auditEntry(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) *model.AuditLogEntry {
	attribution := caseCreateAttribution{actorType: "staff"}
	return s.auditEntryWithAttribution(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
}

// auditEntryWithAttribution builds the atomic case audit row for either a
// current staff actor or Quack's restricted honeypot system actor. It returns
// nil when the guild or staff context is missing; an empty resource ID is
// recorded as "unknown".
func (s *CaseService) auditEntryWithAttribution(
	ctx context.Context,
	guildContext *GuildStaffContext,
	attribution caseCreateAttribution,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) *model.AuditLogEntry {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil
	}
	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	actorDiscordUserID := guildContext.Staff.DiscordUserID
	permissionBits := guildContext.PermissionBits
	if attribution.system {
		actorDiscordUserID = ""
		permissionBits = 0
	}

	entry := &model.AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  actorDiscordUserID,
		ActorPermissionBits: permissionBits,
		Source:              AuditSourceFromContext(ctx),
		Action:              action,
		ResourceType:        resourceType,
		ResourceID:          resourceID,
		Result:              result,
		FailureReason:       failureReason,
		CorrelationID:       correlationID,
		RequestID:           requestID,
		MetadataJSON:        "{}",
	}
	if entry.ResourceID == "" {
		entry.ResourceID = "unknown"
	}
	return entry
}

// ensureTraceContext guarantees the context carries both a request ID and a
// correlation ID, generating whichever is missing, so every row written by a
// case operation can be joined to the same trace.
func ensureTraceContext(ctx context.Context) context.Context {
	if idutil.RequestIDFromContext(ctx) != "" && idutil.CorrelationIDFromContext(ctx) != "" {
		return ctx
	}
	return idutil.ContextWithTrace(ctx, idutil.RequestIDFromContext(ctx), idutil.CorrelationIDFromContext(ctx))
}
