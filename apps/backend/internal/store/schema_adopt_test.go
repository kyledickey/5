package store

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"

	"gorm.io/gorm"
)

// TestAdoptCurrentSchemaPreservesHistory exercises the supported transition on
// both database engines and compares stored history before and after startup.
func TestAdoptCurrentSchemaPreservesHistory(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			if err := runMigrations(db, registeredMigrations()[:1]); err != nil {
				t.Fatal(err)
			}
			insertRepresentativeHistory(t, db)
			if err := runMigrations(db, registeredMigrations()); err != nil {
				t.Fatal(err)
			}
			repository := New(db, nil)
			before, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.AdoptCurrentSchema(); err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); err != nil {
				t.Fatalf("startup after adoption failed: %v", err)
			}
			after, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			delete(before.Tables, "quack_schema_migrations")
			delete(after.Tables, "quack_current_schema")
			// Current startup creates delivery and message-cleanup tables; they
			// contain no historical work and must begin empty on adoption.
			for _, table := range []string{"case_publications", "honeypot_message_cleanups", "ticket_message_journal"} {
				var count int64
				if err := db.Table(table).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("expected empty %s after adoption: count=%d err=%v", table, count, err)
				}
				delete(after.Tables, table)
			}
			if !reflect.DeepEqual(before.Tables, after.Tables) || !reflect.DeepEqual(before.GuildCaseHighWater, after.GuildCaseHighWater) {
				t.Fatal("adoption changed preserved history or case numbering")
			}
			if !db.Migrator().HasTable(&schemaMigration{}) {
				t.Fatal("adoption deleted the old ledger")
			}
		})
	}
}

// TestAdoptionRejectsIncompleteHistory ensures the new marker cannot hide a
// partial or edited migration sequence from startup validation.
func TestAdoptionRejectsIncompleteHistory(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:1]); err != nil {
		t.Fatal(err)
	}
	if err := New(db, nil).AdoptCurrentSchema(); err == nil || db.Migrator().HasTable(&currentSchema{}) {
		t.Fatal("partial history was adopted")
	}
}

// TestCurrentSchemaRecoveryPreservesHoneypotCleanup verifies direct initialization
// includes durable cleanup and recovery detects a changed pending retry receipt.
func TestCurrentSchemaRecoveryPreservesHoneypotCleanup(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasTable(&honeypot.MessageCleanup{}) {
				t.Fatal("direct schema omitted honeypot cleanup")
			}
			now := time.Now().UTC()
			pending := honeypot.MessageCleanup{ID: "pending-cleanup", GuildID: "guild", MessageDiscordID: "message", ChannelDiscordID: "trap", TargetDiscordUserID: "member", TriggerID: "incident", AttemptCount: 2, NextAttemptAt: now.Add(time.Minute), CreatedAt: now}
			if err := db.Create(&pending).Error; err != nil {
				t.Fatal(err)
			}
			manifest, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			receipt, present := manifest.Tables["honeypot_message_cleanups"]
			if !present || receipt.Count != 1 {
				t.Fatal("pending cleanup omitted from recovery manifest", receipt)
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&pending).Update("attempt_count", 3).Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err == nil {
				t.Fatal("changed cleanup retry receipt passed recovery verification")
			}
		})
	}
}

// TestCurrentSchemaAddsTicketJournalOnStartup verifies existing version-one
// databases receive the new journal and recovery manifests preserve original text.
func TestCurrentSchemaAddsTicketJournalOnStartup(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			if !db.Migrator().HasTable(&tickets.MessageJournal{}) {
				t.Fatal("direct initialization omitted journal")
			}
			if err := db.Migrator().DropTable(&tickets.MessageJournal{}); err != nil {
				t.Fatal(err)
			}
			if err := repository.Migrate(); err != nil {
				t.Fatal("marked database failed journal reconciliation", err)
			}
			original := tickets.MessageJournal{GuildID: "guild", ThreadDiscordChannelID: "thread", MessageDiscordID: "message", TicketID: "ticket", AuthorDiscordUserID: "member", AuthorName: "Member", Body: "retained original", SentAt: time.Now().UTC()}
			if err := db.Create(&original).Error; err != nil {
				t.Fatal(err)
			}
			manifest, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if record, ok := manifest.Tables["ticket_message_journal"]; !ok || record.Count != 1 {
				t.Fatal("journal missing from recovery", record)
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&original).Update("body", "changed").Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.VerifyRecoveryManifest(context.Background(), *manifest); err == nil {
				t.Fatal("changed original transcript text passed recovery")
			}
		})
	}
}
