package store

import (
	"context"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
	"time"
)

// TestPublicationSchemaUpgrade verifies existing receipts become eligible once,
// and that later current-schema initialization preserves sleeping/revision state.
func TestPublicationSchemaUpgrade(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	exercisePublicationSchemaUpgrade(t, db)
}

// TestMySQLPublicationSchemaUpgrade validates real default backfill and index
// reconciliation while retaining receipt snapshots in an existing current schema.
func TestMySQLPublicationSchemaUpgrade(t *testing.T) {
	exercisePublicationSchemaUpgrade(t, openMySQLMigrationDB(t))
}

// exercisePublicationSchemaUpgrade uses the exact former receipt columns.
func exercisePublicationSchemaUpgrade(t *testing.T, db *gorm.DB) {
	t.Helper()
	s := New(db, nil)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropTable(&model.CasePublication{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE case_publications (message_id VARCHAR(32) PRIMARY KEY, case_id CHAR(26) NOT NULL, channel_id VARCHAR(32) NOT NULL, presentation_json TEXT NOT NULL, last_digest VARCHAR(64) NOT NULL, retry_at DATETIME NOT NULL)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO case_publications VALUES ('message','case','channel','original','old',?)`, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListDueCasePublications(ctx, now.Add(time.Minute), 50)
	if err != nil || len(rows) != 1 || !rows[0].RefreshRequested || rows[0].Revision != 0 || rows[0].PresentationJSON != "original" {
		t.Fatalf("existing receipt not upgraded: %+v %v", rows, err)
	}
	if err := s.CompleteCasePublicationRefresh(ctx, "message", 0, "new", now, false); err != nil {
		t.Fatal(err)
	}
	if err := s.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListDueCasePublications(ctx, now.AddDate(5, 0, 0), 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("schema repeat woke receipt: %+v %v", rows, err)
	}
	before, err := s.BuildRecoveryManifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := requestCasePublicationRefresh(db, "case", now); err != nil {
		t.Fatal(err)
	}
	after, err := s.BuildRecoveryManifest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if before.Tables["case_publications"].SHA256 == after.Tables["case_publications"].SHA256 {
		t.Fatal("recovery manifest omitted revision/request state")
	}
}
