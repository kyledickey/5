package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestActionLeaseFencingAndCrashRecovery(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, SafeForRetry: true, MaxRetries: 1, ConfigSnapshotJSON: `{"duration_seconds":60}`}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil || first == nil || first.Execution.LeaseToken == "" {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", first.Execution.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-2"})
	if err != nil || second == nil || second.Execution.LeaseToken == first.Execution.LeaseToken {
		t.Fatalf("reclaimed action: %+v err=%v", second, err)
	}
	stale := storage.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken, AttemptNumber: first.Execution.AttemptCount, WorkerID: "worker-1", AttemptStatus: model.ActionAttemptSucceeded, ExecutionStatus: model.ActionExecutionSucceeded}
	if err := repository.CompleteCaseAction(ctx, stale); err == nil {
		t.Fatal("stale worker completed a reclaimed action")
	}
	fresh := stale
	fresh.LeaseToken = second.Execution.LeaseToken
	fresh.AttemptNumber = second.Execution.AttemptCount
	fresh.WorkerID = "worker-2"
	if err := repository.CompleteCaseAction(ctx, fresh); err != nil {
		t.Fatalf("fresh completion: %v", err)
	}
}

func TestActionClaimIsSingleWinnerUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, claimErr := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
			if claimErr == nil && claimed != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("got %d claim winners, want 1", winners.Load())
	}
}

func TestActionRecoveryControlsAreIdempotentAndAuditable(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, ConfigSnapshotJSON: `{}`}, {Position: 1, ActionType: model.ActionKickUser, Status: model.ActionExecutionFailed, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed := actions[1]
	retryParams := model.RetryCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	firstRetry, err := repository.RetryCaseAction(ctx, retryParams)
	if err != nil {
		t.Fatal(err)
	}
	secondRetry, err := repository.RetryCaseAction(ctx, retryParams)
	if err != nil || secondRetry.ID != firstRetry.ID {
		t.Fatalf("retry was not idempotent: %+v err=%v", secondRetry, err)
	}
	if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", failed.ID).Update("status", model.ActionExecutionFailed).Error; err != nil {
		t.Fatal(err)
	}
	dismissParams := model.DismissCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	firstDismiss, err := repository.DismissCaseAction(ctx, dismissParams)
	if err != nil {
		t.Fatal(err)
	}
	secondDismiss, err := repository.DismissCaseAction(ctx, dismissParams)
	if err != nil || secondDismiss.ID != firstDismiss.ID {
		t.Fatalf("dismiss was not idempotent: %+v err=%v", secondDismiss, err)
	}
	reversalParams := model.QueueCaseReversalParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", OriginalExecutionID: actions[0].ID, ActionType: model.ActionRemoveTimeout}
	firstReversal, err := repository.QueueCaseReversal(ctx, reversalParams)
	if err != nil {
		t.Fatal(err)
	}
	secondReversal, err := repository.QueueCaseReversal(ctx, reversalParams)
	if err != nil || secondReversal.ID != firstReversal.ID {
		t.Fatalf("reversal was not idempotent: first=%+v second=%+v err=%v", firstReversal, secondReversal, err)
	}
}

func TestNotificationClaimRecoversBeforeSendButNeverRepeatsAmbiguousSend(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	notification := &model.CaseNotification{Status: model.NotificationPending}
	created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), Notification: notification})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimCaseNotification(ctx, model.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil || first == nil || first.Status != model.NotificationClaimed {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.DB().Model(&model.CaseNotification{}).Where("id = ?", first.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repository.ClaimCaseNotification(ctx, model.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-2"})
	if err != nil || second == nil || second.LeaseToken == first.LeaseToken {
		t.Fatalf("safe pre-send recovery failed: %+v err=%v", second, err)
	}
	if err := repository.BeginCaseNotificationDelivery(ctx, second.ID, second.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Model(&model.CaseNotification{}).Where("id = ?", second.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	third, err := repository.ClaimCaseNotification(ctx, model.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-3"})
	if err != nil || third != nil {
		t.Fatalf("ambiguous send was automatically repeated: %+v err=%v", third, err)
	}
}

func TestExpiredUnsafeActionRequiresReview(t *testing.T) {
	for _, action := range []model.CaseActionExecution{
		{ActionType: model.ActionBanUser, SafeForRetry: true, Irreversible: true, MaxRetries: 3},
		{ActionType: model.ActionKickUser, MaxRetries: 3},
		{ActionType: model.ActionTimeoutUser, SafeForRetry: true, MaxRetries: 0},
	} {
		t.Run(string(action.ActionType), func(t *testing.T) {
			ctx := context.Background()
			repository, guildID := templateTestStore(t)
			action.ConfigSnapshotJSON = `{}`
			created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{action}})
			if err != nil {
				t.Fatal(err)
			}
			first, err := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "old"})
			if err != nil || first == nil {
				t.Fatalf("claim: %+v %v", first, err)
			}
			if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", first.Execution.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			second, err := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "new"})
			if err != nil || second != nil {
				t.Fatalf("unsafe repeat: %+v %v", second, err)
			}
			current, err := repository.GetCaseActionExecution(ctx, guildID, first.Execution.ID)
			if err != nil || current.Status != model.ActionExecutionFailed || current.AttemptCount != 1 || current.LeaseToken != "" {
				t.Fatalf("review state: %+v %v", current, err)
			}
			err = repository.CompleteCaseAction(ctx, storage.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken, AttemptNumber: 1, WorkerID: "old", AttemptStatus: model.ActionAttemptSucceeded, ExecutionStatus: model.ActionExecutionSucceeded})
			if err == nil {
				t.Fatal("expired worker overwrote review state")
			}
		})
	}
}

// TestRetryCannotResurrectVoidedPunishment checks the storage boundary even if a
// caller authorized a retry before another moderator voided the case.
func TestRetryCannotResurrectVoidedPunishment(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionBanUser, Status: model.ActionExecutionFailed, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", Reason: "Mistaken identity"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RetryCaseAction(ctx, model.RetryCaseActionParams{GuildID: guildID, ExecutionID: actions[0].ID, ActorDiscordUserID: "mod"}); err == nil {
		t.Fatal("retried punishment on voided case")
	}
	claimed, err := repository.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
	if err != nil || claimed != nil {
		t.Fatalf("voided punishment became executable: %+v, %v", claimed, err)
	}
}
