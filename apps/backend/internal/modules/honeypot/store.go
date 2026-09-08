package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Trigger records one message claim and its terminal outcome without storing message content.
type Trigger struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_honeypot_trigger,priority:1;index"`
	ChannelDiscordID     string    `gorm:"size:32;not null"`
	MessageDiscordID     string    `gorm:"size:32;not null;uniqueIndex:idx_honeypot_trigger,priority:2"`
	TargetDiscordUserID  string    `gorm:"size:32;not null"`
	TemplateID           string    `gorm:"type:char(26);not null"`
	CaseID               string    `gorm:"type:char(26)"`
	Outcome              Outcome   `gorm:"size:32;not null"`
	FailureCode          string    `gorm:"size:64"`
	CreatedAt, UpdatedAt time.Time `gorm:"not null"`
}

// TableName keeps honeypot outcomes out of moderation case and optional-module tables.
func (Trigger) TableName() string { return "honeypot_triggers" }

// Store owns only honeypot trigger claims and derived statistics.
type Store struct{ db *gorm.DB }

// NewStore constructs isolated trigger persistence around a caller-owned connection.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Migration exposes logical migration 0300 for integration into the production ledger.
func Migration() modules.Migration {
	return modules.Migration{Version: 300, Name: "honeypot_triggers", Apply: func(db *gorm.DB) error {
		return db.AutoMigrate(&Trigger{})
	}}
}

// Claim atomically deduplicates a Discord message before any moderation side effect.
func (s *Store) Claim(ctx context.Context, message Message, templateID string, outcome Outcome) (*Trigger, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("honeypot database is not connected")
	}
	now := time.Now().UTC()
	record := Trigger{ID: ulid.Make().String(), GuildID: message.GuildID, ChannelDiscordID: message.ChannelDiscordID, MessageDiscordID: message.MessageDiscordID, TargetDiscordUserID: message.AuthorDiscordUserID, TemplateID: templateID, Outcome: outcome, CreatedAt: now, UpdatedAt: now}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return nil, false, result.Error
	}
	return &record, result.RowsAffected == 1, nil
}

// Complete transitions one claimed message to a terminal outcome.
func (s *Store) Complete(ctx context.Context, id string, outcome Outcome, caseID, failureCode string) error {
	result := s.db.WithContext(ctx).Model(&Trigger{}).Where("id = ? AND outcome = ?", id, OutcomePending).Updates(map[string]any{"outcome": outcome, "case_id": caseID, "failure_code": failureCode, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDuplicate
	}
	return nil
}

// Statistics derives per-guild counts without reading cases or other modules.
func (s *Store) Statistics(ctx context.Context, guildID string) (Statistics, error) {
	if s == nil || s.db == nil {
		return Statistics{}, errors.New("honeypot database is not connected")
	}
	type count struct {
		Outcome Outcome
		Count   uint64
	}
	var rows []count
	if err := s.db.WithContext(ctx).Model(&Trigger{}).Select("outcome, count(*) AS count").Where("guild_id = ?", guildID).Group("outcome").Scan(&rows).Error; err != nil {
		return Statistics{}, err
	}
	stats := Statistics{}
	for _, row := range rows {
		stats.Total += row.Count
		switch row.Outcome {
		case OutcomePending:
			stats.Pending += row.Count
		case OutcomeCreated:
			stats.Created += row.Count
		case OutcomeFailed:
			stats.Failed += row.Count
		case OutcomeExempt:
			stats.Exempt += row.Count
		}
	}
	return stats, nil
}

// ClaimIncident groups a member's burst into one moderation incident. The guild
// configuration row serializes claims across workers; no Discord work holds the
// lock. Failed case creation does not suppress a later message's retry.
func (s *Store) ClaimIncident(ctx context.Context, message Message, templateID string) (*Trigger, bool, error) {
	var trigger *Trigger
	var claimed bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND module_id = ?", message.GuildID, modules.Honeypots).First(&config).Error; err != nil {
			return err
		}
		// The service's earlier settings read may predate an administrator edit.
		// Resolve the policy again under the claim lock before reserving an incident.
		if !config.Enabled {
			return ErrDisabled
		}
		var settings Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return err
		}
		settings = normalizeSettings(settings)
		if settings.ChannelDiscordID != message.ChannelDiscordID || settings.TemplateID != templateID {
			return ErrNotTrigger
		}
		if isExempt(message, settings) {
			return ErrExempt
		}
		var recent Trigger
		result := tx.Where("guild_id = ? AND target_discord_user_id = ? AND channel_discord_id = ? AND created_at > ? AND outcome IN ?", message.GuildID, message.AuthorDiscordUserID, message.ChannelDiscordID, time.Now().UTC().Add(-30*time.Second), []Outcome{OutcomePending, OutcomeCreated}).Order("created_at DESC").Limit(1).Find(&recent)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			trigger = &recent
			return nil
		}
		var err error
		trigger, claimed, err = NewStore(tx).Claim(ctx, message, templateID, OutcomePending)
		return err
	})
	return trigger, claimed, err
}
