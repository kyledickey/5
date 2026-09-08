package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestRecentCaseEvents verifies bounded deterministic selection in SQLite.
func TestRecentCaseEvents(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	exerciseRecentCaseEvents(t, db)
}

// TestMySQLRecentCaseEvents verifies the same query against actual MySQL.
func TestMySQLRecentCaseEvents(t *testing.T) { exerciseRecentCaseEvents(t, openMySQLMigrationDB(t)) }

// exerciseRecentCaseEvents checks filtering before the limit, timestamp ties,
// chronological output, case isolation, and rejection of unbounded reads.
func exerciseRecentCaseEvents(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&model.CaseEvent{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 20; i++ {
		event := model.CaseEvent{ULIDModel: model.ULIDModel{ID: fmt.Sprintf("event-%02d", i), CreatedAt: now}, CaseID: "case", EventType: model.CaseEventCreated}
		if i >= 15 {
			event.EventType = model.CaseEventType(retiredCaseEventTypes[0])
		}
		if err := db.Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	other := model.CaseEvent{ULIDModel: model.ULIDModel{ID: "zz-other", CreatedAt: now}, CaseID: "other", EventType: model.CaseEventCreated}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	queries := 0
	if err := db.Callback().Query().Before("gorm:query").Register("assert_recent_bound", func(tx *gorm.DB) {
		queries++
		limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
		if !ok || limit.Limit == nil || *limit.Limit != 6 {
			t.Error("timeline query lacks limit six")
		}
	}); err != nil {
		t.Fatal(err)
	}
	s := New(db, nil)
	for repeat := 0; repeat < 2; repeat++ {
		events, err := s.ListRecentCaseEvents(context.Background(), "case", 6)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 6 {
			t.Fatalf("got %d events", len(events))
		}
		for i, event := range events {
			if want := fmt.Sprintf("event-%02d", 9+i); event.ID != want {
				t.Fatalf("event %d=%s want %s", i, event.ID, want)
			}
		}
	}
	for _, limit := range []int{-1, 0, 101} {
		if _, err := s.ListRecentCaseEvents(context.Background(), "case", limit); err == nil {
			t.Fatal("invalid bound accepted")
		}
	}
	if queries != 2 {
		t.Fatalf("unexpected queries: %d", queries)
	}
}
