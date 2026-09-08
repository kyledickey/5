package store

import (
	"errors"
	"testing"
	"time"

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
			for _, table := range []string{"guild_settings", "appeals", "appeal_notifications", "case_evidence_attachments", "ticket_transcripts", "ticket_member_states", "honeypot_triggers", "module_configurations", "v4_import_batches", "action_manual_reviews", "case_publications"} {
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
	if err := New(db, nil).Migrate(); !errors.Is(err, ErrSchemaAdoptionRequired) {
		t.Fatal("existing database was silently adopted")
	}
	if !db.Migrator().HasTable("existing_data") || db.Migrator().HasTable(&currentSchema{}) {
		t.Fatal("rejected initialization changed the database")
	}
}

// TestStartupRequiresExplicitHistoricalAdoption ensures startup cannot silently
// replay data conversions even when it recognizes an incomplete old ledger.
func TestStartupRequiresExplicitHistoricalAdoption(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			if err := runMigrations(db, registeredMigrations()[:1]); err != nil {
				t.Fatal(err)
			}
			want := insertRepresentativeHistory(t, db)
			repository := New(db, nil)
			if err := repository.Migrate(); !errors.Is(err, ErrSchemaAdoptionRequired) {
				t.Fatalf("expected actionable adoption requirement, got %v", err)
			}
			ledger, err := loadAppliedMigrations(db)
			if err != nil || len(ledger) != 1 || db.Migrator().HasTable(&currentSchema{}) {
				t.Fatalf("startup changed legacy schema or ledger: %v, %d entries", err, len(ledger))
			}
			assertRepresentativeHistory(t, db, want)
			if err := repository.MigrateLegacySchema(); err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); !errors.Is(err, ErrSchemaAdoptionRequired) {
				t.Fatalf("completed historical replay bypassed adoption: %v", err)
			}
			if err := repository.AdoptCurrentSchema(); err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); err != nil {
				t.Fatal(err)
			}
			assertRepresentativeHistory(t, db, want)
			if err := repository.MigrateLegacySchema(); err == nil {
				t.Fatal("current database entered historical replay")
			}
		})
	}
}

// TestCurrentSchemaStartupDoesNotRepairHistoricalRows keeps DDL reconciliation
// separate from archived-template conversion and action recovery side effects.
func TestCurrentSchemaStartupDoesNotRepairHistoricalRows(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.Migrate(); err != nil {
				t.Fatal(err)
			}
			expired := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
			template := CaseTemplateRecord{ULIDModelRecord: ULIDModelRecord{ID: "preserved-template"}, GuildID: "guild", Slug: "preserved", DeletedAt: &expired}
			if err := db.Create(&template).Error; err != nil {
				t.Fatal(err)
			}
			action := CaseActionExecutionRecord{ULIDModelRecord: ULIDModelRecord{ID: "expired-action"}, CaseID: "case", Status: "running", IdempotencyKey: "expired", ConfigSnapshotJSON: "{}", LeaseExpiresAt: &expired}
			if err := db.Create(&action).Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); err != nil {
				t.Fatal(err)
			}
			var after CaseTemplateRecord
			if err := db.First(&after, "id = ?", template.ID).Error; err != nil {
				t.Fatal(err)
			}
			if after.DeletedAt == nil || !after.DeletedAt.Equal(expired) || after.ArchivedAt != nil {
				t.Fatal("startup converted historical template state")
			}
			var reviews int64
			if err := db.Model(&ActionManualReviewRecord{}).Count(&reviews).Error; err != nil {
				t.Fatal(err)
			}
			if reviews != 0 {
				t.Fatal("schema startup enqueued runtime action recovery")
			}
		})
	}
}
