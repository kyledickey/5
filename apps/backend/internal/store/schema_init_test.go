package store

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

// TestInitializeCurrentSchema covers direct creation, startup recognition, and
// retry after partial DDL without replaying historical migrations.
func TestInitializeCurrentSchema(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			if db.Migrator().HasTable(&schemaMigration{}) {
				t.Fatal("direct initialization replayed the old migration ledger")
			}
			for _, table := range []string{"guild_settings", "appeals", "appeal_notifications", "case_evidence_attachments", "ticket_transcripts", "ticket_member_states", "honeypot_triggers", "module_configurations", "v4_import_batches", "action_manual_reviews"} {
				if !db.Migrator().HasTable(table) {
					t.Fatalf("missing current table %s", table)
				}
			}
			if err := repository.Migrate(); err != nil {
				t.Fatalf("startup failed on directly initialized schema: %v", err)
			}
			if err := repository.RollbackLastMigration(); !errors.Is(err, ErrMigrationNotReversible) || db.Migrator().HasTable(&schemaMigration{}) {
				t.Fatalf("direct schema entered the legacy rollback path: %v", err)
			}
			first := CaseTemplateLevelRecord{ULIDModelRecord: ULIDModelRecord{ID: "default-level-one"}, TemplateID: "template", Name: "Default", IsDefault: true}
			if err := db.Create(&first).Error; err != nil {
				t.Fatal(err)
			}
			second := first
			second.ID = "default-level-two"
			if err := db.Create(&second).Error; err == nil {
				t.Fatal("direct schema allowed two default levels")
			}
			if err := db.Migrator().DropTable("ticket_transcripts"); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil || !db.Migrator().HasTable("ticket_transcripts") {
				t.Fatalf("partial initialization could not recover: %v", err)
			}
		})
	}
}

// TestInitializeSchemaRefusesUnmarkedData protects an existing database from
// accidental adoption when the operator intended a fresh installation.
func TestInitializeSchemaRefusesUnmarkedData(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := db.Exec("CREATE TABLE existing_data (value TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := New(db, nil).InitializeSchema(); err == nil {
		t.Fatal("existing database was silently adopted")
	}
	if !db.Migrator().HasTable("existing_data") || db.Migrator().HasTable(&currentSchema{}) {
		t.Fatal("rejected initialization changed the database")
	}
}
