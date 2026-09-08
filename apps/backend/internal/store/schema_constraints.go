package store

import (
	"fmt"

	"gorm.io/gorm"
)

// applyCurrentStorageConstraints installs invariants not expressible by portable
// model tags. It only changes schema: historical row conversion and stale-action
// adjudication belong to explicit migration and runtime recovery respectively.
// Keep this independent of the checksum-frozen historical migration helpers so
// changes to the current models do not redefine a previously applied migration.
func applyCurrentStorageConstraints(db *gorm.DB) error {
	if db.Dialector.Name() == "mysql" && !db.Migrator().HasColumn("case_template_levels", "default_template_id") {
		if err := db.Exec(`ALTER TABLE case_template_levels ADD COLUMN default_template_id CHAR(26) GENERATED ALWAYS AS (CASE WHEN is_default = 1 THEN template_id ELSE NULL END) STORED`).Error; err != nil {
			return fmt.Errorf("add default-level constraint column: %w", err)
		}
	}
	defaultColumns := "template_id"
	defaultPredicate := " WHERE is_default = 1"
	if db.Dialector.Name() == "mysql" {
		defaultColumns = "default_template_id"
		defaultPredicate = ""
	}
	indexes := []struct {
		table, name, columns, predicate string
		unique                          bool
	}{
		{"case_template_levels", "uq_v5_template_default_level", defaultColumns, defaultPredicate, true},
		{"case_template_level_actions", "uq_v5_level_enforcement_action", "level_id", "", true},
		{"cases", "idx_v5_case_member_history", "guild_id, target_discord_user_id, created_at, id", "", false},
		{"case_evidence_snapshots", "idx_v5_case_evidence_lookup", "case_id, message_discord_id", "", false},
		{"audit_log_entries", "idx_v5_audit_cursor", "guild_id, created_at, id", "", false},
		{"case_action_executions", "idx_v5_action_claim", "status, next_retry_at, lease_expires_at", "", false},
	}
	for _, index := range indexes {
		if db.Migrator().HasIndex(index.table, index.name) {
			continue
		}
		qualifier := ""
		if index.unique {
			qualifier = "UNIQUE "
		}
		// All identifiers are internal constants, never operator or Discord input.
		statement := fmt.Sprintf("CREATE %sINDEX %s ON %s (%s)%s", qualifier, index.name, index.table, index.columns, index.predicate)
		if err := db.Exec(statement).Error; err != nil {
			return fmt.Errorf("create current schema index %s: %w", index.name, err)
		}
	}
	return nil
}
