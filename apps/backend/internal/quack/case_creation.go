package quack

import (
	"context"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack/model"
)

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
	_, correlationID := TraceIDsFromContext(ctx)

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
