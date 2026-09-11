package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// CreateAuditLogEntry appends one audit row in its own transaction. Entries
// whose Action is not a recognized audit event are dropped silently, as is a
// nil entry, so callers can log unconditionally. Code that already holds a
// transaction must use createAuditLogEntry instead.
func (s *Store) CreateAuditLogEntry(ctx context.Context, entry *model.AuditLogEntry) error {
	if entry == nil || !model.IsAuditEvent(entry.Action) {
		return nil
	}

	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return createAuditLogEntry(tx, entry, time.Now().UTC())
	})
}

// ListAuditLogEntries returns a guild's important audit rows oldest-first with
// no pagination; ListAuditLogEntriesFiltered is the paged variant.
func (s *Store) ListAuditLogEntries(ctx context.Context, guildID string) ([]model.AuditLogEntry, error) {

	var entries []model.AuditLogEntry
	if err := s.db.WithContext(ctx).
		Where("guild_id = ?", guildID).
		Where("action IN ?", model.ImportantAuditActions()).
		Order("created_at ASC").
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list audit log entries: %w", err)
	}

	return entries, nil
}

// ListAuditLogEntriesFiltered pages a guild's important audit rows newest-first.
// Limit is clamped to 1..100. BeforeID is a keyset cursor (created_at, id) that
// composes with Offset; Total counts the filtered set ignoring the cursor.
func (s *Store) ListAuditLogEntriesFiltered(ctx context.Context, params model.ListAuditLogEntriesParams) (*model.ListAuditLogEntriesResult, error) {

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := filteredAuditQuery(s.db.WithContext(ctx).Model(&model.AuditLogEntry{}), params).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count audit log entries: %w", err)
	}

	query := filteredAuditQuery(s.db.WithContext(ctx).Model(&model.AuditLogEntry{}), params)
	if params.BeforeID != "" {
		var cursor model.AuditLogEntry
		result := s.db.WithContext(ctx).Where("guild_id = ? AND id = ?", params.GuildID, params.BeforeID).First(&cursor)
		if result.Error != nil {
			return nil, fmt.Errorf("resolve audit cursor: %w", result.Error)
		}
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var entries []model.AuditLogEntry
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list filtered audit log entries: %w", err)
	}

	return &model.ListAuditLogEntriesResult{Entries: entries, Total: total}, nil
}

// filteredAuditQuery applies the guild scope, the important-action allowlist,
// and every non-empty filter in params. CaseID and MemberDiscordUserID match
// either the resource columns or a JSON substring of metadata_json, which is
// the only portable way to search that column on MySQL and SQLite.
func filteredAuditQuery(query *gorm.DB, params model.ListAuditLogEntriesParams) *gorm.DB {
	query = query.Where("guild_id = ?", params.GuildID).Where("action IN ?", model.ImportantAuditActions())
	if params.ActorDiscordUserID != "" {
		query = query.Where("actor_discord_user_id = ?", params.ActorDiscordUserID)
	}
	if params.Source != "" {
		query = query.Where("source = ?", params.Source)
	}
	if params.Action != "" {
		query = query.Where("action = ?", params.Action)
	}
	if params.ResourceType != "" {
		query = query.Where("resource_type = ?", params.ResourceType)
	}
	if params.ResourceID != "" {
		query = query.Where("resource_id = ?", params.ResourceID)
	}
	if params.Result != "" {
		query = query.Where("result = ?", params.Result)
	}
	if params.CaseID != "" {
		pattern := `%"case_id":"` + params.CaseID + `"%`
		query = query.Where("(resource_type = ? AND resource_id = ?) OR metadata_json LIKE ?", "case", params.CaseID, pattern)
	}
	if params.MemberDiscordUserID != "" {
		pattern := `%"member_discord_user_id":"` + params.MemberDiscordUserID + `"%`
		targetPattern := `%"target_discord_user_id":"` + params.MemberDiscordUserID + `"%`
		query = query.Where("actor_discord_user_id = ? OR metadata_json LIKE ? OR metadata_json LIKE ?",
			params.MemberDiscordUserID, pattern, targetPattern)
	}
	if params.CreatedAfter != "" {
		if value, err := time.Parse(time.RFC3339Nano, params.CreatedAfter); err == nil {
			query = query.Where("created_at >= ?", value.UTC())
		}
	}
	if params.CreatedBefore != "" {
		if value, err := time.Parse(time.RFC3339Nano, params.CreatedBefore); err == nil {
			query = query.Where("created_at < ?", value.UTC())
		}
	}
	return query
}

// createAuditLogEntry atomically adds immutable staff history and its mirror queue
// row inside the caller's source transaction. Standalone callers must use
// CreateAuditLogEntry; a queue failure must roll back the underlying decision.
func createAuditLogEntry(db *gorm.DB, entry *model.AuditLogEntry, now time.Time) error {
	if entry == nil || !model.IsAuditEvent(entry.Action) {
		return nil
	}
	if entry.ResourceID == "" {
		entry.ResourceID = "unknown"
	}
	if entry.MetadataJSON == "" {
		entry.MetadataJSON = "{}"
	}
	entry.MetadataJSON = model.RedactAuditMetadata(entry.MetadataJSON)
	entry.FailureReason = redactAuditFailureReason(entry.FailureReason)
	if err := prepareULIDModel(&entry.ULIDModel, now); err != nil {
		return fmt.Errorf("prepare audit log entry model: %w", err)
	}
	if err := db.Create(entry).Error; err != nil {
		return fmt.Errorf("create audit log entry: %w", err)
	}

	if err := db.Create(&auditMirrorDelivery{AuditEntryID: entry.ID, RetryAt: entry.CreatedAt.UTC()}).Error; err != nil {
		return fmt.Errorf("enqueue audit mirror entry: %w", err)
	}
	return nil
}

// redactAuditFailureReason keeps bounded classifications while stripping credentials accidentally embedded in an error.
func redactAuditFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, fragment := range []string{"token=", "authorization:", "bearer ", "password=", "secret=", "cookie="} {
		if strings.Contains(lower, fragment) {
			return "sensitive failure detail redacted"
		}
	}
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240])
	}
	return value
}
