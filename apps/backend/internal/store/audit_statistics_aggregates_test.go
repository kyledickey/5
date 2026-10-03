package store

import (
	"context"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestStatisticsAggregatesMatchSourceSemantics covers all buckets and filters
// using the former full-record calculation as an independent semantic oracle.
func TestStatisticsAggregatesMatchSourceSemantics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	exerciseStatisticsAggregates(t, db)
}

// TestMySQLStatisticsAggregatesMatchSourceSemantics exercises actual grouping,
// MySQL's insensitive collation, UTC-day boundaries and a DST-aware DSN location.
func TestMySQLStatisticsAggregatesMatchSourceSemantics(t *testing.T) {
	for _, location := range []string{"UTC", "America/New_York"} {
		t.Run(location, func(t *testing.T) {
			dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
			if dsn == "" {
				t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
			}
			cfg, err := mysqlconfig.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			cfg.Loc, err = time.LoadLocation(location)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("QUACK_TEST_MYSQL_DSN", cfg.FormatDSN())
			exerciseStatisticsAggregates(t, openMySQLTestDB(t))
		})
	}
}

// exerciseStatisticsAggregates uses isolated schema without unrelated guards so
// legacy null/empty labels and technical audit rows can be tested deliberately.
func exerciseStatisticsAggregates(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&model.Case{}, &model.CaseActionExecution{}, &model.Appeal{}, &model.AuditLogEntry{}); err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 4)
	insert := func(table string, values map[string]any) {
		t.Helper()
		if err := db.Table(table).Create(values).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i, row := range []struct {
		id, guild, template, status, source string
		at                                  time.Time
	}{
		{"first", "guild", "Rule", "valid", "discord", from},
		{"second", "guild", "rule", "voided", "v4_import", from.Add(24*time.Hour - time.Second)},
		{"third", "guild", "", "valid", "discord", from.Add(24 * time.Hour)},
		{"fourth", "guild", " ", "valid", "discord", from.Add(48 * time.Hour)},
		{"other", "other", "Rule", "valid", "discord", from},
		{"old", "guild", "Rule", "valid", "discord", from.Add(-time.Second)},
		{"end", "guild", "Rule", "valid", "discord", to},
	} {
		values := map[string]any{"id": row.id, "guild_id": row.guild, "case_number": i + 1, "template_id": row.template, "status": row.status, "source": row.source, "created_at": row.at, "updated_at": row.at, "reason": "unneeded case payload", "context_values_json": "[]", "metadata_json": "{}", "template_snapshot_json": "{}", "target_discord_user_id": "member", "moderator_discord_user_id": "moderator", "template_version": 1}
		if row.id == "third" {
			values["template_id"] = nil
		}
		insert("cases", values)
	}
	// Empty template IDs map to the same historical bucket as NULL, while spaces
	// and exact case-sensitive labels retain the former Go grouping behavior.
	insert("cases", map[string]any{"id": "empty", "guild_id": "guild", "case_number": 8, "template_id": "", "status": "valid", "source": "discord", "created_at": from, "updated_at": from, "reason": "unneeded case payload", "context_values_json": "[]", "metadata_json": "{}", "template_snapshot_json": "{}", "target_discord_user_id": "member", "moderator_discord_user_id": "moderator", "template_version": 1})
	for _, row := range []struct {
		id, caseID, status, action string
		at                         time.Time
	}{
		{"a", "first", "succeeded", "ban", from},
		{"b", "old", "failed", "timeout", from.Add(24 * time.Hour)},
		{"c", "other", "failed", "kick", from},
		{"d", "first", "pending", "ban", to},
	} {
		insert("case_action_executions", map[string]any{"id": row.id, "case_id": row.caseID, "status": row.status, "action_type": row.action, "created_at": row.at, "updated_at": row.at, "idempotency_key": row.id, "config_snapshot_json": "{}", "position": 0, "safe_for_retry": true})
	}
	for _, row := range []struct {
		id, guild, status string
		at                time.Time
	}{{"appeal1", "guild", "submitted", from}, {"appeal2", "guild", "accepted", from.Add(48 * time.Hour)}, {"appeal3", "other", "rejected", from}, {"appeal4", "guild", "rejected", to}} {
		insert("appeals", map[string]any{"id": row.id, "guild_id": row.guild, "status": row.status, "created_at": row.at, "updated_at": row.at, "content": "", "question_snapshot_json": "[]", "answers_json": "[]", "metadata_json": "{}", "target_discord_user_id": "member", "version": 1})
	}
	for _, row := range []struct {
		id, guild, action, result, source string
		at                                time.Time
	}{{"audit1", "guild", "case.create", "success", "discord", from}, {"audit2", "guild", "settings.update", "failure", "api", from.Add(24 * time.Hour)}, {"audit3", "guild", "audit.read", "success", "api", from}, {"audit4", "other", "case.create", "success", "discord", from}, {"audit5", "guild", "case.create", "success", "discord", to}} {
		insert("audit_log_entries", map[string]any{"id": row.id, "guild_id": row.guild, "action": row.action, "result": row.result, "source": row.source, "created_at": row.at, "updated_at": row.at, "metadata_json": "{}", "resource_type": "case", "resource_id": row.id})
	}
	var statements []string
	if err := db.Callback().Row().After("gorm:row").Register("statistics_projection_check", func(tx *gorm.DB) { statements = append(statements, tx.Statement.SQL.String()) }); err != nil {
		t.Fatal(err)
	}
	repository := New(db, nil)
	for _, guild := range []string{"guild", "empty-guild"} {
		// Non-UTC request bounds denote the same instants; returned bounds stay UTC.
		params := model.StaffStatisticsParams{GuildID: guild, From: from.In(time.FixedZone("request", -7*3600)), To: to.In(time.FixedZone("request", -7*3600))}
		statements = nil
		got, err := repository.DeriveStaffStatistics(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		if len(statements) != 4 {
			t.Fatalf("wanted one grouped query per source, got %d: %v", len(statements), statements)
		}
		for _, statement := range statements {
			if !strings.Contains(statement, "COUNT(*) AS count") || !strings.Contains(statement, "GROUP BY") || strings.Contains(statement, "SELECT *") || strings.Contains(statement, "metadata_json") || strings.Contains(statement, "config_snapshot_json") {
				t.Fatalf("source payload materialized: %s", statement)
			}
		}
		want := referenceStatistics(t, db, params)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("aggregate differs from source semantics\ngot: %+v\nwant: %+v", got, want)
		}
	}
}

// referenceStatistics intentionally reads full records only in tests, preserving
// the pre-aggregation behavior independently of the production SQL expressions.
func referenceStatistics(t *testing.T, db *gorm.DB, p model.StaffStatisticsParams) *model.StaffStatistics {
	t.Helper()
	query := func() *gorm.DB { return db.Where("created_at >= ? AND created_at < ?", p.From.UTC(), p.To.UTC()) }
	var cases []model.Case
	var actions []model.CaseActionExecution
	var appeals []model.Appeal
	var audits []model.AuditLogEntry
	for _, err := range []error{query().Where("guild_id = ?", p.GuildID).Find(&cases).Error, query().Where("case_id IN (SELECT id FROM cases WHERE guild_id = ?)", p.GuildID).Find(&actions).Error, query().Where("guild_id = ?", p.GuildID).Find(&appeals).Error, query().Where("guild_id = ? AND action IN ?", p.GuildID, model.ImportantAuditActions()).Find(&audits).Error} {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := &model.StaffStatistics{From: p.From.UTC(), To: p.To.UTC(), CaseTotal: int64(len(cases)), ActionTotal: int64(len(actions)), AppealTotal: int64(len(appeals)), AuditTotal: int64(len(audits))}
	maps := make([]map[string]int64, 13)
	for i := range maps {
		maps[i] = map[string]int64{}
	}
	for _, v := range cases {
		key := "historical_or_deleted"
		if v.TemplateID != nil && *v.TemplateID != "" {
			key = *v.TemplateID
		}
		maps[0][v.CreatedAt.UTC().Format(time.DateOnly)]++
		maps[1][key]++
		maps[2][string(v.Validity)]++
		maps[3][string(v.Source)]++
	}
	for _, v := range actions {
		maps[4][v.CreatedAt.UTC().Format(time.DateOnly)]++
		maps[5][string(v.ActionType)]++
		maps[6][string(v.Status)]++
	}
	for _, v := range appeals {
		maps[7][v.CreatedAt.UTC().Format(time.DateOnly)]++
		maps[8][string(v.Status)]++
	}
	for _, v := range audits {
		maps[9][v.CreatedAt.UTC().Format(time.DateOnly)]++
		maps[10][v.Action]++
		maps[11][string(v.Result)]++
		maps[12][string(v.Source)]++
	}
	destinations := []*[]model.StatisticBucket{&r.CasesByDay, &r.CasesByTemplate, &r.CasesByValidity, &r.CasesBySource, &r.ActionsByDay, &r.ActionsByType, &r.ActionsByResult, &r.AppealsByDay, &r.AppealsByStatus, &r.AuditsByDay, &r.AuditsByAction, &r.AuditsByResult, &r.AuditsBySource}
	for i, dst := range destinations {
		keys := make([]string, 0, len(maps[i]))
		for key := range maps[i] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		*dst = make([]model.StatisticBucket, 0, len(keys))
		for _, key := range keys {
			*dst = append(*dst, model.StatisticBucket{Key: key, Count: maps[i][key]})
		}
	}
	return r
}
