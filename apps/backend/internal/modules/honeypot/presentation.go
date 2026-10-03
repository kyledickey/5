package honeypot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WarningRefresh coalesces presentation work by guild independently of incident
// enforcement. SendIdentity is a durable fence for replacement POST uncertainty.
type WarningRefresh struct {
	GuildID       string    `gorm:"type:char(26);primaryKey"`
	Revision      string    `gorm:"type:char(26);not null"`
	Pending       bool      `gorm:"not null;index:idx_honeypot_warning_due,priority:1"`
	NextAttemptAt time.Time `gorm:"not null;index:idx_honeypot_warning_due,priority:2"`
	SendIdentity  string    `gorm:"size:64;not null"`
}

// TableName keeps presentation receipts owned by the honeypot feature.
func (WarningRefresh) TableName() string { return "honeypot_warning_refreshes" }

// ErrWarningDeliveryUnknown prevents a second replacement after an unconfirmed
// send. Administrative inspection is needed; changing warning text is not proof
// that the prior message was absent.
var ErrWarningDeliveryUnknown = errors.New("honeypot warning delivery is unconfirmed; inspect the configured channel")

// RequestWarningRefresh replaces only the requested generation, preserving an
// uncertain send fence and any retry backoff already in effect.
func (s *Service) RequestWarningRefresh(ctx context.Context, guildID string) error {
	return requestWarningRefresh(ctx, s.store.db, guildID)
}

// requestWarningRefresh accepts the incident transaction so successful case
// completion and its derived presentation request commit or roll back together.
func requestWarningRefresh(ctx context.Context, db *gorm.DB, guildID string) error {
	row := WarningRefresh{GuildID: guildID, Revision: ulid.Make().String(), Pending: true, NextAttemptAt: time.Now().UTC()}
	return db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "guild_id"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "pending"})}).Create(&row).Error
}

// WarningRefreshes returns one bounded due batch; no full configuration scan is
// needed during steady-state retries.
func (s *Service) WarningRefreshes(ctx context.Context, now time.Time) ([]WarningRefresh, error) {
	var rows []WarningRefresh
	err := s.store.db.WithContext(ctx).Where("pending = ? AND next_attempt_at <= ?", true, now.UTC()).Order("next_attempt_at, guild_id").Limit(8).Find(&rows).Error
	return rows, err
}

// CompleteWarningRefresh preserves a newer request arriving during delivery.
// Failures back off even if the generation changed, avoiding hot retry loops.
func (s *Service) CompleteWarningRefresh(ctx context.Context, row WarningRefresh, failed bool) error {
	query := s.store.db.WithContext(ctx).Model(&WarningRefresh{}).Where("guild_id = ?", row.GuildID)
	if failed {
		return query.Update("next_attempt_at", time.Now().UTC().Add(30*time.Second)).Error
	}
	return query.Where("revision = ?", row.Revision).Update("pending", false).Error
}

// ConfiguredWarningGuilds keyset-pages enabled configurations once per worker
// startup. The caller retains only a cursor and cancels between bounded pages.
func (s *Service) ConfiguredWarningGuilds(ctx context.Context, after string) ([]string, error) {
	var ids []string
	err := s.store.db.WithContext(ctx).Model(&modules.Configuration{}).Where("module_id = ? AND enabled = ? AND guild_id > ?", modules.Honeypots, true, after).Order("guild_id").Limit(32).Pluck("guild_id", &ids).Error
	return ids, err
}

// warningSendIdentity binds replacement admission to the configured destination
// and old receipt, allowing an explicit channel/receipt change to supersede it.
func warningSendIdentity(settings Settings) string {
	sum := sha256.Sum256([]byte(settings.ChannelDiscordID + "\x00" + settings.WarningMessageID))
	return hex.EncodeToString(sum[:])
}

// ReserveWarningSend persists admission before a non-idempotent replacement POST.
// It is called under the adapter's shared setup/refresh guild lock.
func (s *Service) ReserveWarningSend(ctx context.Context, guildID string, settings Settings) error {
	row := WarningRefresh{GuildID: guildID, Revision: ulid.Make().String(), NextAttemptAt: time.Now().UTC()}
	if err := s.store.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	identity := warningSendIdentity(settings)
	result := s.store.db.WithContext(ctx).Model(&WarningRefresh{}).Where("guild_id = ? AND send_identity <> ?", guildID, identity).Update("send_identity", identity)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWarningDeliveryUnknown
	}
	return nil
}

// ReleaseWarningSend permits another POST only after proven nondelivery. A failed
// receipt write or uncertain transport result deliberately leaves admission held.
func (s *Service) ReleaseWarningSend(ctx context.Context, guildID string, settings Settings) error {
	return s.store.db.WithContext(ctx).Model(&WarningRefresh{}).Where("guild_id = ? AND send_identity = ?", guildID, warningSendIdentity(settings)).Update("send_identity", "").Error
}

// RecordWarningReplacement records presentation repair only while its original
// configuration remains current, so it cannot overwrite a concurrent admin edit.
func (s *Service) RecordWarningReplacement(ctx context.Context, guildID string, previous Settings, messageID string) error {
	if messageID == "" {
		return errors.New("warning message ID is required")
	}
	return s.store.recordWarningReplacement(ctx, guildID, previous, messageID)
}

// recordWarningReplacement compares the warning identity under the configuration
// row lock, updating only the delivery receipt without producing staff audit noise.
func (s *Store) recordWarningReplacement(ctx context.Context, guildID string, previous Settings, messageID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND module_id = ?", guildID, modules.Honeypots).First(&config).Error; err != nil {
			return err
		}
		var current Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &current); err != nil {
			return err
		}
		if !config.Enabled || current.ChannelDiscordID != previous.ChannelDiscordID || current.WarningMessageID != previous.WarningMessageID || current.WarningText != previous.WarningText {
			return errors.New("honeypot warning configuration changed")
		}
		current.WarningMessageID = messageID
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		return tx.Model(&config).Update("config_json", string(encoded)).Error
	})
}
