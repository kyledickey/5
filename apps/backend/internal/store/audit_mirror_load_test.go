package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestAuditMirrorHistoricalLoad measures idle polling against retained history
// in an isolated database. It is opt-in because fixture volume is deliberately
// larger than a regression test; timings describe this host, not Discord capacity.
func TestAuditMirrorHistoricalLoad(t *testing.T) {
	if os.Getenv("QUACK_LOAD_TESTS") != "1" {
		t.Skip("set QUACK_LOAD_TESTS=1 for the local historical-load assessment")
	}
	db := openMySQLMigrationDB(t)
	repository := New(db, nil)
	if err := repository.InitializeSchema(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	const total = 85000
	for start := 0; start < total; start += 500 {
		entries := make([]AuditLogEntryRecord, 0, 500)
		receipts := make([]auditMirrorDelivery, 0, 500)
		for i := start; i < start+500; i++ {
			id := fmt.Sprintf("%026d", i+1)
			entries = append(entries, AuditLogEntryRecord{ULIDModelRecord: ULIDModelRecord{ID: id, CreatedAt: now, UpdatedAt: now}, GuildID: fmt.Sprintf("%026d", i%850+1), Action: "case.create", Source: model.AuditSourceDiscord, Result: model.AuditResultSuccess, MetadataJSON: "{}"})
			receipts = append(receipts, auditMirrorDelivery{AuditEntryID: id, Finished: true, RetryAt: now})
		}
		if err := db.Create(&entries).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&receipts).Error; err != nil {
			t.Fatal(err)
		}
	}
	var durations []time.Duration
	for range 20 {
		started := time.Now()
		rows, err := repository.ListPendingAuditMirrorEntries(context.Background(), 50)
		durations = append(durations, time.Since(started))
		if err != nil || len(rows) != 0 {
			t.Fatalf("finished history returned: %d %v", len(rows), err)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("850 guild identities, %d delivered events, 20 idle polls: median=%s p95=%s max=%s", total, durations[10], durations[18], durations[19])
}
