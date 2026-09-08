package store

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompareAndSetEvidenceChannel saves only an expected-old evidence destination.
// Concurrent administrator changes win; the caller receives that current channel
// instead. A losing newly-created Discord channel is left untouched for inspection.
func (s *Store) CompareAndSetEvidenceChannel(ctx context.Context, guildID, expected, next string) (string, error) {
	if next == "" {
		return "", errors.New("evidence channel receipt is required")
	}
	var winner string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current GuildSettingsRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guildID).First(&current).Error; err != nil {
			return err
		}
		winner = current.ManagedEvidenceChannelDiscordID
		if winner != expected || winner == next {
			return nil
		}
		now := time.Now().UTC()
		if err := tx.Model(&current).Where("guild_id = ?", guildID).Updates(map[string]any{"managed_evidence_channel_discord_id": next, "updated_at": now}).Error; err != nil {
			return err
		}
		winner = next
		return createAuditLogEntry(tx, &model.AuditLogEntry{GuildID: guildID, Source: model.AuditSourceSystem, Action: "evidence_channel.ensure", ResourceType: "guild_settings", ResourceID: current.ID, Result: model.AuditResultSuccess, MetadataJSON: "{}"}, now)
	})
	return winner, err
}
