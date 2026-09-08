package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestPublicationMutationRequests verifies real source transactions reactivate
// sleeping receipts, including rollback and revision-fenced completion.
func TestPublicationMutationRequests(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	exercisePublicationMutationRequests(t, db)
}

// TestMySQLPublicationMutationRequests checks the same atomic updates and fences
// against actual MySQL rather than relying on SQLite's writer serialization.
func TestMySQLPublicationMutationRequests(t *testing.T) {
	exercisePublicationMutationRequests(t, openMySQLMigrationDB(t))
}

// exercisePublicationMutationRequests walks actual mutation methods while the
// observer repeatedly sleeps the receipt and checks that the next change wakes it.
func exercisePublicationMutationRequests(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&model.Case{}, &model.CaseActionExecution{}, &model.CaseActionAttempt{}, &model.CaseEvent{}, &model.CaseNotification{}, &model.CaseEvidenceSnapshot{}, &model.CaseEvidenceAttachment{}, &model.AuditLogEntry{}, &model.CasePublication{}, &AppealRecord{}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s := New(db, nil)
	now := time.Now().UTC()
	item := model.Case{ULIDModel: model.ULIDModel{ID: "case", CreatedAt: now}, GuildID: "guild", CaseNumber: 1, Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord}
	action := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "action", CreatedAt: now}, CaseID: item.ID, Status: model.ActionExecutionPending, ActionType: model.ActionBanUser, ConfigSnapshotJSON: "{}"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCasePublication(ctx, model.CasePublication{MessageID: "message", CaseID: item.ID, ChannelID: "channel", PresentationJSON: "{}", RetryAt: now}); err != nil {
		t.Fatal(err)
	}
	read := func() model.CasePublication {
		t.Helper()
		var receipt model.CasePublication
		if err := db.First(&receipt, "message_id = ?", "message").Error; err != nil {
			t.Fatal(err)
		}
		return receipt
	}
	sleep := func() uint64 {
		t.Helper()
		r := read()
		if err := s.CompleteCasePublicationRefresh(ctx, r.MessageID, r.Revision, "prior", now, false); err != nil {
			t.Fatal(err)
		}
		return r.Revision
	}
	check := func(prior uint64) {
		t.Helper()
		r := read()
		if !r.RefreshRequested || r.Revision <= prior || r.LastDigest != "" {
			t.Fatalf("mutation lost refresh: %+v prior=%d", r, prior)
		}
	}
	prior := sleep()
	claimed, err := s.ClaimNextCaseAction(ctx, ClaimCaseActionParams{CaseID: item.ID})
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	check(prior)
	prior = sleep()
	if err := s.CompleteCaseAction(ctx, CompleteCaseActionParams{ExecutionID: action.ID, LeaseToken: claimed.Execution.LeaseToken, AttemptNumber: 1, ExecutionStatus: model.ActionExecutionFailed, AttemptStatus: model.ActionAttemptFailed}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	prior = sleep()
	if _, err := s.RetryCaseAction(ctx, model.RetryCaseActionParams{GuildID: item.GuildID, ExecutionID: action.ID}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	claimed, err = s.ClaimNextCaseAction(ctx, ClaimCaseActionParams{CaseID: item.ID})
	if err != nil || claimed == nil {
		t.Fatalf("reclaim: %+v %v", claimed, err)
	}
	prior = sleep()
	if err := s.CompleteCaseAction(ctx, CompleteCaseActionParams{ExecutionID: action.ID, LeaseToken: claimed.Execution.LeaseToken, AttemptNumber: 2, ExecutionStatus: model.ActionExecutionSucceeded, AttemptStatus: model.ActionAttemptSucceeded}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	prior = sleep()
	reversal, err := s.QueueCaseReversal(ctx, model.QueueCaseReversalParams{GuildID: item.GuildID, CaseID: item.ID, OriginalExecutionID: action.ID, ActionType: model.ActionUnbanUser})
	if err != nil || reversal == nil {
		t.Fatalf("reversal: %v", err)
	}
	check(prior)
	// An expired irreversible reversal becomes a failed review outcome.
	expired := now.Add(-time.Hour)
	if err := db.Model(&model.CaseActionExecution{}).Where("id = ?", reversal.ID).Updates(map[string]any{"status": model.ActionExecutionRunning, "lease_expires_at": expired}).Error; err != nil {
		t.Fatal(err)
	}
	prior = sleep()
	if _, err := s.ClaimNextCaseAction(ctx, ClaimCaseActionParams{CaseID: item.ID}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	skipped := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "skip"}, CaseID: item.ID, Position: 9, Status: model.ActionExecutionPending}
	if err := db.Create(&skipped).Error; err != nil {
		t.Fatal(err)
	}
	prior = sleep()
	if err := s.SkipCaseActions(ctx, SkipCaseActionsParams{CaseID: item.ID, AfterPosition: 8}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	prior = sleep()
	if _, err := s.VoidCase(ctx, model.VoidCaseParams{GuildID: item.GuildID, CaseID: item.ID, Reason: "review"}); err != nil {
		t.Fatal(err)
	}
	check(prior)
	prior = sleep()
	if err := s.AppendCaseEvidence(ctx, item.GuildID, item.ID, []model.CaseEvidenceSnapshot{{CaptureOutcome: "unavailable", CaptureWarning: "missing", MessageCreatedAt: now}}, nil, nil); err != nil {
		t.Fatal(err)
	}
	check(prior)
	// A stale completion, successful or failed, cannot clear/delay the new work.
	before := read()
	for _, requested := range []bool{false, true} {
		if err := s.CompleteCasePublicationRefresh(ctx, "message", prior, "stale", now.Add(time.Hour), requested); err != nil {
			t.Fatal(err)
		}
	}
	after := read()
	if after.Revision <= before.Revision || after.LastDigest != before.LastDigest || after.RetryAt.After(now.Add(time.Minute)) || !after.RefreshRequested {
		t.Fatalf("stale completion overwrote mutation: %+v", after)
	}
	current := read()
	if err := s.CompleteCasePublicationRefresh(ctx, "message", current.Revision, "fresh", now, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteCasePublicationRefresh(ctx, "message", prior, "stale after fresh", now.Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	repaired := read()
	if !repaired.RefreshRequested || repaired.LastDigest != "" || repaired.Revision <= current.Revision {
		t.Fatalf("late stale edit not scheduled for repair: %+v", repaired)
	}
	// A third worker read the current revision and edited before the stale edit,
	// but completes after repair was requested. It must not acknowledge that repair.
	if err := s.CompleteCasePublicationRefresh(ctx, "message", current.Revision, "previously current", now.Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	fenced := read()
	if !fenced.RefreshRequested || fenced.LastDigest != "" || fenced.Revision <= repaired.Revision {
		t.Fatalf("in-flight current completion cleared stale-edit repair: %+v", fenced)
	}
	// Only a worker that observes the newly advanced revision can put it to sleep.
	if err := s.CompleteCasePublicationRefresh(ctx, "message", fenced.Revision, "repaired", now, false); err != nil {
		t.Fatal(err)
	}
	if read().RefreshRequested {
		t.Fatal("fresh repair completion did not sleep")
	}

	prior = sleep()
	sentinel := errors.New("rollback")
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := requestCasePublicationRefresh(tx, item.ID, now); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) || read().Revision != prior || read().RefreshRequested {
		t.Fatal("rolled-back transaction woke receipt")
	}
	due, err := s.ListDueCasePublications(ctx, now.AddDate(10, 0, 0), 50)
	if err != nil || len(due) != 0 {
		t.Fatalf("dormant historical receipt polled: %+v %v", due, err)
	}
	// Concurrent completion and mutation must leave a request whichever commits
	// first. The stale worker must also be unable to restore its old digest.
	for i := 0; i < 10; i++ {
		revision := sleep()
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() {
			<-start
			results <- s.CompleteCasePublicationRefresh(ctx, "message", revision, "old", now.Add(time.Hour), false)
		}()
		go func() {
			<-start
			results <- db.Transaction(func(tx *gorm.DB) error { return requestCasePublicationRefresh(tx, item.ID, now) })
		}()
		close(start)
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		check(revision)
	}
	sleep()

	// A committed change before registration needs no separate durable marker.
	if err := requestCasePublicationRefresh(db, "future-case", now); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCasePublication(ctx, model.CasePublication{MessageID: "future", CaseID: "future-case", ChannelID: "channel", PresentationJSON: "{}", RetryAt: now}); err != nil {
		t.Fatal(err)
	}
	due, err = s.ListDueCasePublications(ctx, now.Add(time.Minute), 50)
	if err != nil || len(due) != 1 || due[0].MessageID != "future" {
		t.Fatalf("registration missed initial refresh: %+v %v", due, err)
	}
}
