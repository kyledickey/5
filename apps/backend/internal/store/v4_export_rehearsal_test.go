package store

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/v4import"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestV4SQLExportImportRehearsal exercises the actual SQL column contract through
// export and historical-only import using independent disposable databases.
func TestV4SQLExportImportRehearsal(t *testing.T) {
	source, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	rehearseV4SQLImport(t, source, target)
}

// TestMySQLV4SQLExportImportRehearsal uses the checked-in legacy MySQL schema and
// independent test databases; the helper only creates and drops randomized names.
func TestMySQLV4SQLExportImportRehearsal(t *testing.T) {
	rehearseV4SQLImport(t, openMySQLTestDB(t), openMySQLTestDB(t))
}

// rehearseV4SQLImport proves all six legacy types retain their data and stable
// identity across deterministic extraction, preview, repeat apply, and rollback.
func rehearseV4SQLImport(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	schema := "CREATE TABLE cases (id VARCHAR(32) PRIMARY KEY, user_id VARCHAR(32) NOT NULL, moderator_id VARCHAR(32) NOT NULL, guild_id VARCHAR(32) NOT NULL, reason TEXT NOT NULL, type TINYINT NOT NULL, created_at TIMESTAMP NOT NULL, context_url TEXT)"
	if source.Dialector.Name() == "mysql" {
		content, err := os.ReadFile("../../../../Legacy/SQL/cases.sql")
		if err != nil {
			t.Fatal(err)
		}
		schema = string(content)
	}
	if err := source.Exec(schema).Error; err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../v4import/testdata/legacy_cases.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Exec(string(fixture)).Error; err != nil {
		t.Fatal(err)
	}
	sqlSource, err := source.DB()
	if err != nil {
		t.Fatal(err)
	}
	var exported, repeated bytes.Buffer
	count, err := v4import.Export(ctx, sqlSource, "3001", importGuildID, &exported)
	if err != nil || count != 6 {
		t.Fatalf("SQL export: count=%d err=%v", count, err)
	}
	if _, err := v4import.Export(ctx, sqlSource, "3001", importGuildID, &repeated); err != nil || repeated.String() != exported.String() {
		t.Fatalf("unstable export: %v", err)
	}
	if strings.Contains(exported.String(), "other-guild") {
		t.Fatal("cross-guild row leaked")
	}
	repository := New(target, nil)
	if err := repository.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	seedImportGuild(t, target)
	importer := v4import.New(repository)
	preview, err := importer.Import(ctx, "legacy-snapshot", importGuildID, "operator", bytes.NewReader(exported.Bytes()), true)
	if err != nil || preview.Valid != 6 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	var total int64
	target.Model(&model.Case{}).Count(&total)
	if total != 0 {
		t.Fatal("preview wrote cases")
	}
	applied, err := importer.Import(ctx, "legacy-snapshot", importGuildID, "operator", bytes.NewReader(exported.Bytes()), false)
	if err != nil || applied.Created != 6 {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	again, err := importer.Import(ctx, "legacy-snapshot", importGuildID, "operator", bytes.NewReader(exported.Bytes()), false)
	if err != nil || again.Created != 0 || again.AlreadyImported != 6 {
		t.Fatalf("repeat: %+v %v", again, err)
	}
	var cases []model.Case
	if err := target.Find(&cases).Error; err != nil {
		t.Fatal(err)
	}
	originals := map[string]v4import.LegacyCase{}
	for _, line := range strings.Split(strings.TrimSpace(exported.String()), "\n") {
		var original v4import.LegacyCase
		if err := json.Unmarshal([]byte(line), &original); err != nil {
			t.Fatal(err)
		}
		originals[original.SourceID] = original
	}
	seen := map[string]bool{}
	for _, item := range cases {
		var metadata struct {
			V4 struct {
				SourceID   string `json:"source_id"`
				ActionType string `json:"action_type"`
			} `json:"v4"`
		}
		if err := json.Unmarshal([]byte(item.MetadataJSON), &metadata); err != nil {
			t.Fatal(err)
		}
		original, ok := originals[metadata.V4.SourceID]
		if !ok || metadata.V4.ActionType != original.ActionType || item.Reason != original.Reason || item.ContextURL != original.ContextURL || !item.CreatedAt.Equal(original.CreatedAt) || item.TargetDiscordUserID != original.TargetDiscordUserID || item.ModeratorDiscordUserID != original.ModeratorDiscordUserID || item.Source != model.CaseSourceV4Import || item.TemplateID != nil {
			t.Fatalf("historical data changed: %+v", item)
		}
		seen[metadata.V4.ActionType] = true
	}
	if len(seen) != 6 {
		t.Fatalf("lost action types: %v", seen)
	}
	for _, table := range []any{&model.CaseActionExecution{}, &model.CaseNotification{}, &model.CaseEvidenceSnapshot{}, &model.Appeal{}} {
		if err := target.Model(table).Count(&total).Error; err != nil || total != 0 {
			t.Fatalf("unexpected side effects in %T: count=%d err=%v", table, total, err)
		}
	}
	if err := importer.Rollback(ctx, importGuildID, applied.BatchID, "operator"); err != nil {
		t.Fatal(err)
	}
	if err := target.Model(&model.Case{}).Count(&total).Error; err != nil || total != 0 {
		t.Fatalf("rollback: count=%d err=%v", total, err)
	}
	if err := source.Table("cases").Count(&total).Error; err != nil || total != 7 {
		t.Fatalf("source mutated: count=%d err=%v", total, err)
	}
	var invalid bytes.Buffer
	if _, err := v4import.Export(ctx, sqlSource, "9999", importGuildID, &invalid); err == nil || invalid.Len() != 0 {
		t.Fatalf("invalid legacy type emitted partial data: %v", err)
	}
}
