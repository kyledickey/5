package store

import (
	"context"
	"fmt"
)

// SchemaReady reports whether the moderation schema exists. Startup creates it
// before serving traffic, so a missing table means the process is pointed at an
// uninitialized or wrong database.
func (s *Store) SchemaReady(ctx context.Context) error {
	migrator := s.db.WithContext(ctx).Migrator()
	for _, table := range []string{"guilds", "guild_settings", "cases", "case_action_executions", "audit_log_entries"} {
		if !migrator.HasTable(table) {
			return fmt.Errorf("schema table %s is missing", table)
		}
	}
	return nil
}

// OperationalMetricSnapshot returns aggregate, low-cardinality durable
// workflow counters without exposing guild, member, content, or payload data.
func (s *Store) OperationalMetricSnapshot(ctx context.Context) (map[string]int64, error) {
	tables := map[string]string{
		"quack_cases_total":             "cases",
		"quack_escalation_levels_total": "cases",
		"quack_action_attempts_total":   "case_action_attempts",
		"quack_notifications_total":     "case_notifications",
		"quack_appeals_total":           "appeals",
		"quack_audit_events_total":      "audit_log_entries",
	}
	result := make(map[string]int64, len(tables))
	for metric, table := range tables {
		if !s.db.Migrator().HasTable(table) {
			result[metric] = 0
			continue
		}
		var count int64
		if err := s.db.WithContext(ctx).Table(table).Count(&count).Error; err != nil {
			return nil, fmt.Errorf("count %s: %w", table, err)
		}
		result[metric] = count
	}
	filtered := []struct {
		metric, table, predicate string
		args                     []any
	}{
		{metric: "quack_action_failures_total", table: "case_action_executions", predicate: "status = ?", args: []any{"failed"}},
		{metric: "quack_action_retries_total", table: "case_action_attempts", predicate: "attempt_number > ?", args: []any{1}},
		{metric: "quack_audit_mirror_events_total", table: "audit_log_entries", predicate: "action LIKE ?", args: []any{"audit_mirror.%"}},
		{metric: "quack_optional_module_events_total", table: "audit_log_entries", predicate: "action LIKE ? OR action LIKE ? OR action LIKE ?", args: []any{"ticket.%", "general_logging.%", "honeypot.%"}},
	}
	for _, counter := range filtered {
		if !s.db.Migrator().HasTable(counter.table) {
			result[counter.metric] = 0
			continue
		}
		var count int64
		if err := s.db.WithContext(ctx).Table(counter.table).Where(counter.predicate, counter.args...).Count(&count).Error; err != nil {
			return nil, fmt.Errorf("count %s: %w", counter.metric, err)
		}
		result[counter.metric] = count
	}
	return result, nil
}
