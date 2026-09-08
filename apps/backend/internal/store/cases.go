package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// retiredCaseEventTypes are preserved compatibility rows that must not cross the live v5 event boundary.
var retiredCaseEventTypes = []string{"note_added", "note_edited", "note_deleted", "status_changed"}

// CreateCaseParams aliases the core create case params contract so Store satisfies the port without maintaining a second data shape.
type CreateCaseParams = model.CreateCaseParams

// CreatedCase aliases the core created case contract so Store satisfies the port without maintaining a second data shape.
type CreatedCase = model.CreatedCase

// CountTemplateCasesForTargetParams aliases the core count template cases for target params contract so Store satisfies the port without maintaining a second data shape.
type CountTemplateCasesForTargetParams = model.CountTemplateCasesForTargetParams

// ListCasesParams aliases the core list cases params contract so Store satisfies the port without maintaining a second data shape.
type ListCasesParams = model.ListCasesParams

// ListCasesResult aliases the core list cases result contract so Store satisfies the port without maintaining a second data shape.
type ListCasesResult = model.ListCasesResult

// TargetCaseSummary aliases the core target case summary contract so Store satisfies the port without maintaining a second data shape.
type TargetCaseSummary = model.TargetCaseSummary

// ClaimedCaseAction aliases the core claimed case action contract so Store satisfies the port without maintaining a second data shape.
type ClaimedCaseAction = model.ClaimedCaseAction

// ClaimCaseActionParams aliases the core claim case action params contract so Store satisfies the port without maintaining a second data shape.
type ClaimCaseActionParams = model.ClaimCaseActionParams

// CompleteCaseActionParams aliases the core complete case action params contract so Store satisfies the port without maintaining a second data shape.
type CompleteCaseActionParams = model.CompleteCaseActionParams

// SkipCaseActionsParams aliases the core skip case actions params contract so Store satisfies the port without maintaining a second data shape.
type SkipCaseActionsParams = model.SkipCaseActionsParams

// CreateCase creates case while preserving validation, authorization, and persistence invariants.
func (s *Store) CreateCase(ctx context.Context, params CreateCaseParams) (*CreatedCase, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	caseModel := params.Case
	if err := prepareULIDModel(&caseModel.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare case model: %w", err)
	}
	if caseModel.Validity == "" {
		caseModel.Validity = model.CaseValidityValid
	}
	if caseModel.Source == "" {
		caseModel.Source = model.CaseSourceDashboard
	}
	if caseModel.TemplateSnapshotJSON == "" {
		caseModel.TemplateSnapshotJSON = "{}"
	}
	if caseModel.MetadataJSON == "" {
		caseModel.MetadataJSON = "{}"
	}
	if caseModel.ContextValuesJSON == "" {
		caseModel.ContextValuesJSON = "[]"
	}

	event := params.Event
	actionExecutions := make([]model.CaseActionExecution, len(params.ActionExecutions))
	copy(actionExecutions, params.ActionExecutions)
	evidence := make([]model.CaseEvidenceSnapshot, len(params.Evidence))
	copy(evidence, params.Evidence)
	attachments := make([]model.CaseEvidenceAttachment, len(params.Attachments))
	copy(attachments, params.Attachments)
	var notification *model.CaseNotification

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		caseNumber, err := nextCaseNumber(tx, caseModel.GuildID)
		if err != nil {
			return err
		}
		caseModel.CaseNumber = caseNumber

		if err := tx.Select("*").Create(&caseModel).Error; err != nil {
			return fmt.Errorf("create case: %w", err)
		}

		event.CaseID = caseModel.ID
		event.GuildID = caseModel.GuildID
		if event.EventType == "" {
			event.EventType = model.CaseEventCreated
		}
		if event.ActorType == "" {
			event.ActorType = "staff"
		}
		if event.Visibility == "" {
			event.Visibility = model.EventVisibilityStaff
		}
		if event.MetadataJSON == "" {
			event.MetadataJSON = "{}"
		}
		if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case event model: %w", err)
		}
		if err := tx.Select("*").Create(&event).Error; err != nil {
			return fmt.Errorf("create case event: %w", err)
		}

		for i := range actionExecutions {
			actionExecutions[i].CaseID = caseModel.ID
			if actionExecutions[i].Status == "" {
				actionExecutions[i].Status = model.ActionExecutionPending
			}
			if actionExecutions[i].ConfigSnapshotJSON == "" {
				actionExecutions[i].ConfigSnapshotJSON = "{}"
			}
			if actionExecutions[i].IdempotencyKey == "" {
				actionExecutions[i].IdempotencyKey = fmt.Sprintf("case:%s:action:%d", caseModel.ID, actionExecutions[i].Position)
			}
			if actionExecutions[i].CorrelationID == "" {
				actionExecutions[i].CorrelationID = caseModel.CorrelationID
			}
			if err := prepareULIDModel(&actionExecutions[i].ULIDModel, now); err != nil {
				return fmt.Errorf("prepare case action execution model: %w", err)
			}
		}
		if len(actionExecutions) > 0 {
			if err := tx.Select("*").Create(&actionExecutions).Error; err != nil {
				return fmt.Errorf("create case action executions: %w", err)
			}
		}

		for i := range evidence {
			evidence[i].CaseID = caseModel.ID
			evidence[i].GuildID = caseModel.GuildID
			if evidence[i].EmbedsJSON == "" {
				evidence[i].EmbedsJSON = "[]"
			}
			if err := prepareULIDModel(&evidence[i].ULIDModel, now); err != nil {
				return err
			}
			if err := tx.Select("*").Create(&evidence[i]).Error; err != nil {
				return fmt.Errorf("create case evidence: %w", err)
			}
		}
		for i := range attachments {
			if attachments[i].EvidenceID == "" {
				return errors.New("evidence attachment has no snapshot")
			}
			if err := prepareULIDModel(&attachments[i].ULIDModel, now); err != nil {
				return err
			}
			if err := tx.Select("*").Create(&attachments[i]).Error; err != nil {
				return fmt.Errorf("create case evidence attachment: %w", err)
			}
		}
		if params.Notification != nil {
			copyValue := *params.Notification
			copyValue.CaseID = caseModel.ID
			if copyValue.Status == "" {
				copyValue.Status = model.NotificationPending
			}
			if err := prepareULIDModel(&copyValue.ULIDModel, now); err != nil {
				return err
			}
			if err := tx.Select("*").Create(&copyValue).Error; err != nil {
				return fmt.Errorf("create case notification: %w", err)
			}
			notification = &copyValue
		}
		if caseModel.ReplacesCaseID != nil {
			result := tx.Model(&model.Case{}).Where("id = ? AND guild_id = ? AND status = ? AND replacement_case_id IS NULL", *caseModel.ReplacesCaseID, caseModel.GuildID, model.CaseValidityVoided).Update("replacement_case_id", caseModel.ID)
			if result.Error != nil {
				return fmt.Errorf("link replacement case: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return errors.New("replacement case is no longer available")
			}
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = caseModel.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}
		for i := range params.AdditionalAudits {
			entry := params.AdditionalAudits[i]
			if entry.ResourceID == "" || entry.ResourceID == "unknown" {
				entry.ResourceID = caseModel.ID
			}
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &CreatedCase{
		Case:             caseModel,
		Event:            event,
		ActionExecutions: actionExecutions,
		Evidence:         evidence, Attachments: attachments, Notification: notification,
	}, nil
}

// VoidCase atomically changes only case validity and appends immutable correction history.
func (s *Store) VoidCase(ctx context.Context, params model.VoidCaseParams) (*model.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var item model.Case
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", params.CaseID, params.GuildID).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		if item.Validity == model.CaseValidityVoided {
			if item.VoidedReason == params.Reason {
				return nil
			}
			return errors.New("case is already voided with a different reason")
		}
		item.Validity = model.CaseValidityVoided
		item.VoidedReason = params.Reason
		item.VoidedByDiscordUserID = params.ActorDiscordUserID
		item.VoidedAt = &now
		item.ReplacementCaseID = params.ReplacementCaseID
		item.UpdatedAt = now
		if err := tx.Select("*").Save(&item).Error; err != nil {
			return err
		}
		if err := cancelVoidedCaseWork(tx, item.ID, now); err != nil {
			return err
		}
		if err := queueVoidedCaseReversals(tx, item, now); err != nil {
			return err
		}
		event := model.CaseEvent{CaseID: item.ID, EventType: model.CaseEventVoided, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: model.EventVisibilityPublic, Body: "Case voided", MetadataJSON: marshalJSONObject(map[string]any{"reason": params.Reason, "replacement_case_id": params.ReplacementCaseID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = item.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// firstNonEmpty encapsulates the first non empty rule so callers share one consistent package implementation.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// nextCaseNumber encapsulates the next case number rule so callers share one consistent package implementation.
func nextCaseNumber(tx *gorm.DB, guildID string) (uint64, error) {
	var latest model.Case
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("guild_id = ?", guildID).
		Order("case_number DESC").
		Limit(1).
		Find(&latest).Error
	if err != nil {
		return 0, fmt.Errorf("get latest case number: %w", err)
	}
	if latest.CaseNumber == 0 {
		return 1, nil
	}
	return latest.CaseNumber + 1, nil
}

// appendCaseEvent encapsulates the append case event rule so callers share one consistent package implementation.
func appendCaseEvent(tx *gorm.DB, event *model.CaseEvent, now time.Time) error {
	var caseModel model.Case
	result := tx.Where("id = ?", event.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for event: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	event.GuildID = caseModel.GuildID
	if event.Visibility == "" {
		event.Visibility = model.EventVisibilityStaff
	}
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
		return fmt.Errorf("prepare case event model: %w", err)
	}
	if err := tx.Select("*").Create(event).Error; err != nil {
		return fmt.Errorf("create case event: %w", err)
	}
	return nil
}

// marshalJSONObject serializes marshal jsonobject into its stable external representation.
func marshalJSONObject(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}
