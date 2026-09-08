package store

import (
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// getCaseTemplateExpanded retrieves case template expanded without exposing the underlying adapter implementation.
func getCaseTemplateExpanded(db *gorm.DB, guildID, templateID string) (*ExpandedCaseTemplate, error) {
	var templateRecord CaseTemplateRecord
	if err := db.Where("id = ? AND guild_id = ?", templateID, guildID).First(&templateRecord).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template: %w", err)
	}

	// Only adopted historical databases have quarantined pre-release templates.
	if db.Migrator().HasTable(&migration0002TemplateCompatibility{}) {
		var compatibility migration0002TemplateCompatibility
		compatibilityResult := db.Where("template_id = ?", templateRecord.ID).Limit(1).Find(&compatibility)
		if compatibilityResult.Error != nil {
			return nil, fmt.Errorf("get case template compatibility state: %w", compatibilityResult.Error)
		}
		if compatibilityResult.RowsAffected > 0 {
			return nil, &model.TemplateCompatibilityReviewError{
				TemplateID: templateRecord.ID,
				Reason:     compatibility.Reason,
			}
		}
	}

	template := caseTemplateModelFromRecord(templateRecord)
	var contextFields []model.CaseTemplateContextField
	if err := db.Where("template_id = ?", template.ID).Order("position ASC").Find(&contextFields).Error; err != nil {
		return nil, fmt.Errorf("get case template context fields: %w", err)
	}
	var levelRecords []CaseTemplateLevelRecord
	if err := db.Where("template_id = ?", template.ID).Order("position ASC").Find(&levelRecords).Error; err != nil {
		return nil, fmt.Errorf("get case template levels: %w", err)
	}

	expandedLevels := make([]ExpandedCaseTemplateLevel, 0, len(levelRecords))
	for _, levelRecord := range levelRecords {
		level := caseTemplateLevelModelFromRecord(levelRecord)
		var actionRecords []CaseTemplateLevelActionRecord
		if err := db.Where("level_id = ?", level.ID).Order("position ASC").Find(&actionRecords).Error; err != nil {
			return nil, fmt.Errorf("get case template level actions: %w", err)
		}
		actions := make([]model.CaseTemplateLevelAction, 0, len(actionRecords))
		for _, actionRecord := range actionRecords {
			actions = append(actions, caseTemplateLevelActionModelFromRecord(actionRecord))
		}
		expandedLevels = append(expandedLevels, ExpandedCaseTemplateLevel{
			Level:   level,
			Actions: actions,
		})
	}

	return &ExpandedCaseTemplate{
		Template: template, ContextFields: contextFields, Levels: expandedLevels,
	}, nil
}

// createTemplateContextFields persists validated definitions in their stable display order.
func createTemplateContextFields(tx *gorm.DB, templateID string, fields []model.CaseTemplateContextField, now time.Time) error {
	for i := range fields {
		field := fields[i]
		field.TemplateID = templateID
		if err := prepareULIDModel(&field.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare template context field: %w", err)
		}
		if err := tx.Select("*").Create(&field).Error; err != nil {
			return fmt.Errorf("create template context field: %w", err)
		}
	}
	return nil
}

// createTemplateLevels creates template levels while preserving validation, authorization, and persistence invariants.
func createTemplateLevels(tx *gorm.DB, templateID string, levels []ExpandedCaseTemplateLevel, now time.Time) error {
	for i := range levels {
		level := levels[i].Level
		level.TemplateID = templateID
		if err := prepareULIDModel(&level.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case template level model: %w", err)
		}
		levelRecord := caseTemplateLevelRecordFromModel(level)
		if err := tx.Select("*").Create(&levelRecord).Error; err != nil {
			return fmt.Errorf("create case template level: %w", err)
		}

		for j := range levels[i].Actions {
			action := levels[i].Actions[j]
			action.LevelID = level.ID
			if err := prepareULIDModel(&action.ULIDModel, now); err != nil {
				return fmt.Errorf("prepare case template level action model: %w", err)
			}
			actionRecord := caseTemplateLevelActionRecordFromModel(action)
			if err := tx.Select("*").Create(&actionRecord).Error; err != nil {
				return fmt.Errorf("create case template level action: %w", err)
			}
		}
	}

	return nil
}

// caseTemplateRecordFromModel maps the live template model into the compatibility storage shape.
func caseTemplateRecordFromModel(template model.CaseTemplate) CaseTemplateRecord {
	return CaseTemplateRecord{
		ULIDModelRecord:        ulidRecordFromModel(template.ULIDModel),
		GuildID:                template.GuildID,
		Slug:                   template.Slug,
		Name:                   template.Name,
		Description:            template.Description,
		ReasonTemplate:         template.ReasonTemplate,
		CaseDecayDays:          template.CaseDecayDays,
		DefaultSeverity:        "medium",
		Appealable:             template.Appealable,
		Enabled:                true,
		Version:                template.Version,
		CreatedByDiscordUserID: template.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: template.UpdatedByDiscordUserID,
		ArchivedAt:             template.ArchivedAt,
	}
}

// caseTemplateModelFromRecord omits retired compatibility columns from live behavior.
func caseTemplateModelFromRecord(record CaseTemplateRecord) model.CaseTemplate {
	return model.CaseTemplate{
		ULIDModel:              ulidModelFromRecord(record.ULIDModelRecord),
		GuildID:                record.GuildID,
		Slug:                   record.Slug,
		Name:                   record.Name,
		Description:            record.Description,
		ReasonTemplate:         record.ReasonTemplate,
		CaseDecayDays:          record.CaseDecayDays,
		Appealable:             record.Appealable,
		Version:                record.Version,
		CreatedByDiscordUserID: record.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: record.UpdatedByDiscordUserID,
		ArchivedAt:             record.ArchivedAt,
	}
}

// caseTemplateLevelRecordFromModel stores live level state with inert compatibility defaults.
func caseTemplateLevelRecordFromModel(level model.CaseTemplateLevel) CaseTemplateLevelRecord {
	return CaseTemplateLevelRecord{
		ULIDModelRecord:  ulidRecordFromModel(level.ULIDModel),
		TemplateID:       level.TemplateID,
		Position:         level.Position,
		Name:             level.Name,
		IsDefault:        level.IsDefault,
		TriggerCaseCount: level.TriggerCaseCount,
		NotifyUser:       level.NotifyUser,
		Enabled:          true,
	}
}

// caseTemplateLevelModelFromRecord omits retired compatibility columns from live behavior.
func caseTemplateLevelModelFromRecord(record CaseTemplateLevelRecord) model.CaseTemplateLevel {
	return model.CaseTemplateLevel{
		ULIDModel:        ulidModelFromRecord(record.ULIDModelRecord),
		TemplateID:       record.TemplateID,
		Position:         record.Position,
		Name:             record.Name,
		IsDefault:        record.IsDefault,
		TriggerCaseCount: record.TriggerCaseCount,
		NotifyUser:       record.NotifyUser,
	}
}

// caseTemplateLevelActionRecordFromModel stores one live action with inert compatibility defaults.
func caseTemplateLevelActionRecordFromModel(action model.CaseTemplateLevelAction) CaseTemplateLevelActionRecord {
	return CaseTemplateLevelActionRecord{
		ULIDModelRecord:  ulidRecordFromModel(action.ULIDModel),
		LevelID:          action.LevelID,
		Position:         1,
		ActionType:       action.ActionType,
		ConfigJSON:       action.ConfigJSON,
		MaxRetries:       action.MaxRetries,
		IdempotencyScope: "case",
		Enabled:          true,
	}
}

// caseTemplateLevelActionModelFromRecord omits retired compatibility columns from live behavior.
func caseTemplateLevelActionModelFromRecord(record CaseTemplateLevelActionRecord) model.CaseTemplateLevelAction {
	return model.CaseTemplateLevelAction{
		ULIDModel:  ulidModelFromRecord(record.ULIDModelRecord),
		LevelID:    record.LevelID,
		ActionType: record.ActionType,
		ConfigJSON: record.ConfigJSON,
		MaxRetries: record.MaxRetries,
	}
}

// ulidRecordFromModel maps shared identifier and timestamp fields into storage.
func ulidRecordFromModel(value model.ULIDModel) ULIDModelRecord {
	return ULIDModelRecord{ID: value.ID, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

// ulidModelFromRecord maps shared identifier and timestamp fields into the domain.
func ulidModelFromRecord(value ULIDModelRecord) model.ULIDModel {
	return model.ULIDModel{ID: value.ID, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
