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
	ErrCaseValidation           = errors.New("case validation failed")
	ErrCaseTemplateNotAvailable = errors.New("case template not available")
	ErrCasePermissionDenied     = errors.New("case permission denied")
	ErrCaseNotFound             = errors.New("case not found")
	errCasePreflightStale       = errors.New("case preflight became stale")
)

// CaseService owns case authorization, escalation selection, snapshots, auditing, and action scheduling.
type CaseService struct {
	store      CaseRepository
	scheduler  CaseWorkScheduler
	authorizer *GuildService
	evidence   *EvidenceService
}

// NewCaseService binds case persistence and an optional latency queue without starting workers.
func NewCaseService(store CaseRepository, scheduler ...CaseWorkScheduler) *CaseService {
	service := &CaseService{store: store}
	if len(scheduler) > 0 {
		service.scheduler = scheduler[0]
	}
	return service
}

// WithEvidenceCapture configures the shared pre-commit evidence service.
func (s *CaseService) WithEvidenceCapture(evidence *EvidenceService) *CaseService {
	if s != nil {
		s.evidence = evidence
	}
	return s
}

// Create applies a staff-attributed template to a user inside the guild-scoped
// transaction boundary. The lock keeps escalation history and case numbering
// consistent, while scheduling occurs only after the transaction commits.
func (s *CaseService) Create(ctx context.Context, guildContext *GuildStaffContext, input CaseInput) (*CaseResponse, error) {
	if input.Source == model.CaseSourceHoneypot {
		return nil, validationCaseError("honeypot cases require the system application boundary")
	}
	return s.createWithAttribution(ctx, guildContext, input, caseCreateAttribution{actorType: "staff", auditSource: model.AuditSourceAPI})
}

// CreateSystemHoneypot applies one honeypot template through the ordinary case
// transaction while attributing the operation to Quack itself. It is intended
// only for the injected optional-module adapter and rejects every other source.
func (s *CaseService) CreateSystemHoneypot(ctx context.Context, guildID string, input CaseInput) (*CaseResponse, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
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
	return s.createWithAttribution(ctx, systemContext, input, caseCreateAttribution{actorType: "system", auditSource: model.AuditSourceSystem, system: true})
}

// createWithAttribution owns the shared moderation path for staff and the
// narrowly scoped honeypot system boundary.
func (s *CaseService) createWithAttribution(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseCreateAttribution) (*CaseResponse, error) {
	ctx = ensureTraceContext(ctx)
	ctx = ContextWithAuditSource(ctx, AuditSourceForCaseSource(input.Source))
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
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
			if existing.TargetDiscordUserID != strings.TrimSpace(input.TargetDiscordUserID) || (existing.TemplateID != nil && *existing.TemplateID != strings.TrimSpace(input.TemplateID)) {
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
			_ = s.authorizer.auditAuthorizationDenialWithMetadata(ctx, guildContext, authorizationErr.Capability, AuditSourceFromContext(ctx), authorizationErr.Reason, authorizationErr.MetadataJSON)
		}
		if errors.Is(err, ErrCaseValidation) || errors.Is(err, ErrCasePermissionDenied) || errors.Is(err, ErrCaseTemplateNotAvailable) || errors.Is(err, errCasePreflightStale) {
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

// preflightCreate performs live authorization and evidence capture before the atomic case transaction.
func (s *CaseService) preflightCreate(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseCreateAttribution) (*caseCreatePreflight, error) {
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
	result := &caseCreatePreflight{TemplateID: template.Template.ID, TemplateVersion: template.Template.Version, SelectedLevelID: selected.Level.ID, ActionType: actionType, ContextValuesJSON: valuesJSON}
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
		captured, captureErr := s.evidence.capture(ctx, guildContext.Guild.DiscordGuildID, actorID, targetID, channelID, links)
		if captureErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			warning := "Evidence could not be saved. Add it to the case later."
			slog.WarnContext(ctx, "Case evidence capture failed", "guild_id", guildContext.Guild.ID)
			result.Captured = CapturedEvidence{
				Snapshots: []model.CaseEvidenceSnapshot{{CaptureOutcome: "unavailable", CaptureWarning: warning, MessageCreatedAt: time.Now().UTC(), EmbedsJSON: "[]"}},
				Warnings:  []string{warning},
			}
		}
		if captured != nil {
			result.Captured = *captured
		}
		uploads, uploadErr := s.evidence.CaptureUploads(ctx, guildContext.Guild.DiscordGuildID, actorID, channelID, input.Attachments)
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
func (s *CaseService) Void(ctx context.Context, guildContext *GuildStaffContext, caseRef, reason string, replacementCaseID *string) (response *CaseResponse, err error) {
	defer func() {
		if err == nil || s == nil || s.store == nil || guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
			return
		}
		result := model.AuditResultFailure
		if errors.Is(err, ErrCasePermissionDenied) || errors.Is(err, ErrAuthorizationDenied) {
			result = model.AuditResultDenied
		}
		_ = s.audit(ctx, guildContext, string(model.AuditActionCaseVoid), "case", strings.TrimSpace(caseRef), result, err.Error())
	}()
	if s == nil || s.store == nil {
		return nil, errors.New("case service is not configured")
	}
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
	voided, err := s.store.VoidCase(ctx, model.VoidCaseParams{GuildID: guildContext.Guild.ID, CaseID: item.ID, ActorDiscordUserID: guildContext.Staff.DiscordUserID, Reason: reason, ReplacementCaseID: replacementCaseID, Audit: s.auditEntry(ctx, guildContext, "case.void", "case", item.ID, model.AuditResultSuccess, "")})
	if err != nil {
		return nil, err
	}
	if voided == nil {
		return nil, ErrCaseNotFound
	}
	slog.InfoContext(ctx, "Case voided", "guild_id", voided.GuildID, "case_id", voided.ID, "case_number", voided.CaseNumber)
	actions, err := s.store.ListCaseActionExecutions(ctx, voided.ID)
	if err != nil {
		return nil, err
	}
	result := caseResponseFromModel(*voided, actions)
	return &result, nil
}

// requireCaseRead checks the service dependency and current guild read capability before loading case data.
func (s *CaseService) requireCaseRead(guildContext *GuildStaffContext) error {
	if s == nil || s.store == nil {
		return errors.New("case service is not configured")
	}
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return validationCaseError("missing guild context")
	}
	if !guildContext.Can(model.PermissionActionCaseRead) {
		return ErrCasePermissionDenied
	}
	return nil
}
