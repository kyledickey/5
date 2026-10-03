package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

func (s *Store) CreateCaseTemplate(ctx context.Context, params model.CreateCaseTemplateParams) (*model.ExpandedCaseTemplate, error) {
	now := time.Now().UTC()
	template := params.Template
	prepareULIDModel(&template.ULIDModel, now)
	template.Version = 1

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Select("*").Create(&template).Error; err != nil {
			return fmt.Errorf("create case template: %w", err)
		}

		if err := createTemplateLevels(tx, template.ID, params.Levels, now); err != nil {
			return err
		}
		if err := createTemplateContextFields(tx, template.ID, params.ContextFields, now); err != nil {
			return err
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = template.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, template.GuildID, template.ID)
}

func (s *Store) ListCaseTemplates(ctx context.Context, guildID string) ([]model.ExpandedCaseTemplate, error) {
	var records []model.CaseTemplate
	if err := s.db.WithContext(ctx).
		Where("guild_id = ?", guildID).
		Order("slug ASC").
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case templates: %w", err)
	}

	expanded := make([]model.ExpandedCaseTemplate, 0, len(records))
	for _, record := range records {
		item, err := s.GetCaseTemplateExpanded(ctx, guildID, record.ID)
		if err != nil {
			return nil, err
		}
		if item != nil {
			expanded = append(expanded, *item)
		}
	}

	return expanded, nil
}

func (s *Store) GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*model.ExpandedCaseTemplate, error) {
	return getCaseTemplateExpanded(s.db.WithContext(ctx), guildID, templateID)
}

func (s *Store) GetCaseTemplateBySlug(ctx context.Context, guildID, slug string) (*model.CaseTemplate, error) {
	var record model.CaseTemplate
	if err := s.db.WithContext(ctx).Where("guild_id = ? AND slug = ?", guildID, slug).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template by slug: %w", err)
	}

	return &record, nil
}

func (s *Store) UpdateCaseTemplate(ctx context.Context, params model.UpdateCaseTemplateParams) (*model.ExpandedCaseTemplate, error) {
	now := time.Now().UTC()
	var record model.CaseTemplate
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", params.TemplateID, params.GuildID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for update: %w", err)
		}

		expected := params.ExpectedVersion
		if expected == 0 {
			expected = record.Version
		}
		if record.Version != expected {
			return model.ErrTemplateConflict
		}

		record.Slug = params.Template.Slug
		record.Name = params.Template.Name
		record.Description = params.Template.Description
		record.ReasonTemplate = params.Template.ReasonTemplate
		record.CaseDecayDays = params.Template.CaseDecayDays
		record.Appealable = params.Template.Appealable
		record.UpdatedByDiscordUserID = params.Template.UpdatedByDiscordUserID
		record.Version++
		record.UpdatedAt = now

		// Claim the next version before replacing children. A concurrent writer
		// must fail this comparison, leaving both policy and audit untouched.
		result := tx.Model(&model.CaseTemplate{}).Where("id = ? AND guild_id = ? AND version = ?", record.ID, params.GuildID, expected).
			Select("slug", "name", "description", "reason_template", "case_decay_days", "appealable", "updated_by_discord_user_id", "version", "updated_at").Updates(&record)
		if result.Error != nil {
			return fmt.Errorf("update case template: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return model.ErrTemplateConflict
		}

		levelIDs := tx.Model(&model.CaseTemplateLevel{}).Select("id").Where("template_id = ?", record.ID)
		if err := tx.Where("level_id IN (?)", levelIDs).Delete(&model.CaseTemplateLevelAction{}).Error; err != nil {
			return fmt.Errorf("replace case template level actions: %w", err)
		}
		if err := tx.Where("template_id = ?", record.ID).Delete(&model.CaseTemplateLevel{}).Error; err != nil {
			return fmt.Errorf("replace case template levels: %w", err)
		}
		if err := createTemplateLevels(tx, record.ID, params.Levels, now); err != nil {
			return err
		}
		if err := tx.Where("template_id = ?", record.ID).Delete(&model.CaseTemplateContextField{}).Error; err != nil {
			return fmt.Errorf("replace case template context fields: %w", err)
		}
		if err := createTemplateContextFields(tx, record.ID, params.ContextFields, now); err != nil {
			return err
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, params.GuildID, record.ID)
}

// RestoreCaseTemplate makes an archived template available again without changing its identity or version.
func (s *Store) RestoreCaseTemplate(ctx context.Context, guildID, templateID string, audit *model.AuditLogEntry) (*model.ExpandedCaseTemplate, error) {
	now := time.Now().UTC()
	var record model.CaseTemplate
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND guild_id = ?", templateID, guildID).First(&record)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return fmt.Errorf("get case template for restore: %w", result.Error)
		}
		record.ArchivedAt = nil
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("restore case template: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
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
	return s.GetCaseTemplateExpanded(ctx, guildID, templateID)
}

func (s *Store) ArchiveCaseTemplate(ctx context.Context, guildID, templateID string, audit *model.AuditLogEntry) (*model.ExpandedCaseTemplate, error) {
	now := time.Now().UTC()
	var record model.CaseTemplate
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", templateID, guildID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for archive: %w", err)
		}

		record.ArchivedAt = &now
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("archive case template: %w", err)
		}

		if audit != nil {
			audit := *audit
			audit.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, guildID, record.ID)
}

func getCaseTemplateExpanded(db *gorm.DB, guildID, templateID string) (*model.ExpandedCaseTemplate, error) {
	var template model.CaseTemplate
	if err := db.Where("id = ? AND guild_id = ?", templateID, guildID).First(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template: %w", err)
	}

	var contextFields []model.CaseTemplateContextField
	if err := db.Where("template_id = ?", template.ID).Order("position ASC").Find(&contextFields).Error; err != nil {
		return nil, fmt.Errorf("get case template context fields: %w", err)
	}
	var levels []model.CaseTemplateLevel
	if err := db.Where("template_id = ?", template.ID).Order("position ASC").Find(&levels).Error; err != nil {
		return nil, fmt.Errorf("get case template levels: %w", err)
	}

	expandedLevels := make([]model.ExpandedCaseTemplateLevel, 0, len(levels))
	for _, level := range levels {
		var actions []model.CaseTemplateLevelAction
		if err := db.Where("level_id = ?", level.ID).Find(&actions).Error; err != nil {
			return nil, fmt.Errorf("get case template level actions: %w", err)
		}
		expandedLevels = append(expandedLevels, model.ExpandedCaseTemplateLevel{Level: level, Actions: actions})
	}

	return &model.ExpandedCaseTemplate{Template: template, ContextFields: contextFields, Levels: expandedLevels}, nil
}

// createTemplateContextFields persists validated definitions in their stable display order.
func createTemplateContextFields(tx *gorm.DB, templateID string, fields []model.CaseTemplateContextField, now time.Time) error {
	for i := range fields {
		field := fields[i]
		field.TemplateID = templateID
		prepareULIDModel(&field.ULIDModel, now)
		if err := tx.Select("*").Create(&field).Error; err != nil {
			return fmt.Errorf("create template context field: %w", err)
		}
	}
	return nil
}

func createTemplateLevels(tx *gorm.DB, templateID string, levels []model.ExpandedCaseTemplateLevel, now time.Time) error {
	for i := range levels {
		level := levels[i].Level
		level.TemplateID = templateID
		prepareULIDModel(&level.ULIDModel, now)
		if err := tx.Select("*").Create(&level).Error; err != nil {
			return fmt.Errorf("create case template level: %w", err)
		}

		for j := range levels[i].Actions {
			action := levels[i].Actions[j]
			action.LevelID = level.ID
			prepareULIDModel(&action.ULIDModel, now)
			if err := tx.Select("*").Create(&action).Error; err != nil {
				return fmt.Errorf("create case template level action: %w", err)
			}
		}
	}
	return nil
}
