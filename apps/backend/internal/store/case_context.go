package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateCaseContext writes only the current context and its attribution event.
// The row lock prevents a context edit from overwriting a simultaneous void.
func (s *Store) UpdateCaseContext(ctx context.Context, guildID, caseRef, valuesJSON string, audit *model.AuditLogEntry) (*model.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var updated *model.Case
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.Case
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guildID)
		if number, err := strconv.ParseUint(caseRef, 10, 64); err == nil {
			query = query.Where("case_number = ?", number)
		} else {
			query = query.Where("id = ?", caseRef)
		}
		if err := query.First(&item).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&item).Updates(map[string]any{"context_values_json": valuesJSON, "updated_at": now}).Error; err != nil {
			return err
		}
		item.ContextValuesJSON, item.UpdatedAt = valuesJSON, now
		if audit != nil {
			entry := *audit
			entry.GuildID, entry.ResourceID = guildID, item.ID
			entry.MetadataJSON = marshalJSONObject(map[string]any{"case_id": item.ID, "case_number": item.CaseNumber, "target_discord_user_id": item.TargetDiscordUserID})
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}
		updated = &item
		return nil
	})
	return updated, err
}
