package store

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestCaseEvidencePageStableOrder verifies timestamp ties, bounds and attachment
// scoping on SQLite and MySQL without fetching unrelated snapshots.
func TestCaseEvidencePageStableOrder(t *testing.T) {
	for name, open := range map[string]func(*testing.T) *gorm.DB{"sqlite": openSQLiteTestDB, "mysql": openMySQLTestDB} {
		t.Run(name, func(t *testing.T) {
			db := open(t)
			repository := New(db, nil)
			ctx := context.Background()
			if err := repository.InitializeSchema(); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			for _, id := range []string{"c", "a", "b"} {
				snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: id, CreatedAt: now}, CaseID: "case", GuildID: "guild", MessageCreatedAt: now, EmbedsJSON: "[]"}
				if err := db.Create(&snapshot).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.CaseEvidenceAttachment{ULIDModel: model.ULIDModel{ID: "file-" + id}, EvidenceID: id, Filename: id + ".png"}).Error; err != nil {
					t.Fatal(err)
				}
			}
			for _, position := range []int{0, 2, 99} {
				snapshot, files, total, err := repository.GetCaseEvidencePage(ctx, "case", position)
				want := map[int]string{0: "a", 2: "b", 99: "c"}[position]
				if err != nil || total != 3 || snapshot.ID != want || len(files) != 1 || files[0].EvidenceID != want {
					t.Fatal(snapshot, files, total, err)
				}
			}
			snapshot, files, total, err := repository.GetCaseEvidencePage(ctx, "empty", 1)
			if err != nil || snapshot != nil || len(files) != 0 || total != 0 {
				t.Fatal(snapshot, files, total, err)
			}
		})
	}
}
