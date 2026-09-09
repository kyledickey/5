package store

import "gorm.io/gorm"

const migration0012Definition = `appeal-review-reasons-v1
schema: add a non-null guild setting that selects one-click appeal decisions or a required moderator reason form
compatibility: existing guilds retain one-click decisions because the new setting defaults to false
rollback: forward-only because deployed application code reads the setting`

// migration0012AppealReviewReasons adds the review-form toggle after the frozen
// pre-release migration history without changing any existing migration checksum.
func migration0012AppealReviewReasons() migration {
	return migration{
		Version: 12, Name: "appeal_review_reasons",
		Definition: migration0012Definition, Source: migration0012Source,
		Up: applyAppealReviewReasons,
	}
}

// migration0012GuildSettingsColumns freezes the additive guild-settings shape
// owned by this migration.
type migration0012GuildSettingsColumns struct {
	ID                         string `gorm:"type:char(26);primaryKey"`
	AppealReviewReasonRequired bool   `gorm:"not null;default:false"`
}

// TableName targets the guild settings table created by migration 0004.
func (migration0012GuildSettingsColumns) TableName() string { return "guild_settings" }

// applyAppealReviewReasons adds the default-off setting idempotently.
func applyAppealReviewReasons(db *gorm.DB) error {
	return withMySQLTableOptions(db).AutoMigrate(&migration0012GuildSettingsColumns{})
}
