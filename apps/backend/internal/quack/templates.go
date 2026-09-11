package quack

import (
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

const (
	// MaxCaseDecayDays bounds the rolling window to 100 years and keeps duration arithmetic safe.
	MaxCaseDecayDays = 36500
	// MaxTemplateSafeRetries bounds the only execution control exposed to guild administrators.
	MaxTemplateSafeRetries = 10
	// MaxTimeoutDurationSeconds is Discord's maximum 28-day member timeout.
	MaxTimeoutDurationSeconds = 28 * 24 * 60 * 60
	// MaxBanDeleteMessageSeconds is Discord's maximum seven-day ban history deletion window.
	MaxBanDeleteMessageSeconds = 7 * 24 * 60 * 60
)

var (
	// ErrTemplateValidation reports a policy that failed normalization; the wrapped message is safe to show.
	ErrTemplateValidation = errors.New("template validation failed")
	// ErrTemplateConflict re-exports the store's stale-version rejection for Update.
	ErrTemplateConflict = model.ErrTemplateConflict
	// ErrTemplateNotFound reports a template that does not exist in the caller's guild.
	ErrTemplateNotFound = errors.New("case template not found")
	// ErrTemplatePermissionDenied reports that the caller lacks the template read or write capability.
	ErrTemplatePermissionDenied = errors.New("template permission denied")
	// ErrTemplateCompatibilityReviewRequired re-exports the store's error for
	// preserved legacy policy that cannot be projected as a valid live template.
	ErrTemplateCompatibilityReviewRequired = model.ErrTemplateCompatibilityReviewRequired
)

// TemplateCompatibilityReviewError re-exports the typed model error so adapters
// can errors.As against it without importing model.
type TemplateCompatibilityReviewError = model.TemplateCompatibilityReviewError

var templateSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

// TemplateService owns template validation, normalization, persistence, and audit creation.
type TemplateService struct {
	store TemplateRepository
}

// NewTemplateService returns a service over store; construction has no side effects.
func NewTemplateService(store TemplateRepository) *TemplateService {
	return &TemplateService{store: store}
}

// List returns every template in the caller's guild, archived ones included,
// for staff with the template read capability. Denials and failures are audited.
func (s *TemplateService) List(ctx context.Context, guildContext *GuildStaffContext) ([]TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errors.New("missing guild context")
	}
	const action = string(model.AuditActionTemplateRead)
	if !guildContext.Can(model.PermissionActionCaseTemplateRead) {
		_ = s.audit(ctx, guildContext, action, "case_template", "list", model.AuditResultDenied, ErrTemplatePermissionDenied.Error()) // best-effort: denial already returned
		return nil, ErrTemplatePermissionDenied
	}
	templates, err := s.store.ListCaseTemplates(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", "list", model.AuditResultFailure, "query_failed") // best-effort: storage error already returned
		return nil, err
	}

	out := make([]TemplateResponse, 0, len(templates))
	for _, template := range templates {
		out = append(out, templateResponse(template))
	}
	if err := s.audit(ctx, guildContext, action, "case_template", "list", model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// ListActive returns only templates currently available for new cases and Discord autocomplete.
func (s *TemplateService) ListActive(ctx context.Context, guildContext *GuildStaffContext) ([]TemplateResponse, error) {
	all, err := s.List(ctx, guildContext)
	if err != nil {
		return nil, err
	}
	active := make([]TemplateResponse, 0, len(all))
	for _, item := range all {
		if item.ArchivedAt == nil {
			active = append(active, item)
		}
	}
	return active, nil
}

// Get returns one template in the caller's guild for staff with the template
// read capability. A template in another guild is reported as not found.
func (s *TemplateService) Get(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errors.New("missing guild context")
	}
	const action = string(model.AuditActionTemplateRead)
	if !guildContext.Can(model.PermissionActionCaseTemplateRead) {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultDenied, ErrTemplatePermissionDenied.Error()) // best-effort: denial already returned
		return nil, ErrTemplatePermissionDenied
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, "query_failed") // best-effort: storage error already returned
		return nil, err
	}
	if template == nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, "not_found") // best-effort: not-found already returned
		return nil, ErrTemplateNotFound
	}

	response := templateResponse(*template)
	if err := s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &response, nil
}

// Create validates, normalizes, and persists a new guild template with its
// escalation levels and actions, then records the moderation audit entry.
func (s *TemplateService) Create(ctx context.Context, guildContext *GuildStaffContext, input TemplateInput) (*TemplateResponse, error) {
	const action = "case_template.create"
	if err := s.requireWrite(ctx, guildContext, action, ""); err != nil {
		return nil, err
	}

	ctx = ensureTraceContext(ctx)
	normalized, err := s.validate(ctx, guildContext, "", input)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", "unknown", model.AuditResultFailure, err.Error()) // best-effort: validation error already returned
		return nil, err
	}

	expanded, err := s.store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template:      normalized.template,
		ContextFields: normalized.contextFields,
		Levels:        normalized.levels,
		Audit:         s.auditEntry(ctx, guildContext, action, "case_template", "", model.AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}

	logTemplate(ctx, "Template created", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}

// Update replaces a policy only if its source version is still current. Existing case snapshots remain unchanged.
func (s *TemplateService) Update(
	ctx context.Context, guildContext *GuildStaffContext, templateID string, input TemplateInput,
) (*TemplateResponse, error) {
	const action = "case_template.update"
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}

	ctx = ensureTraceContext(ctx)
	existing, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrTemplateNotFound
	}

	if input.ExpectedVersion == 0 {
		input.ExpectedVersion = existing.Template.Version
	}

	normalized, err := s.validate(ctx, guildContext, templateID, input)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, err.Error()) // best-effort: validation error already returned
		return nil, err
	}

	expanded, err := s.store.UpdateCaseTemplate(ctx, model.UpdateCaseTemplateParams{
		GuildID:         guildContext.Guild.ID,
		TemplateID:      templateID,
		ExpectedVersion: input.ExpectedVersion,
		Template:        normalized.template,
		ContextFields:   normalized.contextFields,
		Levels:          normalized.levels,
		Audit:           s.auditEntry(ctx, guildContext, action, "case_template", templateID, model.AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	if expanded == nil {
		return nil, ErrTemplateNotFound
	}

	logTemplate(ctx, "Template updated", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}

// Restore reverses archive without changing the template identity or version.
func (s *TemplateService) Restore(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	const action = "case_template.restore"
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}

	ctx = ensureTraceContext(ctx)
	expanded, err := s.store.RestoreCaseTemplate(
		ctx, guildContext.Guild.ID, strings.TrimSpace(templateID),
		s.auditEntry(ctx, guildContext, action, "case_template", templateID, model.AuditResultSuccess, ""),
	)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, err.Error()) // best-effort: storage error already returned
		return nil, err
	}
	if expanded == nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, ErrTemplateNotFound.Error()) // best-effort: not-found already returned
		return nil, ErrTemplateNotFound
	}
	logTemplate(ctx, "Template restored", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}

// Export returns policy fields only, deliberately excluding guild identity, history, channels, audit data, and secrets.
func (s *TemplateService) Export(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplatePolicy, error) {
	const action = "case_template.export"
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}

	template, err := s.Get(ctx, guildContext, templateID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, err.Error()) // best-effort: read error already returned
		return nil, err
	}
	input := template.EditInput()
	policy := &TemplatePolicy{
		SchemaVersion:  1,
		CaseDecayDays:  input.CaseDecayDays,
		Slug:           input.Slug,
		Name:           input.Name,
		Description:    input.Description,
		OfficialReason: input.ReasonTemplate,
		Appealable:     input.Appealable,
		ContextFields:  input.ContextFields,
		Levels:         input.Levels,
	}
	if err := s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return policy, nil
}

// Import validates confirmed guild-neutral policy and creates a new active guild-owned template identity.
func (s *TemplateService) Import(
	ctx context.Context, guildContext *GuildStaffContext, input TemplateImportInput,
) (*TemplateResponse, error) {
	const action = "case_template.import"
	if err := s.requireWrite(ctx, guildContext, action, ""); err != nil {
		return nil, err
	}

	if !input.Confirm {
		err := validationError("template import must be explicitly confirmed")
		_ = s.audit(ctx, guildContext, action, "case_template", "unknown", model.AuditResultFailure, err.Error()) // best-effort: validation error already returned
		return nil, err
	}
	if input.Policy.SchemaVersion != 1 {
		err := validationError("unsupported template policy schema_version")
		_ = s.audit(ctx, guildContext, action, "case_template", "unknown", model.AuditResultFailure, err.Error()) // best-effort: validation error already returned
		return nil, err
	}
	normalized, err := s.validate(ctx, guildContext, "", TemplateInput{
		CaseDecayDays:  input.Policy.CaseDecayDays,
		Slug:           input.Policy.Slug,
		Name:           input.Policy.Name,
		Description:    input.Policy.Description,
		ReasonTemplate: input.Policy.OfficialReason,
		Appealable:     input.Policy.Appealable,
		ContextFields:  input.Policy.ContextFields,
		Levels:         input.Policy.Levels,
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", "unknown", model.AuditResultFailure, err.Error()) // best-effort: validation error already returned
		return nil, err
	}
	expanded, err := s.store.CreateCaseTemplate(ctx, model.CreateCaseTemplateParams{
		Template:      normalized.template,
		ContextFields: normalized.contextFields,
		Levels:        normalized.levels,
		Audit:         s.auditEntry(ctx, guildContext, action, "case_template", "", model.AuditResultSuccess, ""),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", "unknown", model.AuditResultFailure, err.Error()) // best-effort: storage error already returned
		return nil, err
	}
	logTemplate(ctx, "Template imported", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}

// Archive hides a template from new cases and autocomplete without deleting
// it; cases that snapshotted it keep their references.
func (s *TemplateService) Archive(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	const action = "case_template.archive"
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}

	ctx = ensureTraceContext(ctx)
	expanded, err := s.store.ArchiveCaseTemplate(
		ctx,
		guildContext.Guild.ID,
		templateID,
		s.auditEntry(ctx, guildContext, action, "case_template", templateID, model.AuditResultSuccess, ""),
	)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, err.Error()) // best-effort: storage error already returned
		return nil, err
	}
	if expanded == nil {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultFailure, ErrTemplateNotFound.Error()) // best-effort: not-found already returned
		return nil, ErrTemplateNotFound
	}

	logTemplate(ctx, "Template archived", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}

// logTemplate emits the structured info line shared by every successful template write.
func logTemplate(ctx context.Context, message string, expanded *model.ExpandedCaseTemplate) {
	slog.InfoContext(
		ctx, message,
		"guild_id", expanded.Template.GuildID, "template_id", expanded.Template.ID, "version", expanded.Template.Version,
	)
}

// audit records a template outcome that did not go through the store's
// transactional audit write (denials, validation failures, read outcomes).
func (s *TemplateService) audit(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) error {
	entry := s.auditEntry(ctx, guildContext, action, resourceType, resourceID, result, failureReason)
	if entry == nil {
		return nil
	}
	return recordAudit(ctx, s.store, entry)
}

// auditEntry builds a template audit row with trace ids and the actor's current
// permission bits. It returns nil when there is no staff row to attribute to;
// an empty resource id is recorded as "unknown".
func (s *TemplateService) auditEntry(
	ctx context.Context,
	guildContext *GuildStaffContext,
	action, resourceType, resourceID string,
	result model.AuditResult,
	failureReason string,
) *model.AuditLogEntry {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil
	}
	requestID, correlationID := TraceIDsFromContext(ctx)

	entry := &model.AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  guildContext.Staff.DiscordUserID,
		ActorPermissionBits: guildContext.PermissionBits,
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

// requireWrite enforces the manager boundary even when a non-HTTP adapter calls
// the service. Permission-sensitive denials are recorded without reading policy.
func (s *TemplateService) requireWrite(ctx context.Context, guildContext *GuildStaffContext, action, templateID string) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil ||
		!guildContext.Can(model.PermissionActionCaseTemplateWrite) {
		_ = s.audit(ctx, guildContext, action, "case_template", templateID, model.AuditResultDenied, "permission_denied") // best-effort: denial already returned
		return ErrTemplatePermissionDenied
	}
	return nil
}
