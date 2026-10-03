package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestAuditMirrorQueue exercises source atomicity and bounded delivery selection
// on SQLite and MySQL rather than relying only on generated SQL inspection.
func TestAuditMirrorQueue(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteTestDB, "mysql": openMySQLTestDB} {
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
				return createAuditLogEntry(tx, &model.AuditLogEntry{ULIDModel: model.ULIDModel{ID: "failed-source"}, Action: "case.create", GuildID: "guild"}, now)
			})
			if !errors.Is(err, reject) {
				t.Fatal("source queue failure lost", err)
			}
			var count int64
			if err := db.Model(&model.AuditLogEntry{}).Where("id IN ?", []string{"failed", "failed-source"}).Count(&count).Error; err != nil || count != 0 {
				t.Fatal("orphan audit survived rollback", count, err)
			}
		})
	}
}
