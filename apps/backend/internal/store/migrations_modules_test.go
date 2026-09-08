package store

import (
	"strings"
	"testing"
	"time"
)

func TestOptionalModuleLogicalMigrationsJoinCentralLedger(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()); err != nil {
		t.Fatalf("migrate optional modules: %v", err)
	}

	for _, table := range []any{
		&migration0006Configuration{}, &migration0006ImportRecord{},
		&migration0007Transcript{}, &migration0007MemberState{},
		&migration0008HoneypotTrigger{},
		&AppealRecord{}, &AppealEventRecord{}, &GuildAppealSettingsRecord{}, &AppealNotificationRecord{},
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("central migration did not create %T", table)
		}
	}
	var ledger []schemaMigration
	if err := db.Order("version ASC").Find(&ledger).Error; err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if len(ledger) != len(registeredMigrations()) || ledger[5].Name != "optional_module_registry_0100" || ledger[6].Name != "ticket_lifecycle_0110" || ledger[7].Name != "honeypot_triggers_0300" || ledger[8].Name != "appeals_and_member_access_0200" || ledger[9].Name != "v4_historical_import_0400" || ledger[10].Name != "final_storage_constraints_0410" {
		t.Fatalf("unexpected reconciled ledger: %+v", ledger)
	}
	if strings.Contains(migration0006Source, "internal/modules") || strings.Contains(migration0007Source, "internal/modules") || strings.Contains(migration0008Source, "internal/modules") || strings.Contains(migration0200Source, "internal/modules") {
		t.Fatal("central module migrations depend on mutable feature models")
	}
}

func TestTicketLifecycleMigrationPreservesBaselineTicketRows(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:6]); err != nil {
		t.Fatalf("apply through module registry: %v", err)
	}
	now := time.Now().UTC()
	want := migration0007Ticket{
		ID: "01J50000000000000000000001", GuildID: "01J50000000000000000000002",
		OwnerDiscordUserID: "member", ThreadDiscordChannelID: "channel",
		Status: "resolved", MetadataJSON: `{"legacy":true}`, CreatedAt: now, UpdatedAt: now,
	}
	// Seed the older schema before the queue destination column is introduced.
	if err := db.Omit("LogChannelDiscordID").Create(&want).Error; err != nil {
		t.Fatalf("insert baseline ticket: %v", err)
	}
	if err := runMigrations(db, registeredMigrations()); err != nil {
		t.Fatalf("apply ticket lifecycle: %v", err)
	}
	var got migration0007Ticket
	if err := db.First(&got, "id = ?", want.ID).Error; err != nil {
		t.Fatalf("load preserved ticket: %v", err)
	}
	if got.GuildID != want.GuildID || got.Status != want.Status || got.MetadataJSON != want.MetadataJSON {
		t.Fatalf("ticket changed during lifecycle migration: got=%+v want=%+v", got, want)
	}
}

func TestCoreModerationMigrationStillRollsBackBeforeForwardOnlyModules(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	through0004 := registeredMigrations()[:4]
	if err := runMigrations(db, through0004); err != nil {
		t.Fatalf("apply through guild settings: %v", err)
	}
	want := insertRepresentativeHistory(t, db)
	through0005 := registeredMigrations()[:5]
	if err := runMigrations(db, through0005); err != nil {
		t.Fatalf("apply core moderation migration: %v", err)
	}
	if !db.Migrator().HasTable(&migration0005Notification{}) || !db.Migrator().HasColumn(&migration0005CaseColumns{}, "IdempotencyKey") {
		t.Fatal("core moderation migration did not create its schema")
	}
	if err := rollbackLastMigration(db, through0005); err != nil {
		t.Fatalf("roll back core moderation migration: %v", err)
	}
	if db.Migrator().HasTable(&migration0005Notification{}) || db.Migrator().HasColumn(&migration0005CaseColumns{}, "IdempotencyKey") {
		t.Fatal("core moderation schema remained after rollback")
	}
	assertRepresentativeHistory(t, db, want)
}
