package store

import (
	"testing"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestInitializeSchema covers creation on an empty database, recognition on
// startup, and recovery after partially applied DDL.
func TestInitializeSchema(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteTestDB, "mysql": openMySQLTestDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{
				"guilds", "guild_settings", "staff_members", "case_templates", "case_template_context_fields",
				"case_template_levels", "case_template_level_actions", "cases", "case_events", "case_action_executions",
				"case_action_attempts", "case_evidence_snapshots", "case_evidence_attachments", "case_notifications",
				"case_publications", "appeals", "appeal_events", "guild_appeal_settings", "appeal_notifications",
				"audit_log_entries", "audit_mirror_deliveries", "v4_import_batches",
				"v4_import_sources", "module_configurations", "module_import_records", "tickets", "ticket_events",
				"ticket_transcripts", "ticket_member_states", "ticket_message_journal", "honeypot_triggers",
				"honeypot_message_cleanups", "honeypot_warning_refreshes",
			} {
				if !db.Migrator().HasTable(table) {
					t.Fatalf("missing table %s", table)
				}
			}
			if err := repository.Migrate(); err != nil {
				t.Fatalf("startup failed on an initialized schema: %v", err)
			}

			first := model.CaseTemplateLevel{ULIDModel: model.ULIDModel{ID: "default-level-one"}, TemplateID: "template", Name: "Default", IsDefault: true}
			if err := db.Create(&first).Error; err != nil {
				t.Fatal(err)
			}
			second := first
			second.ID = "default-level-two"
			if err := db.Create(&second).Error; err == nil {
				t.Fatal("schema allowed two default levels for one template")
			}

			action := model.CaseTemplateLevelAction{ULIDModel: model.ULIDModel{ID: "level-action-one"}, LevelID: first.ID, ActionType: model.ActionTimeoutUser, ConfigJSON: "{}"}
			if err := db.Create(&action).Error; err != nil {
				t.Fatal(err)
			}
			secondAction := action
			secondAction.ID = "level-action-two"
			secondAction.ActionType = model.ActionBanUser
			if err := db.Create(&secondAction).Error; err == nil {
				t.Fatal("schema allowed two enforcement actions on one level")
			}

			if err := db.Migrator().DropTable("ticket_transcripts"); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasColumn("tickets", "queue_delivery_attempt_id") {
				t.Fatal("missing ticket delivery attempt column")
			}
			// Raw ALTER avoids GORM's SQLite table-name-only DropColumn panic.
			if err := db.Exec("ALTER TABLE tickets DROP COLUMN queue_delivery_attempt_id").Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil || !db.Migrator().HasTable("ticket_transcripts") {
				t.Fatalf("partial initialization could not recover: %v", err)
			}
			if !db.Migrator().HasColumn("tickets", "queue_delivery_attempt_id") {
				t.Fatal("startup did not restore the ticket delivery attempt column")
			}
		})
	}
}

// TestInitializeSchemaLeavesUnrelatedTablesAlone keeps schema creation additive
// so an operator's own tables in the same database survive startup.
func TestInitializeSchemaLeavesUnrelatedTablesAlone(t *testing.T) {
	db := openSQLiteTestDB(t)
	if err := db.Exec("CREATE TABLE existing_data (value TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := New(db, nil).Migrate(); err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasTable("existing_data") || !db.Migrator().HasTable("cases") {
		t.Fatal("startup did not leave the existing table alongside the new schema")
	}
}

// TestSchemaReadyRequiresCoreTables keeps the readiness probe honest about an
// uninitialized or wrong database.
func TestSchemaReadyRequiresCoreTables(t *testing.T) {
	db := openSQLiteTestDB(t)
	repository := New(db, nil)
	if err := repository.SchemaReady(t.Context()); err == nil {
		t.Fatal("empty database reported a ready schema")
	}
	if err := repository.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	if err := repository.SchemaReady(t.Context()); err != nil {
		t.Fatalf("initialized database is not ready: %v", err)
	}
}
