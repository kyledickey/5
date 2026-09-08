package store

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AppendCaseEvidence saves a staff upload atomically with its attribution. Only
// evidence tables change; the moderation decision and action queue are untouched.
func (s *Store) AppendCaseEvidence(ctx context.Context, guildID, caseID string, evidence []model.CaseEvidenceSnapshot, attachments []model.CaseEvidenceAttachment, audit *model.AuditLogEntry) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item model.Case
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND id = ?", guildID, caseID).First(&item).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		ids := map[string]bool{}
		for i := range evidence {
			evidence[i].GuildID, evidence[i].CaseID = guildID, caseID
			if err := prepareULIDModel(&evidence[i].ULIDModel, now); err != nil {
				return err
			}
			ids[evidence[i].ID] = true
			if err := tx.Create(&evidence[i]).Error; err != nil {
				return err
			}
		}
		for i := range attachments {
			if !ids[attachments[i].EvidenceID] {
				return errors.New("attachment does not belong to this evidence batch")
			}
			if err := prepareULIDModel(&attachments[i].ULIDModel, now); err != nil {
				return err
			}
			if err := tx.Create(&attachments[i]).Error; err != nil {
				return err
			}
		}
		if audit != nil {
			entry := *audit
			entry.GuildID, entry.ResourceID = guildID, caseID
			entry.MetadataJSON = marshalJSONObject(map[string]any{"case_id": caseID, "case_number": item.CaseNumber, "target_discord_user_id": item.TargetDiscordUserID, "evidence_added": len(evidence)})
			return createAuditLogEntry(tx, &entry, now)
		}
		return nil
	})
}
