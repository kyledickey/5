package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestReversalProvenanceQueries checks guild/member/kind isolation and protects
// later successful, active and failed/uncertain punishment attempts.
func TestReversalProvenanceQueries(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { sqlDB.Close() })
	exerciseReversalProvenanceQueries(t, db)
}

// TestMySQLReversalProvenanceQueries runs actual timestamp/status predicates on
// a disposable MySQL database, including stable latest-successful-attempt lookup.
func TestMySQLReversalProvenanceQueries(t *testing.T) {
	exerciseReversalProvenanceQueries(t, openMySQLMigrationDB(t))
}

// exerciseReversalProvenanceQueries seeds distinct case identities without
// invoking Discord or widening the fixture beyond queried source tables.
func exerciseReversalProvenanceQueries(t *testing.T, db *gorm.DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.AutoMigrate(&model.Case{}, &model.CaseActionExecution{}, &model.CaseActionAttempt{}, &model.AuditLogEntry{}); err != nil {
		t.Fatal(err)
	}
	repository := New(db, nil)
	item := model.Case{ULIDModel: model.ULIDModel{ID: "case", CreatedAt: now}, GuildID: "guild", CaseNumber: 1, TargetDiscordUserID: "member", Validity: model.CaseValidityValid}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	original := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "original", CreatedAt: now}, CaseID: item.ID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, StartedAt: &now}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	for i, status := range []model.ActionAttemptStatus{model.ActionAttemptSucceeded, model.ActionAttemptFailed, model.ActionAttemptSucceeded} {
		attempt := model.CaseActionAttempt{ULIDModel: model.ULIDModel{ID: string(rune('a' + i)), CreatedAt: now}, ExecutionID: original.ID, AttemptNumber: uint8(i + 1), Status: status, StartedAt: now, ResponsePayloadJSON: `{"timeout_until":"latest"}`}
		if i < 2 {
			attempt.ResponsePayloadJSON = `{"timeout_until":"older"}`
		}
		if err := db.Create(&attempt).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, scenario := range []string{"none", "succeeded", "running", "pending", "retrying", "failed", "older_succeeded", "older_running", "cancelled", "skipped", "other_guild", "other_member", "other_kind", "reversal"} {
		t.Run(scenario, func(t *testing.T) {
			competingCase := model.Case{ULIDModel: model.ULIDModel{ID: "other", CreatedAt: now}, GuildID: "guild", CaseNumber: 2, TargetDiscordUserID: "member"}
			later := now.Add(time.Minute)
			competing := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "competitor", CreatedAt: later}, CaseID: competingCase.ID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}
			expected := true
			switch scenario {
			case "none":
				expected = false
			case "running", "pending", "retrying", "failed", "cancelled", "skipped":
				competing.Status = model.ActionExecutionStatus(scenario)
				expected = scenario != "cancelled" && scenario != "skipped"
			case "older_succeeded":
				competing.CreatedAt = now.Add(-time.Hour)
				expected = false
			case "older_running":
				competing.CreatedAt = now.Add(-time.Hour)
				competing.Status = model.ActionExecutionRunning
			case "other_guild":
				competingCase.GuildID = "another"
				expected = false
			case "other_member":
				competingCase.TargetDiscordUserID = "another"
				expected = false
			case "other_kind":
				competing.ActionType = model.ActionBanUser
				expected = false
			case "reversal":
				competing.ReversalOfExecutionID = &original.ID
				expected = false
			}
			if err := db.Create(&competingCase).Error; err != nil {
				t.Fatal(err)
			}
			if scenario != "none" {
				if err := db.Create(&competing).Error; err != nil {
					t.Fatal(err)
				}
			}
			read, payload, conflict, err := repository.LoadCaseReversalProvenance(ctx, "guild", "case", "original")
			if err != nil || read == nil || read.ID != original.ID || payload != `{"timeout_until":"latest"}` || conflict != expected {
				t.Fatalf("read=%+v payload=%s conflict=%v err=%v", read, payload, conflict, err)
			}
			db.Delete(&model.CaseActionExecution{}, "id = ?", "competitor")
			db.Delete(&model.Case{}, "id = ?", "other")
		})
	}
	if _, _, _, err := repository.LoadCaseReversalProvenance(ctx, "another-guild", "case", "original"); err == nil {
		t.Fatal("cross-guild provenance accepted")
	}
	reversal := original
	reversal.ID = "inverse"
	reversal.ActionType = model.ActionRemoveTimeout
	reversal.ReversalOfExecutionID = &original.ID
	if err := createCaseActionAudit(db, reversal, CompleteCaseActionParams{ExecutionStatus: model.ActionExecutionSucceeded, ResponsePayloadJSON: `{"result":"timeout_already_absent","reversal_noop":true}`}, now); err != nil {
		t.Fatal(err)
	}
	var audit model.AuditLogEntry
	if err := db.First(&audit, "resource_id = ?", reversal.ID).Error; err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if json.Unmarshal([]byte(audit.MetadataJSON), &metadata) != nil || metadata["reversal_noop"] != true {
		t.Fatalf("no-op audit lost explicit outcome: %s", audit.MetadataJSON)
	}
}
