package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestAuditMirrorQueue exercises source atomicity and bounded delivery selection
// on SQLite and MySQL rather than relying only on generated SQL inspection.
func TestAuditMirrorQueue(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Millisecond)
			for i := 0; i < 3; i++ {
				entry := model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: fmt.Sprintf("entry-%d", i), CreatedAt: now.Add(time.Duration(i-10) * time.Minute)}, Action: "case.create", GuildID: "guild", Source: model.AuditSourceDiscord, Result: model.AuditResultSuccess}
				if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
					t.Fatal(err)
				}
			}
			if err := repository.SaveAuditMirrorDelivery(ctx, "entry-0", false, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if err := repository.SaveAuditMirrorDelivery(ctx, "entry-1", true, now); err != nil {
				t.Fatal(err)
			}
			// More obsolete rows than one poll's four batches must still make progress.
			for i := 0; i < 9; i++ {
				if err := db.Create(&auditMirrorDelivery{AuditEntryID: fmt.Sprintf("orphan-%d", i), RetryAt: now.Add(-time.Hour)}).Error; err != nil {
					t.Fatal(err)
				}
			}
			retired := model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: "retired", CreatedAt: now}, Action: "case.read", GuildID: "guild", MetadataJSON: "{}"}
			if err := db.Create(&retired).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&auditMirrorDelivery{AuditEntryID: retired.ID, RetryAt: now.Add(-time.Hour)}).Error; err != nil {
				t.Fatal(err)
			}
			rows, err := repository.ListPendingAuditMirrorEntries(ctx, 2)
			if err != nil || len(rows) != 0 {
				t.Fatalf("first bounded retirement %+v %v", rows, err)
			}
			rows, err = repository.ListPendingAuditMirrorEntries(ctx, 2)
			if err != nil || len(rows) != 1 || rows[0].ID != "entry-2" {
				t.Fatalf("valid due work starved %+v %v", rows, err)
			}
			var obsolete int64
			if err := db.Model(&auditMirrorDelivery{}).Where("finished = ? AND (audit_entry_id LIKE ? OR audit_entry_id = ?)", false, "orphan-%", "retired").Count(&obsolete).Error; err != nil || obsolete != 0 {
				t.Fatal("obsolete receipts remain", obsolete, err)
			}
			if err := repository.SaveAuditMirrorDelivery(ctx, "entry-0", false, now.Add(-time.Second)); err != nil {
				t.Fatal(err)
			}
			rows, err = repository.ListPendingAuditMirrorEntries(ctx, 2)
			if err != nil || len(rows) != 2 || rows[0].ID != "entry-2" || rows[1].ID != "entry-0" {
				t.Fatalf("due ordering/backoff changed %+v %v", rows, err)
			}
			// Reject queue insert after audit insertion; standalone history and the outer
			// source transaction must both roll back without publishing orphan events.
			reject := errors.New("queue unavailable")
			if err := db.Callback().Create().Before("gorm:create").Register("reject_mirror_queue", func(tx *gorm.DB) {
				if tx.Statement.Table == "audit_mirror_deliveries" {
					tx.AddError(reject)
				}
			}); err != nil {
				t.Fatal(err)
			}
			failed := model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: "failed"}, Action: "case.create", GuildID: "guild"}
			if err := repository.CreateAuditLogEntry(ctx, &failed); !errors.Is(err, reject) {
				t.Fatal("queue failure lost", err)
			}
			err = db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&currentSchema{}).Where("id = ?", 1).Update("audit_mirror_queue_ready", false).Error; err != nil {
					return err
				}
				return createAuditLogEntry(tx, &model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: "failed-source"}, Action: "case.create", GuildID: "guild"}, now)
			})
			if !errors.Is(err, reject) {
				t.Fatal("source queue failure lost", err)
			}
			var marker currentSchema
			if err := db.First(&marker, 1).Error; err != nil || !marker.AuditMirrorQueueReady {
				t.Fatal("source write survived rollback", err)
			}
			var count int64
			if err := db.Model(&model.AuditLogEntry{}).Where("id IN ?", []string{"failed", "failed-source"}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("orphan audit survived rollback", count, err)
			}
		})
	}
}

// TestAuditMirrorBackfillPreservesHistoricalDelivery verifies one-time startup
// adoption creates only missing semantic work and retains every retry/completion.
func TestAuditMirrorBackfillPreservesHistoricalDelivery(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Millisecond)
			for _, id := range []string{"missing", "finished", "retry", "retired"} {
				action := "case.create"
				if id == "retired" {
					action = "case.read"
				}
				entry := model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: id, CreatedAt: now.Add(-time.Hour)}, GuildID: "guild", Action: action, MetadataJSON: "{}"}
				if err := db.Create(&entry).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, receipt := range []auditMirrorDelivery{{AuditEntryID: "finished", Finished: true, RetryAt: now}, {AuditEntryID: "retry", RetryAt: now.Add(time.Hour)}} {
				if err := db.Create(&receipt).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&currentSchema{}).Where("id = ?", 1).Update("audit_mirror_queue_ready", false).Error; err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			var receipts []auditMirrorDelivery
			if err := db.Order("audit_entry_id").Find(&receipts).Error; err != nil {
				t.Fatal(err)
			}
			if len(receipts) != 3 || !receipts[0].Finished || receipts[1].AuditEntryID != "missing" || !receipts[2].RetryAt.Equal(now.Add(time.Hour)) {
				t.Fatalf("backfill changed receipts %+v", receipts)
			}
			if err := db.Callback().Raw().Before("gorm:raw").Register("reject_repeated_backfill", func(tx *gorm.DB) {
				if strings.Contains(tx.Statement.SQL.String(), "SELECT audit_log_entries.id") {
					tx.AddError(errors.New("repeated historical scan"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			manifest, err := repository.BuildRecoveryManifest(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Tables["audit_mirror_deliveries"].Count != 3 {
				t.Fatal("delivery recovery data omitted")
			}
		})
	}
}

// TestAuditMirrorReadyQueueLossFailsClosed prevents startup from recreating a
// lost delivery ledger and silently forgetting pending or completed deliveries.
func TestAuditMirrorReadyQueueLossFailsClosed(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrator().DropTable(&auditMirrorDelivery{}); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err == nil || !strings.Contains(err.Error(), "delivery ledger is missing") {
				t.Fatalf("missing ready ledger was not rejected: %v", err)
			}
			if db.Migrator().HasTable(&auditMirrorDelivery{}) {
				t.Fatal("startup recreated the lost ready ledger")
			}
		})
	}
}

// TestAuditMirrorOlderMarkerUpgrade verifies databases initialized before queue
// readiness existed acquire the column and recover missing historical work.
func TestAuditMirrorOlderMarkerUpgrade(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteMigrationDB, "mysql": openMySQLMigrationDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			entry := model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: "older-event"}, GuildID: "guild", Action: "case.create", MetadataJSON: "{}"}
			if err := db.Create(&entry).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Migrator().DropColumn(&currentSchema{}, "AuditMirrorQueueReady"); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			var receipt auditMirrorDelivery
			if err := db.First(&receipt, "audit_entry_id = ?", entry.ID).Error; err != nil || receipt.Finished {
				t.Fatalf("older marker did not backfill pending event: %+v, %v", receipt, err)
			}
		})
	}
}
