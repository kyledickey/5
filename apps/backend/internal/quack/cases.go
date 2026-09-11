package quack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

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
// submitted immediately; a nil store is a programming error and panics.
func NewCaseService(store CaseRepository, scheduler CaseWorkScheduler) *CaseService {
	if store == nil {
		panic("quack: NewCaseService requires a non-nil CaseRepository")
	}
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
	attribution := caseCreateAttribution{actorType: "staff", auditSource: model.AuditSourceAPI}
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
	attribution := caseCreateAttribution{actorType: "system", auditSource: model.AuditSourceSystem, system: true}
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
			_ = s.authorizer.auditAuthorizationDenialWithMetadata(
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

	response := caseResponse(*created)
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
