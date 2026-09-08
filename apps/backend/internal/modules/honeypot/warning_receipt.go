package honeypot

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RecordWarningReplacement records presentation repair only while its original
// configuration remains current. It never overwrites concurrent administrator edits.
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
