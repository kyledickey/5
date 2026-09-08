package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCasePublicationSurvivesRestart verifies transport coordinates and refresh
// progress survive a real database close/reopen without rewriting the snapshot.
func TestCasePublicationSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CasePublication{}); err != nil {
		t.Fatal(err)
	}
	repository := New(db, nil)
	now := time.Now().UTC()
	receipt := model.CasePublication{CaseID: "case", MessageID: "message", ChannelID: "channel", PresentationJSON: "original public snapshot", RetryAt: now}
	if err := repository.SaveCasePublication(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	receipt.PresentationJSON = "should not replace snapshot"
	if err := repository.SaveCasePublication(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteCasePublicationRefresh(context.Background(), "message", 0, "digest", now.Add(time.Minute), true); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ = reopened.DB()
	defer sqlDB.Close()
	repository = New(reopened, nil)
	rows, err := repository.ListDueCasePublications(context.Background(), now, 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("premature retry: %v %+v", err, rows)
	}
	rows, err = repository.ListDueCasePublications(context.Background(), now.Add(2*time.Minute), 50)
	if err != nil || len(rows) != 1 || rows[0].PresentationJSON != "original public snapshot" || rows[0].LastDigest != "digest" {
		t.Fatalf("restart lost receipt: %v %+v", err, rows)
	}
	if err := repository.DeleteCasePublication(context.Background(), "message"); err != nil {
		t.Fatal(err)
	}
	rows, err = repository.ListDueCasePublications(context.Background(), now.Add(2*time.Minute), 50)
	if err != nil || len(rows) != 0 {
		t.Fatalf("retirement failed: %v %+v", err, rows)
	}
}

// TestCasePublicationEvidenceHealthTracksRecovery verifies the public health
// query follows capture changes while keeping raw evidence inside storage.
func TestCasePublicationEvidenceHealthTracksRecovery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&CaseEvidenceSnapshotRecord{}); err != nil {
		t.Fatal(err)
	}
	repository := New(db, nil)
	snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: "evidence"}, CaseID: "case", CaptureOutcome: "unavailable", CaptureWarning: "missing", Content: "private content"}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	incomplete, err := repository.CasePublicationEvidenceIncomplete(context.Background(), "case")
	if err != nil || !incomplete {
		t.Fatalf("missing warning: %v %v", incomplete, err)
	}
	if err := db.Model(&model.CaseEvidenceSnapshot{}).Where("id = ?", "evidence").Updates(map[string]any{"capture_outcome": "captured", "capture_warning": ""}).Error; err != nil {
		t.Fatal(err)
	}
	incomplete, err = repository.CasePublicationEvidenceIncomplete(context.Background(), "case")
	if err != nil || incomplete {
		t.Fatalf("stale warning: %v %v", incomplete, err)
	}
}
