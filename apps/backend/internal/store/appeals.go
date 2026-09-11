package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetGuildAppealSettings returns a guild's configured future appeal form, or nil to select the product default.
func (s *Store) GetGuildAppealSettings(ctx context.Context, guildID string) (*model.GuildAppealSettings, error) {
	var record GuildAppealSettingsRecord
	result := s.db.WithContext(ctx).Where("guild_id = ?", guildID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return guildAppealSettingsModel(record), nil
}

// UpdateGuildAppealSettings replaces only the form used by future submissions and appends its audit record atomically.
func (s *Store) UpdateGuildAppealSettings(ctx context.Context, params model.UpdateGuildAppealSettingsParams) (*model.GuildAppealSettings, error) {
	now := time.Now().UTC()
	settings := params.Settings
	if err := prepareULIDModel(&settings.ULIDModel, now); err != nil {
		return nil, err
	}
	record := guildAppealSettingsRecord(settings)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing GuildAppealSettingsRecord
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", settings.GuildID).First(&existing)
		if result.Error == nil {
			record.ID = existing.ID
			record.CreatedAt = existing.CreatedAt
			record.UpdatedAt = now
			if err := tx.Select("questions_json", "updated_by_discord_user_id", "updated_at").Updates(&record).Error; err != nil {
				return err
			}
		} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else {
			return result.Error
		}
		audit := params.Audit
		audit.ResourceID = record.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return guildAppealSettingsModel(record), nil
}

// CreateAppeal inserts one case-unique appeal with its first immutable event, public case history, audit, and staff notification.
func (s *Store) CreateAppeal(ctx context.Context, params model.CreateAppealParams) (*model.Appeal, error) {
	now := time.Now().UTC()
	appeal := params.Appeal
	if err := prepareULIDModel(&appeal.ULIDModel, now); err != nil {
		return nil, err
	}
	event := params.Event
	event.AppealID = appeal.ID
	event.GuildID = appeal.GuildID
	if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
		return nil, err
	}
	notification := params.Notification
	notification.AppealID = appeal.ID
	notification.EventID = event.ID
	notification.GuildID = appeal.GuildID
	if err := prepareULIDModel(&notification.ULIDModel, now); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if appeal.CaseID == nil || strings.TrimSpace(*appeal.CaseID) == "" {
			return model.ErrAppealCaseIneligible
		}
		var item CaseRecord
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND guild_id = ?", *appeal.CaseID, appeal.GuildID).
			First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return model.ErrAppealCaseIneligible
		}
		if result.Error != nil {
			return result.Error
		}
		ineligible := item.TargetDiscordUserID != appeal.TargetDiscordUserID ||
			item.Validity != model.CaseValidityValid ||
			!snapshotAppealable(item.TemplateSnapshotJSON)
		if ineligible {
			return model.ErrAppealCaseIneligible
		}
		var existing int64
		if err := tx.Model(&AppealRecord{}).Where("case_id = ?", item.ID).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return model.ErrAppealAlreadyExists
		}
		if err := tx.Create(appealRecord(appeal)).Error; err != nil {
			if isDuplicateError(err) {
				return model.ErrAppealAlreadyExists
			}
			return err
		}
		if err := tx.Create(appealEventRecord(event)).Error; err != nil {
			return err
		}
		caseEvent := params.CaseEvent
		caseEvent.CaseID = item.ID
		caseEvent.GuildID = item.GuildID
		if err := appendCaseEvent(tx, &caseEvent, now); err != nil {
			return err
		}
		if err := tx.Create(appealNotificationRecord(notification)).Error; err != nil {
			return err
		}
		audit := params.Audit
		audit.ResourceID = appeal.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

// GetAppealByID returns one appeal without applying caller authorization.
func (s *Store) GetAppealByID(ctx context.Context, appealID string) (*model.Appeal, error) {
	var record AppealRecord
	result := s.db.WithContext(ctx).Where("id = ?", appealID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return appealModel(record), nil
}

// GetAppealByCaseID returns the only appeal for a case, if any.
func (s *Store) GetAppealByCaseID(ctx context.Context, caseID string) (*model.Appeal, error) {
	var record AppealRecord
	result := s.db.WithContext(ctx).Where("case_id = ?", caseID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return appealModel(record), nil
}

// ListAppeals returns stable newest-first staff queue pagination for one guild.
func (s *Store) ListAppeals(ctx context.Context, params model.AppealListParams) (*model.AppealListResult, error) {
	query := s.db.WithContext(ctx).Model(&AppealRecord{}).Where("guild_id = ?", params.GuildID)
	if params.Status != "" {
		query = query.Where("status = ?", params.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var records []AppealRecord
	if err := query.Order("created_at DESC, id DESC").Limit(params.Limit).Offset(params.Offset).Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]model.Appeal, 0, len(records))
	for _, record := range records {
		items = append(items, *appealModel(record))
	}
	return &model.AppealListResult{Appeals: items, Total: total}, nil
}

// ListAppealEvents returns one immutable timeline in creation order.
func (s *Store) ListAppealEvents(ctx context.Context, appealID string) ([]model.AppealEvent, error) {
	var records []AppealEventRecord
	if err := s.db.WithContext(ctx).Where("appeal_id = ?", appealID).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]model.AppealEvent, 0, len(records))
	for _, record := range records {
		items = append(items, appealEventModel(record))
	}
	return items, nil
}

// TransitionAppeal moves a pending appeal to accepted or rejected under a row
// lock, bumping its version with an optimistic check so two concurrent
// decisions cannot both succeed. Accepting (params.VoidCase) also voids the
// case, cancels its pending work, and queues reversals in the same
// transaction. It appends the appeal event, the member and staff outbox
// rows, and the audit rows, and asks the staff queue message to refresh.
func (s *Store) TransitionAppeal(ctx context.Context, params model.TransitionAppealParams) (*model.Appeal, error) {
	now := time.Now().UTC()
	var updated AppealRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND guild_id = ?", params.AppealID, params.GuildID).
			First(&updated)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return model.ErrAppealStateConflict
		}
		if result.Error != nil {
			return result.Error
		}
		terminal := params.To == model.AppealStatusAccepted || params.To == model.AppealStatusRejected
		invalid := updated.Status != model.AppealStatusPending ||
			!appealStatusIn(updated.Status, params.AllowedFrom) ||
			!terminal ||
			params.VoidCase != (params.To == model.AppealStatusAccepted)
		if invalid {
			return model.ErrAppealStateConflict
		}
		if params.VoidCase {
			if updated.CaseID == nil {
				return model.ErrAppealCaseIneligible
			}
			var item CaseRecord
			caseResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("id = ? AND guild_id = ?", *updated.CaseID, params.GuildID).
				First(&item)
			if caseResult.Error != nil || item.Validity != model.CaseValidityValid {
				return model.ErrAppealStateConflict
			}
			caseUpdates := map[string]any{
				"status":                    model.CaseValidityVoided,
				"voided_reason":             "Appeal accepted",
				"voided_by_discord_user_id": params.ActorDiscordUserID,
				"voided_at":                 now,
				"updated_at":                now,
			}
			voided := tx.Model(&CaseRecord{}).Where("id = ? AND status = ?", item.ID, model.CaseValidityValid).Updates(caseUpdates)
			if voided.Error != nil || voided.RowsAffected != 1 {
				return model.ErrAppealStateConflict
			}
			if err := cancelVoidedCaseWork(tx, item.ID, now); err != nil {
				return err
			}
			caseEvent := model.CaseEvent{
				CaseID:             item.ID,
				GuildID:            item.GuildID,
				EventType:          model.CaseEventVoided,
				ActorDiscordUserID: params.ActorDiscordUserID,
				ActorType:          "staff",
				Visibility:         model.EventVisibilityPublic,
				Body:               "Case voided after appeal accepted",
				MetadataJSON:       "{}",
			}
			if err := appendCaseEvent(tx, &caseEvent, now); err != nil {
				return err
			}
			if params.CaseAudit != nil {
				caseAudit := *params.CaseAudit
				caseAudit.ResourceID = item.ID
				if err := createAuditLogEntry(tx, &caseAudit, now); err != nil {
					return err
				}
			}
		}
		updated.Status = params.To
		updated.DecisionReason = params.Reason
		updated.ReviewedByDiscordUserID = params.ActorDiscordUserID
		updated.ReviewedAt = &now
		updated.Version++
		updated.UpdatedAt = now
		result = tx.Model(&AppealRecord{}).Where("id = ? AND version = ?", updated.ID, updated.Version-1).Updates(map[string]any{
			"status":                      updated.Status,
			"decision_reason":             updated.DecisionReason,
			"reviewed_by_discord_user_id": updated.ReviewedByDiscordUserID,
			"reviewed_at":                 now,
			"version":                     updated.Version,
			"updated_at":                  now,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			return model.ErrAppealStateConflict
		}
		if params.VoidCase {
			var item model.Case
			if err := tx.Where("id = ?", *updated.CaseID).First(&item).Error; err != nil {
				return err
			}
			if err := queueVoidedCaseReversals(tx, item, now); err != nil {
				return err
			}
		}
		event := params.Event
		event.AppealID = updated.ID
		event.GuildID = updated.GuildID
		if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealEventRecord(event)).Error; err != nil {
			return err
		}
		notification := params.Notification
		notification.AppealID = updated.ID
		notification.EventID = event.ID
		notification.GuildID = updated.GuildID
		if err := prepareULIDModel(&notification.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealNotificationRecord(notification)).Error; err != nil {
			return err
		}
		// Preserve an in-flight send's lease and request another pass after it
		// finishes. Idle queue messages can be refreshed immediately.
		staffRows := tx.Model(&AppealNotificationRecord{}).Where("appeal_id = ? AND audience = ?", updated.ID, model.AppealNotificationStaff)
		if err := staffRows.Updates(map[string]any{
			"refresh_requested": true,
			"status":            gorm.Expr("CASE WHEN status = ? THEN ? ELSE status END", model.AppealNotificationSent, model.AppealNotificationPending),
			"updated_at":        now,
		}).Error; err != nil {
			return err
		}
		audit := params.AppealAudit
		audit.ResourceID = updated.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return appealModel(updated), nil
}

// snapshotAppealable reads the appealable flag frozen into a case's template
// snapshot; later template edits must not change an existing case's eligibility.
func snapshotAppealable(body string) bool {
	var snapshot struct {
		Template struct {
			Appealable bool `json:"appealable"`
		} `json:"template"`
	}
	return json.Unmarshal([]byte(body), &snapshot) == nil && snapshot.Template.Appealable
}

func appealStatusIn(status model.AppealStatus, allowed []model.AppealStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

// isDuplicateError recognizes unique-constraint violations from both MySQL
// ("Duplicate entry") and SQLite ("UNIQUE constraint failed") by message text,
// since neither driver exposes a portable sentinel.
func isDuplicateError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate") || strings.Contains(text, "unique constraint")
}
