package store

import (
	"context"
	"fmt"
)

// MigrationReadiness reports the schema version for readiness checks. A
// database marked by InitializeSchema reports 1 without consulting the ledger;
// otherwise the frozen migration ledger must be complete and clean, and the
// newest applied version is returned (0 for an empty ledger).
func (s *Store) MigrationReadiness(ctx context.Context) (uint64, error) {
	if s.db.WithContext(ctx).Migrator().HasTable(&currentSchema{}) {
		var marker currentSchema
		if err := s.db.WithContext(ctx).First(&marker, "id = ?", 1).Error; err != nil {
			return 0, fmt.Errorf("current schema initialization is incomplete: %w", err)
		}
		return 1, nil
	}
	applied, err := loadAppliedMigrations(s.db.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	registry := registeredMigrations()
	if err := validateAppliedMigrations(applied, registry); err != nil {
		return 0, err
	}
	if len(applied) != len(registry) {
		return 0, fmt.Errorf("migration ledger is behind: applied=%d required=%d", len(applied), len(registry))
	}
	if len(applied) == 0 {
		return 0, nil
	}
	return applied[len(applied)-1].Version, nil
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
