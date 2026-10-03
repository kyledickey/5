package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

const (
	schemaLockName    = "quack_v5_schema_migrations"
	mysqlTableOptions = "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
)

// schemaTypes lists every table owned by the moderation core. Optional modules
// contribute their own tables through SchemaTypes on their packages.
func schemaTypes() []any {
	return []any{
		&model.Guild{},
		&model.GuildSettings{},
		&model.StaffMember{},
		&model.CaseTemplate{},
		&model.CaseTemplateContextField{},
		&model.CaseTemplateLevel{},
		&model.CaseTemplateLevelAction{},
		&model.Case{},
		&model.CaseActionExecution{},
		&model.CaseActionAttempt{},
		&model.CaseEvidenceSnapshot{},
		&model.CaseEvidenceAttachment{},
		&model.CaseNotification{},
		&model.CaseEvent{},
		&model.CasePublication{},
		&model.Appeal{},
		&model.AppealEvent{},
		&model.GuildAppealSettings{},
		&model.AppealNotification{},
		&model.AuditLogEntry{},
		&auditMirrorDelivery{},
		&V4ImportBatch{},
		&V4ImportSource{},
	}
}

// Migrate reconciles the schema on normal bot startup.
func (s *Store) Migrate() error { return s.InitializeSchema() }

// InitializeSchema creates the schema on an empty database and reconciles it on
// one that already has it. It is idempotent, so an interrupted run can be retried.
func (s *Store) InitializeSchema() error {
	return withSchemaLock(s.db, func() error {
		db := withMySQLTableOptions(s.db)
		types := schemaTypes()
		types = append(types, modules.SchemaTypes()...)
		types = append(types, tickets.SchemaTypes()...)
		types = append(types, honeypot.SchemaTypes()...)
		if err := db.AutoMigrate(types...); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
		return applyStorageConstraints(db)
	})
}

// applyStorageConstraints installs invariants that portable model tags cannot
// express: at most one default level per template, at most one enforcement
// action per level, and the composite read indexes the hot queries rely on.
func applyStorageConstraints(db *gorm.DB) error {
	if db.Dialector.Name() == "mysql" && !db.Migrator().HasColumn("case_template_levels", "default_template_id") {
		if err := db.Exec(`ALTER TABLE case_template_levels ADD COLUMN default_template_id CHAR(26) GENERATED ALWAYS AS (CASE WHEN is_default = 1 THEN template_id ELSE NULL END) STORED`).Error; err != nil {
			return fmt.Errorf("add default-level constraint column: %w", err)
		}
	}
	// MySQL has no partial indexes, so the generated column carries the predicate.
	defaultColumns, defaultPredicate := "template_id", " WHERE is_default = 1"
	if db.Dialector.Name() == "mysql" {
		defaultColumns, defaultPredicate = "default_template_id", ""
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
			return fmt.Errorf("create schema index %s: %w", index.name, err)
		}
	}
	return nil
}

// withSchemaLock serializes schema creation in MySQL and relies on SQLite's database lock in local tests.
func withSchemaLock(db *gorm.DB, fn func() error) error {
	if db.Dialector.Name() != "mysql" {
		return fn()
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle for schema lock: %w", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("reserve database connection for schema lock: %w", err)
	}
	defer conn.Close()

	var acquired int
	if err := conn.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 30)", schemaLockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire schema lock: %w", err)
	}
	if acquired != 1 {
		return errors.New("acquire schema lock: timed out")
	}
	defer func() {
		var released any
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", schemaLockName).Scan(&released)
	}()

	return fn()
}

// withMySQLTableOptions applies the common InnoDB and utf8mb4 options to newly created MySQL tables.
func withMySQLTableOptions(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() != "mysql" {
		return db
	}

	return db.Set("gorm:table_options", mysqlTableOptions)
}
