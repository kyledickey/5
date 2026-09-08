package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// AppealRecord stores one case-linked appeal, its immutable submission, and its
// terminal review state. The unique case reference enforces one appeal per case.
type AppealRecord struct {
	ULIDModelRecord
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string            `gorm:"type:char(26);uniqueIndex"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  model.AppealStatus `gorm:"size:32;not null;default:'pending';index:idx_appeal_guild_status,priority:2"`
	Content                 string             `gorm:"type:text;not null"`
	QuestionSnapshotJSON    string             `gorm:"type:json;not null"`
	AnswersJSON             string             `gorm:"type:json;not null"`
	Version                 uint64             `gorm:"type:bigint unsigned;not null;default:1"`
	DecisionReason          string             `gorm:"type:text"`
	ReviewedByDiscordUserID string             `gorm:"size:32"`
	ReviewedAt              *time.Time         `gorm:"index"`
	ReviewMessageDiscordID  string             `gorm:"size:32"`
	MetadataJSON            string             `gorm:"type:json;not null"`
}

// TableName identifies the current appeal table.
func (AppealRecord) TableName() string { return "appeals" }

// AppealEventRecord records appeal lifecycle events with actor type kept separate
// from actor identity so member responses can omit moderator attribution.
type AppealEventRecord struct {
	ULIDModelRecord
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	ActorType          string `gorm:"size:32;not null"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// TableName identifies the current appeal event table.
func (AppealEventRecord) TableName() string { return "appeal_events" }

func appealRecord(item model.Appeal) *AppealRecord {
	return &AppealRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, GuildID: item.GuildID, CaseID: item.CaseID, TargetDiscordUserID: item.TargetDiscordUserID, Status: item.Status, Content: item.Content, QuestionSnapshotJSON: item.QuestionSnapshotJSON, AnswersJSON: item.AnswersJSON, Version: item.Version, DecisionReason: item.DecisionReason, ReviewedByDiscordUserID: item.ReviewedByDiscordUserID, ReviewedAt: item.ReviewedAt, ReviewMessageDiscordID: item.ReviewMessageDiscordID, MetadataJSON: item.MetadataJSON}
}

func appealModel(record AppealRecord) *model.Appeal {
	return &model.Appeal{ULIDModel: model.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, GuildID: record.GuildID, CaseID: record.CaseID, TargetDiscordUserID: record.TargetDiscordUserID, Status: record.Status, Content: record.Content, QuestionSnapshotJSON: record.QuestionSnapshotJSON, AnswersJSON: record.AnswersJSON, Version: record.Version, DecisionReason: record.DecisionReason, ReviewedByDiscordUserID: record.ReviewedByDiscordUserID, ReviewedAt: record.ReviewedAt, ReviewMessageDiscordID: record.ReviewMessageDiscordID, MetadataJSON: record.MetadataJSON}
}

func appealEventRecord(item model.AppealEvent) *AppealEventRecord {
	return &AppealEventRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, AppealID: item.AppealID, GuildID: item.GuildID, EventType: item.EventType, ActorDiscordUserID: item.ActorDiscordUserID, ActorType: item.ActorType, Body: item.Body, MetadataJSON: item.MetadataJSON}
}

func appealEventModel(record AppealEventRecord) model.AppealEvent {
	return model.AppealEvent{ULIDModel: model.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, AppealID: record.AppealID, GuildID: record.GuildID, EventType: record.EventType, ActorDiscordUserID: record.ActorDiscordUserID, ActorType: record.ActorType, Body: record.Body, MetadataJSON: record.MetadataJSON}
}

func guildAppealSettingsRecord(item model.GuildAppealSettings) GuildAppealSettingsRecord {
	return GuildAppealSettingsRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, GuildID: item.GuildID, QuestionsJSON: item.QuestionsJSON, UpdatedByDiscordUserID: item.UpdatedByDiscordUserID}
}

func guildAppealSettingsModel(record GuildAppealSettingsRecord) *model.GuildAppealSettings {
	return &model.GuildAppealSettings{ULIDModel: model.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, GuildID: record.GuildID, QuestionsJSON: record.QuestionsJSON, UpdatedByDiscordUserID: record.UpdatedByDiscordUserID}
}

func appealNotificationRecord(item model.AppealNotification) *AppealNotificationRecord {
	return &AppealNotificationRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, AppealID: item.AppealID, EventID: item.EventID, GuildID: item.GuildID, TargetDiscordUserID: item.TargetDiscordUserID, Audience: item.Audience, Status: item.Status, Body: item.Body, DecisionIntentJSON: item.DecisionIntentJSON, DeliveryMessageID: item.DeliveryMessageID, DeliveryChannelID: item.DeliveryChannelID, RefreshRequested: item.RefreshRequested, LastErrorCode: item.LastErrorCode, LeaseToken: item.LeaseToken, LeaseExpiresAt: item.LeaseExpiresAt}
}

func appealNotificationModel(record AppealNotificationRecord) model.AppealNotification {
	return model.AppealNotification{ULIDModel: model.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, AppealID: record.AppealID, EventID: record.EventID, GuildID: record.GuildID, TargetDiscordUserID: record.TargetDiscordUserID, Audience: record.Audience, Status: record.Status, Body: record.Body, DecisionIntentJSON: record.DecisionIntentJSON, DeliveryMessageID: record.DeliveryMessageID, DeliveryChannelID: record.DeliveryChannelID, RefreshRequested: record.RefreshRequested, LastErrorCode: record.LastErrorCode, LeaseToken: record.LeaseToken, LeaseExpiresAt: record.LeaseExpiresAt}
}

// These aliases keep the frozen migration source checksum stable until the
// pre-release migration chain is replaced. Runtime code uses the current records.
type appealV5Record = AppealRecord
type appealEventV5Record = AppealEventRecord
