package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	mysqlerrors "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const starterPolicySlug = "general-rule-violation"

// GetGuildSettings returns core settings with enablement projected from the
// canonical module envelopes, including changes made through native setup.
func (s *Store) GetGuildSettings(ctx context.Context, guildID string) (*model.GuildSettings, error) {
	var record model.GuildSettings
	result := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Limit(1).Find(&record)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild settings: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	if err := loadCanonicalModuleFlags(s.db.WithContext(ctx), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// UpdateGuildSettings atomically replaces validated core settings, applies only
// explicit canonical module toggles, and appends success audit evidence.
func (s *Store) UpdateGuildSettings(ctx context.Context, params model.UpdateGuildSettingsParams) (*model.GuildSettings, error) {
	var record model.GuildSettings
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", params.Settings.GuildID).First(&record).Error; err != nil {
			return fmt.Errorf("get guild settings for update: %w", err)
		}
		record.AppealRejoinURL = params.Settings.AppealRejoinURL
		record.AppealQueueChannelDiscordID = params.Settings.AppealQueueChannelDiscordID
		record.AppealReviewReasonRequired = params.Settings.AppealReviewReasonRequired
		record.AuditMirrorChannelDiscordID = params.Settings.AuditMirrorChannelDiscordID
		record.ManagedEvidenceChannelDiscordID = params.Settings.ManagedEvidenceChannelDiscordID
		record.NotificationIntroduction = params.Settings.NotificationIntroduction
		record.NotificationFooter = params.Settings.NotificationFooter
		if err := applyCanonicalModuleToggles(tx, params.Settings.GuildID, params.ModuleToggles, now); err != nil {
			return err
		}
		record.StarterPolicyNoticePending = params.Settings.StarterPolicyNoticePending
		record.StarterPolicyNoticeAcknowledgedAt = params.Settings.StarterPolicyNoticeAcknowledgedAt
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("update guild settings: %w", err)
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
		return nil, err
	}
	if err := loadCanonicalModuleFlags(s.db.WithContext(ctx), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// ClearGuildChannelReferences blanks every core settings field that points at
// channelID (appeal queue, audit mirror, managed evidence) under a row lock and
// appends the audit row. When nothing referenced the channel no write or audit
// happens.
func (s *Store) ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *model.AuditLogEntry) (*model.GuildSettings, error) {
	var record model.GuildSettings
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guildID).First(&record).Error; err != nil {
			return fmt.Errorf("get guild settings for channel repair: %w", err)
		}
		changed := false
		if record.AppealQueueChannelDiscordID == channelID {
			record.AppealQueueChannelDiscordID = ""
			changed = true
		}
		if record.AuditMirrorChannelDiscordID == channelID {
			record.AuditMirrorChannelDiscordID = ""
			changed = true
		}
		if record.ManagedEvidenceChannelDiscordID == channelID {
			record.ManagedEvidenceChannelDiscordID = ""
			changed = true
		}
		if !changed {
			return nil
		}
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("clear guild channel references: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.GuildID = guildID
			entry.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := loadCanonicalModuleFlags(s.db.WithContext(ctx), &record); err != nil {
		return nil, err
	}
	return &record, nil
}

// BootstrapGuild is the guild-join and ready-event entry point: it creates or
// reactivates the guild, seeds guild settings and the exact starter policy on
// first bootstrap, drops channel references that are no longer in
// KnownChannelDiscordIDs, and audits each of those lifecycle changes. Repeat
// calls are idempotent and report what was created.
func (s *Store) BootstrapGuild(ctx context.Context, params model.BootstrapGuildParams) (*model.BootstrapGuildResult, error) {
	// Concurrent ready/guild-create events can deadlock while locking a guild
	// that does not exist yet. MySQL rolls back the victim transaction in full,
	// so retry only that definite rollback, with fresh result flags each time.
	for attempt := 0; ; attempt++ {
		result, err := s.bootstrapGuildOnce(ctx, params)
		var dbErr *mysqlerrors.MySQLError
		if attempt >= 4 || !errors.As(err, &dbErr) || dbErr.Number != 1213 {
			return result, err
		}
		timer := time.NewTimer(time.Duration(10<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// bootstrapGuildOnce owns one atomic attempt; no external effects occur before
// commit and failed attempts never leak created flags into a later retry.
func (s *Store) bootstrapGuildOnce(ctx context.Context, params model.BootstrapGuildParams) (*model.BootstrapGuildResult, error) {
	result := &model.BootstrapGuildResult{}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild model.Guild
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("discord_guild_id = ?", params.DiscordGuildID).Limit(1).Find(&guild)
		if query.Error != nil {
			return fmt.Errorf("get guild for bootstrap: %w", query.Error)
		}
		wasActive := query.RowsAffected > 0 && guild.IsActive
		if query.RowsAffected == 0 {
			guild = model.Guild{DiscordGuildID: params.DiscordGuildID}
			prepareULIDModel(&guild.ULIDModel, now)
			result.GuildCreated = true
		}
		guild.Name = params.Name
		guild.IconURL = params.IconURL
		guild.OwnerDiscordUserID = params.OwnerDiscordUserID
		guild.IsActive = true
		guild.UpdatedAt = now
		if query.RowsAffected == 0 {
			if err := tx.Create(&guild).Error; err != nil {
				return fmt.Errorf("create bootstrap guild: %w", err)
			}
		} else if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("refresh bootstrap guild: %w", err)
		}

		var settingsRecord model.GuildSettings
		settingsQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guild.ID).Limit(1).Find(&settingsRecord)
		if settingsQuery.Error != nil {
			return fmt.Errorf("get bootstrap settings: %w", settingsQuery.Error)
		}
		if settingsQuery.RowsAffected == 0 {
			settingsRecord = model.GuildSettings{GuildID: guild.ID, StarterPolicyNoticePending: true}
			prepareULIDModel(&settingsRecord.ULIDModel, now)
			if err := tx.Create(&settingsRecord).Error; err != nil {
				return fmt.Errorf("create bootstrap settings: %w", err)
			}
		}

		if settingsRecord.StarterPolicyTemplateID == "" {
			starter, created, err := ensureStarterPolicy(tx, guild.ID, now)
			if err != nil {
				return err
			}
			settingsRecord.StarterPolicyTemplateID = starter.Template.ID
			settingsRecord.StarterPolicyNoticePending = true
			settingsRecord.UpdatedAt = now
			if err := tx.Save(&settingsRecord).Error; err != nil {
				return fmt.Errorf("bind starter policy to guild settings: %w", err)
			}
			result.StarterTemplate = *starter
			result.StarterTemplateCreated = created
			if created {
				err := createSystemLifecycleAudit(tx, guild.ID, "case_template.bootstrap", "case_template", starter.Template.ID, now)
				if err != nil {
					return err
				}
			}
		} else {
			starter, err := getCaseTemplateExpanded(tx, guild.ID, settingsRecord.StarterPolicyTemplateID)
			if err != nil {
				return err
			}
			if starter == nil {
				return errors.New("configured starter policy template is missing")
			}
			result.StarterTemplate = *starter
		}

		channelReferencesRepaired := false
		if params.KnownChannelDiscordIDs != nil {
			known := make(map[string]struct{}, len(params.KnownChannelDiscordIDs))
			for _, id := range params.KnownChannelDiscordIDs {
				known[id] = struct{}{}
			}
			if settingsRecord.AppealQueueChannelDiscordID != "" {
				if _, ok := known[settingsRecord.AppealQueueChannelDiscordID]; !ok {
					settingsRecord.AppealQueueChannelDiscordID = ""
					channelReferencesRepaired = true
				}
			}
			if settingsRecord.AuditMirrorChannelDiscordID != "" {
				if _, ok := known[settingsRecord.AuditMirrorChannelDiscordID]; !ok {
					settingsRecord.AuditMirrorChannelDiscordID = ""
					channelReferencesRepaired = true
				}
			}
			if settingsRecord.ManagedEvidenceChannelDiscordID != "" {
				if _, ok := known[settingsRecord.ManagedEvidenceChannelDiscordID]; !ok {
					settingsRecord.ManagedEvidenceChannelDiscordID = ""
					channelReferencesRepaired = true
				}
			}
			settingsRecord.UpdatedAt = now
			if err := tx.Save(&settingsRecord).Error; err != nil {
				return fmt.Errorf("repair bootstrap channel references: %w", err)
			}
		}
		if channelReferencesRepaired {
			err := createSystemLifecycleAudit(tx, guild.ID, "guild_settings.channel_references.repaired", "guild_settings", settingsRecord.ID, now)
			if err != nil {
				return err
			}
		}
		if result.GuildCreated || !wasActive {
			if err := createSystemLifecycleAudit(tx, guild.ID, "guild.lifecycle.bootstrap", "guild", guild.ID, now); err != nil {
				return err
			}
		}

		result.Guild = guild
		result.Settings = settingsRecord
		return loadCanonicalModuleFlags(tx, &result.Settings)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// createSystemLifecycleAudit appends adapter-attributed lifecycle evidence inside the caller's transaction.
func createSystemLifecycleAudit(tx *gorm.DB, guildID, action, resourceType, resourceID string, now time.Time) error {
	return createAuditLogEntry(tx, &model.AuditLogEntry{
		GuildID:            guildID,
		ActorDiscordUserID: "quack-system",
		Source:             model.AuditSourceDiscord,
		Action:             action,
		ResourceType:       resourceType,
		ResourceID:         resourceID,
		Result:             model.AuditResultSuccess,
		MetadataJSON:       "{}",
	}, now)
}

// DeactivateGuild flags a guild inactive when the bot leaves it. Nothing the
// guild owns is deleted, so a rejoin resumes with full history. A guild that
// was never seen returns (nil, nil).
func (s *Store) DeactivateGuild(ctx context.Context, discordGuildID string, audit *model.AuditLogEntry) (*model.Guild, error) {
	var guild model.Guild
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("discord_guild_id = ?", discordGuildID).
			First(&guild).Error; err != nil {
			return fmt.Errorf("get guild for deactivation: %w", err)
		}
		guild.IsActive = false
		guild.UpdatedAt = now
		if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("deactivate guild: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.GuildID = guild.ID
			entry.ResourceID = guild.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
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
	return &guild, nil
}

// ensureStarterPolicy creates the exact editable v5 starter template or returns its existing identity on repeated bootstrap.
func ensureStarterPolicy(tx *gorm.DB, guildID string, now time.Time) (*model.ExpandedCaseTemplate, bool, error) {
	var existing model.CaseTemplate
	query := tx.Where("guild_id = ? AND slug = ?", guildID, starterPolicySlug).Limit(1).Find(&existing)
	if query.Error != nil {
		return nil, false, fmt.Errorf("find starter policy: %w", query.Error)
	}
	if query.RowsAffected > 0 {
		expanded, err := getCaseTemplateExpanded(tx, guildID, existing.ID)
		if err != nil {
			return nil, false, err
		}
		if expanded == nil || !isExactStarterPolicy(*expanded) {
			return nil, false, errors.New("general-rule-violation slug is already used by a non-starter policy")
		}
		return expanded, false, nil
	}

	template := model.CaseTemplate{
		GuildID:                guildID,
		Slug:                   starterPolicySlug,
		Name:                   "General rule violation",
		Description:            "A starter rule for general violations. Review and customize it for this guild.",
		ReasonTemplate:         "General rule violation",
		Appealable:             true,
		Version:                1,
		CreatedByDiscordUserID: "quack-system",
		UpdatedByDiscordUserID: "quack-system",
	}
	prepareULIDModel(&template.ULIDModel, now)
	if err := tx.Select("*").Create(&template).Error; err != nil {
		return nil, false, fmt.Errorf("create starter policy: %w", err)
	}
	levels := starterPolicyLevels()
	if err := createTemplateLevels(tx, template.ID, levels, now); err != nil {
		return nil, false, err
	}
	expanded, err := getCaseTemplateExpanded(tx, guildID, template.ID)
	return expanded, true, err
}

// starterPolicyLevels returns the product-defined case-only, timeout, and ban escalation policy.
func starterPolicyLevels() []model.ExpandedCaseTemplateLevel {
	return []model.ExpandedCaseTemplateLevel{
		{
			Level: model.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true, TriggerCaseCount: 0, NotifyUser: true},
		},
		{
			Level: model.CaseTemplateLevel{Position: 2, Name: "24-hour timeout", TriggerCaseCount: 3, NotifyUser: true},
			Actions: []model.CaseTemplateLevelAction{
				{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":86400}`, MaxRetries: 0},
			},
		},
		{
			Level: model.CaseTemplateLevel{Position: 3, Name: "Ban", TriggerCaseCount: 5, NotifyUser: true},
			Actions: []model.CaseTemplateLevelAction{
				{ActionType: model.ActionBanUser, ConfigJSON: `{"delete_message_seconds":86400}`, MaxRetries: 0},
			},
		},
	}
}

// isExactStarterPolicy prevents an unrelated policy from being silently adopted when the reserved starter slug already exists.
func isExactStarterPolicy(template model.ExpandedCaseTemplate) bool {
	want := starterPolicyLevels()
	header := template.Template
	if header.Name != "General rule violation" || header.ReasonTemplate != "General rule violation" ||
		!header.Appealable || header.ArchivedAt != nil || len(template.Levels) != len(want) {
		return false
	}
	for i := range want {
		gotLevel, wantLevel := template.Levels[i], want[i]
		if gotLevel.Level.IsDefault != wantLevel.Level.IsDefault ||
			gotLevel.Level.TriggerCaseCount != wantLevel.Level.TriggerCaseCount ||
			!gotLevel.Level.NotifyUser ||
			len(gotLevel.Actions) != len(wantLevel.Actions) {
			return false
		}
		if len(wantLevel.Actions) == 1 &&
			(gotLevel.Actions[0].ActionType != wantLevel.Actions[0].ActionType || gotLevel.Actions[0].ConfigJSON != wantLevel.Actions[0].ConfigJSON) {
			return false
		}
	}
	return true
}

// loadCanonicalModuleFlags projects the shared module envelopes; obsolete guild
// booleans never override configuration written by native module setup.
func loadCanonicalModuleFlags(db *gorm.DB, settings *model.GuildSettings) error {
	var configs []modules.Configuration
	core := []modules.ID{modules.Tickets, modules.GeneralLogging, modules.Honeypots}
	if err := db.Select("module_id, enabled").Where("guild_id = ? AND module_id IN ?", settings.GuildID, core).Find(&configs).Error; err != nil {
		return err
	}
	settings.TicketsEnabled, settings.GeneralLoggingEnabled, settings.HoneypotEnabled = false, false, false
	for _, config := range configs {
		switch config.ModuleID {
		case modules.Tickets:
			settings.TicketsEnabled = config.Enabled
		case modules.GeneralLogging:
			settings.GeneralLoggingEnabled = config.Enabled
		case modules.Honeypots:
			settings.HoneypotEnabled = config.Enabled
		}
	}
	return nil
}

// applyCanonicalModuleToggles locks existing module rows and changes only enabled.
// All changes share the core-settings/audit transaction; configuration bytes,
// identities, and omitted module flags survive concurrent native setup safely.
func applyCanonicalModuleToggles(tx *gorm.DB, guildID string, toggles []model.GuildModuleToggle, now time.Time) error {
	for _, toggle := range toggles {
		id := modules.ID(toggle.ModuleID)
		if id != modules.Tickets && id != modules.GeneralLogging && id != modules.Honeypots {
			return errors.New("unknown guild module")
		}
		var config modules.Configuration
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND module_id = ?", guildID, id).Limit(1).Find(&config)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected == 0 {
			if toggle.Enabled {
				return &model.GuildModuleConfigurationError{Message: fmt.Sprintf("%s is not configured; run /setup first", id)}
			}
			continue
		}
		if toggle.Enabled && (toggle.ExpectedConfigJSON == "" || config.ConfigJSON != toggle.ExpectedConfigJSON) {
			return &model.GuildModuleConfigurationError{Message: fmt.Sprintf("%s configuration changed; reload settings and try again", id)}
		}
		if err := tx.Model(&modules.Configuration{}).Where("id = ?", config.ID).
			Updates(map[string]any{"enabled": toggle.Enabled, "updated_at": now}).Error; err != nil {
			return err
		}
	}
	return nil
}
