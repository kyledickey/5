package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RecoveryTableManifest captures a deterministic row count and content digest for one preserved table.
type RecoveryTableManifest struct {
	Count  int64  `json:"count"`
	SHA256 string `json:"sha256"`
}

// RecoveryManifest is the portable evidence compared before backup and after restore.
type RecoveryManifest struct {
	Version            string                           `json:"version"`
	CapturedAt         time.Time                        `json:"captured_at"`
	Tables             map[string]RecoveryTableManifest `json:"tables"`
	GuildCaseHighWater map[string]uint64                `json:"guild_case_high_water"`
}

type recoveryTableDefinition struct {
	name    string
	columns []string
	order   string
}

var recoveryTables = []recoveryTableDefinition{
	{"quack_schema_migrations", []string{"version", "name", "checksum", "state"}, "version"},
	{"guilds", []string{"id", "discord_guild_id", "is_active"}, "id"},
	{"guild_settings", []string{"id", "guild_id", "tickets_enabled", "general_logging_enabled", "honeypot_enabled"}, "id"},
	{"staff_members", []string{"id", "guild_id", "discord_user_id", "last_seen_permission_bits"}, "id"},
	{"case_templates", []string{"id", "guild_id", "slug", "version", "archived_at"}, "id"},
	{"case_template_context_fields", []string{"id", "template_id", "`key`", "position", "field_type"}, "id"},
	{"case_template_levels", []string{"id", "template_id", "position", "is_default", "trigger_case_count"}, "id"},
	{"case_template_level_actions", []string{"id", "level_id", "position", "action_type", "config_json"}, "id"},
	{"cases", []string{"id", "guild_id", "case_number", "source", "status"}, "id"},
	{"case_events", []string{"id", "case_id", "guild_id", "event_type", "visibility", "body", "metadata_json"}, "id"},
	{"case_action_executions", []string{"id", "case_id", "idempotency_key", "status"}, "id"},
	{"case_action_attempts", []string{"id", "execution_id", "attempt_number", "status"}, "id"},
	{"case_evidence_snapshots", []string{"id", "case_id", "message_discord_id"}, "id"},
	{"case_evidence_attachments", []string{"id", "evidence_id", "filename", "size_bytes", "copy_outcome"}, "id"},
	{"case_notifications", []string{"id", "case_id", "status", "delivery_message_discord_id", "attempt_count"}, "id"},
	{"appeals", []string{"id", "guild_id", "case_id", "status"}, "id"},
	{"appeal_events", []string{"id", "appeal_id", "guild_id", "event_type", "body", "metadata_json"}, "id"},
	{"guild_appeal_settings", []string{"id", "guild_id", "questions_json"}, "id"},
	{"appeal_notifications", []string{"id", "appeal_id", "event_id", "audience", "status"}, "id"},
	{"audit_log_entries", []string{"id", "guild_id", "action", "resource_id", "result"}, "id"},
	{"module_configurations", []string{"id", "guild_id", "module_id", "enabled", "config_json"}, "id"},
	{"module_import_records", []string{"id", "guild_id", "module_id", "source_id", "target_id"}, "id"},
	{"tickets", []string{"id", "guild_id", "owner_discord_user_id", "status"}, "id"},
	{"ticket_events", []string{"id", "ticket_id", "guild_id", "event_type"}, "id"},
	{"ticket_transcripts", []string{"ticket_id", "guild_id", "content", "captured_at", "expires_at"}, "ticket_id"},
	{"ticket_member_states", []string{"id", "guild_id", "owner_discord_user_id", "open_ticket_id"}, "id"},
	{"honeypot_triggers", []string{"id", "guild_id", "message_discord_id", "case_id", "outcome"}, "id"},
	{"v4_import_batches", []string{"id", "guild_id", "source_name", "checksum", "record_count"}, "id"},
	{"v4_import_sources", []string{"id", "guild_id", "source_name", "source_id", "target_case_id", "fingerprint"}, "id"},
}

// BuildRecoveryManifest creates a deterministic, content-minimizing backup manifest from an isolated target.
func (s *Store) BuildRecoveryManifest(ctx context.Context) (*RecoveryManifest, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	manifest := &RecoveryManifest{Version: "quack-v5-recovery/v1", CapturedAt: time.Now().UTC(), Tables: map[string]RecoveryTableManifest{}, GuildCaseHighWater: map[string]uint64{}}
	definitions := append([]recoveryTableDefinition{}, recoveryTables...)
	if s.db.Migrator().HasTable(&currentSchema{}) {
		definitions = append(definitions,
			recoveryTableDefinition{"case_publications", []string{"message_id", "case_id", "channel_id", "presentation_json", "last_digest", "retry_at"}, "message_id"},
			recoveryTableDefinition{"honeypot_message_cleanups", []string{"id", "guild_id", "message_discord_id", "channel_discord_id", "target_discord_user_id", "trigger_id", "attempt_count", "next_attempt_at", "completed_at"}, "id"},
			recoveryTableDefinition{"ticket_message_journal", []string{"guild_id", "thread_discord_channel_id", "message_discord_id", "ticket_id", "author_discord_user_id", "author_name", "body", "sent_at", "expires_at"}, "guild_id, thread_discord_channel_id, message_discord_id"},
		)
	}
	for _, definition := range definitions {
		if definition.name == "quack_schema_migrations" && s.db.Migrator().HasTable(&currentSchema{}) {
			definition = recoveryTableDefinition{"quack_current_schema", []string{"id"}, "id"}
		}
		if !s.db.Migrator().HasTable(definition.name) {
			return nil, fmt.Errorf("required recovery table %s is missing", definition.name)
		}
		rows, err := s.db.WithContext(ctx).Table(definition.name).Select(strings.Join(definition.columns, ", ")).Order(definition.order).Rows()
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		var count int64
		for rows.Next() {
			values := make([]any, len(definition.columns))
			pointers := make([]any, len(values))
			for index := range values {
				pointers[index] = &values[index]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			for _, value := range values {
				_, _ = fmt.Fprintf(hash, "%T:%v\x00", value, value)
			}
			_, _ = hash.Write([]byte("\n"))
			count++
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		manifest.Tables[definition.name] = RecoveryTableManifest{Count: count, SHA256: hex.EncodeToString(hash.Sum(nil))}
	}
	type highWater struct {
		GuildID string
		Maximum uint64
	}
	var values []highWater
	if err := s.db.WithContext(ctx).Table("cases").Select("guild_id, MAX(case_number) AS maximum").Group("guild_id").Scan(&values).Error; err != nil {
		return nil, err
	}
	for _, value := range values {
		manifest.GuildCaseHighWater[value.GuildID] = value.Maximum
	}
	return manifest, nil
}

// VerifyRecoveryManifest compares a restored target and rejects duplicate-execution or case-number invariants.
func (s *Store) VerifyRecoveryManifest(ctx context.Context, expected RecoveryManifest) error {
	actual, err := s.BuildRecoveryManifest(ctx)
	if err != nil {
		return err
	}
	if expected.Version != actual.Version {
		return fmt.Errorf("recovery manifest version %q is unsupported", expected.Version)
	}
	names := make([]string, 0, len(expected.Tables))
	for name := range expected.Tables {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if actual.Tables[name] != expected.Tables[name] {
			return fmt.Errorf("restored table %s differs: expected %+v, got %+v", name, expected.Tables[name], actual.Tables[name])
		}
	}
	for guildID, maximum := range expected.GuildCaseHighWater {
		if actual.GuildCaseHighWater[guildID] != maximum {
			return fmt.Errorf("guild %s case-number high water differs", guildID)
		}
	}
	checks := []struct{ query, label string }{
		{`SELECT COUNT(*) FROM (SELECT guild_id, case_number FROM cases GROUP BY guild_id, case_number HAVING COUNT(*) > 1) duplicates`, "duplicate guild case numbers"},
		{`SELECT COUNT(*) FROM (SELECT idempotency_key FROM case_action_executions GROUP BY idempotency_key HAVING COUNT(*) > 1) duplicates`, "duplicate action idempotency keys"},
		{`SELECT COUNT(*) FROM (SELECT guild_id, source_name, source_id FROM v4_import_sources GROUP BY guild_id, source_name, source_id HAVING COUNT(*) > 1) duplicates`, "duplicate v4 source identities"},
	}
	for _, check := range checks {
		var count int64
		if err := s.db.WithContext(ctx).Raw(check.query).Scan(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("restore safety failed: %d %s", count, check.label)
		}
	}
	return nil
}

// WriteRedisRecoveryProbe writes one namespaced, expiring value for a controlled persistence rehearsal.
func WriteRedisRecoveryProbe(ctx context.Context, client *redis.Client, namespace, token string) error {
	if client == nil || strings.TrimSpace(namespace) == "" || strings.TrimSpace(token) == "" {
		return errors.New("Redis client, namespace, and token are required")
	}
	key := "quack:v5:recovery:test:" + strings.TrimSpace(namespace)
	created, err := client.SetNX(ctx, key, token, 30*time.Minute).Result()
	if err != nil {
		return err
	}
	if !created {
		return errors.New("Redis recovery probe already exists")
	}
	return nil
}

// VerifyAndDeleteRedisRecoveryProbe proves the probe survived the operator-controlled recovery step and cleans it up.
func VerifyAndDeleteRedisRecoveryProbe(ctx context.Context, client *redis.Client, namespace, token string) error {
	if client == nil {
		return errors.New("Redis client is required")
	}
	key := "quack:v5:recovery:test:" + strings.TrimSpace(namespace)
	value, err := client.Get(ctx, key).Result()
	if err != nil {
		return err
	}
	if value != token {
		return errors.New("Redis recovery probe token differs")
	}
	return client.Del(ctx, key).Err()
}
