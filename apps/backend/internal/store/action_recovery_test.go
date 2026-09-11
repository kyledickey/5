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
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, SafeForRetry: true, MaxRetries: 1, ConfigSnapshotJSON: `{"duration_seconds":60}`}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil || first == nil || first.Execution.LeaseToken == "" {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", first.Execution.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-2"})
	if err != nil || second == nil || second.Execution.LeaseToken == first.Execution.LeaseToken {
		t.Fatalf("reclaimed action: %+v err=%v", second, err)
	}
	stale := model.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken, AttemptNumber: first.Execution.AttemptCount, WorkerID: "worker-1", AttemptStatus: model.ActionAttemptSucceeded, ExecutionStatus: model.ActionExecutionSucceeded}
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
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, claimErr := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
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
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, ConfigSnapshotJSON: `{}`}, {Position: 1, ActionType: model.ActionKickUser, Status: model.ActionExecutionFailed, ConfigSnapshotJSON: `{}`}}})
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
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), Notification: notification})
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
			created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{action}})
			if err != nil {
				t.Fatal(err)
			}
			first, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "old"})
			if err != nil || first == nil {
				t.Fatalf("claim: %+v %v", first, err)
			}
			if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", first.Execution.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			second, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "new"})
			if err != nil || second != nil {
				t.Fatalf("unsafe repeat: %+v %v", second, err)
			}
			current, err := repository.GetCaseActionExecution(ctx, guildID, first.Execution.ID)
			if err != nil || current.Status != model.ActionExecutionFailed || current.AttemptCount != 1 || current.LeaseToken != "" {
				t.Fatalf("review state: %+v %v", current, err)
			}
			err = repository.CompleteCaseAction(ctx, model.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken, AttemptNumber: 1, WorkerID: "old", AttemptStatus: model.ActionAttemptSucceeded, ExecutionStatus: model.ActionExecutionSucceeded})
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
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionBanUser, Status: model.ActionExecutionFailed, ConfigSnapshotJSON: `{}`}}})
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
	claimed, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
	if err != nil || claimed != nil {
		t.Fatalf("voided punishment became executable: %+v, %v", claimed, err)
	}
}

// TestVoidQueuesReversalAcrossCompletionRace exercises both transaction orders:
// completed punishment then void, and void while Discord enforcement is in flight.
func TestVoidQueuesReversalAcrossCompletionRace(t *testing.T) {
	for _, late := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "in_flight"}[late], func(t *testing.T) {
			ctx := context.Background()
			repository, guildID := templateTestStore(t)
			created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionBanUser, ConfigSnapshotJSON: `{}`}}})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
			if err != nil || claimed == nil {
				t.Fatalf("claim: %+v %v", claimed, err)
			}
			complete := func() {
				t.Helper()
				if err := repository.CompleteCaseAction(ctx, model.CompleteCaseActionParams{ExecutionID: claimed.Execution.ID, LeaseToken: claimed.Execution.LeaseToken, AttemptNumber: claimed.Execution.AttemptCount, AttemptStatus: model.ActionAttemptSucceeded, ExecutionStatus: model.ActionExecutionSucceeded}); err != nil {
					t.Fatal(err)
				}
			}
			void := func() {
				t.Helper()
				if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "moderator", Reason: "Mistaken identity"}); err != nil {
					t.Fatal(err)
				}
			}
			if !late {
				complete()
			}
			void()
			if late {
				complete()
			}
			void()
			actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
			if err != nil || len(actions) != 2 || actions[1].ActionType != model.ActionUnbanUser || actions[1].Status != model.ActionExecutionPending || actions[1].ReversalOfExecutionID == nil || *actions[1].ReversalOfExecutionID != claimed.Execution.ID {
				t.Fatalf("reversal: %+v %v", actions, err)
			}
			// A fresh polling cycle must discover removal even though the case is voided.
			ids, err := repository.ListExecutableCaseIDs(ctx, 10)
			if err != nil || len(ids) != 1 || ids[0] != created.Case.ID {
				t.Fatalf("durable polling lost reversal: %v %v", ids, err)
			}
		})
	}
}

// TestVoidedInFlightFailureCannotRetry verifies both a reported safe retry and a
// lost worker lease stay out of automatic punishment execution after a void.
func TestVoidedInFlightFailureCannotRetry(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "reported_failure", true: "expired_worker"}[expired], func(t *testing.T) {
			ctx := context.Background()
			repository, guildID := templateTestStore(t)
			created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, SafeForRetry: true, MaxRetries: 3, ConfigSnapshotJSON: `{}`}}})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
			if err != nil || claimed == nil {
				t.Fatalf("claim: %v", err)
			}
			if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", Reason: "Mistake"}); err != nil {
				t.Fatal(err)
			}
			if expired {
				if err := repository.DB().Model(&model.CaseActionExecution{}).Where("id = ?", claimed.Execution.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := repository.CompleteCaseAction(ctx, model.CompleteCaseActionParams{ExecutionID: claimed.Execution.ID, LeaseToken: claimed.Execution.LeaseToken, AttemptNumber: claimed.Execution.AttemptCount, AttemptStatus: model.ActionAttemptFailed, ExecutionStatus: model.ActionExecutionRetrying, ErrorCode: "transport_failed"}); err != nil {
					t.Fatal(err)
				}
			}
			next, err := repository.ClaimNextCaseAction(ctx, model.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "next"})
			if err != nil || next != nil {
				t.Fatalf("void retried enforcement: %+v %v", next, err)
			}
			actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
			if err != nil || len(actions) != 1 || actions[0].Status != model.ActionExecutionFailed {
				t.Fatalf("lost review failure: %+v %v", actions, err)
			}
		})
	}
}

// TestVoidRollsBackWhenRemovalCannotBeStored proves a saved void cannot lose its
// removal work when writing the inverse fails before the transaction commits.
func TestVoidRollsBackWhenRemovalCannotBeStored(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, model.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []model.CaseActionExecution{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Exec("CREATE TRIGGER reject_inverse BEFORE INSERT ON case_action_executions WHEN NEW.reversal_of_execution_id IS NOT NULL BEGIN SELECT RAISE(ABORT, 'simulated storage failure'); END").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", Reason: "Mistake"}); err == nil {
		t.Fatal("void committed without inverse")
	}
	persisted, err := repository.GetCaseByID(ctx, created.Case.ID)
	if err != nil || persisted.Validity != model.CaseValidityValid {
		t.Fatalf("void escaped rollback: %+v %v", persisted, err)
	}
	if err := repository.DB().Exec("DROP TRIGGER reject_inverse").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", Reason: "Mistake"}); err != nil {
		t.Fatal(err)
	}
	actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil || len(actions) != 2 || actions[1].ActionType != model.ActionRemoveTimeout {
		t.Fatalf("timeout removal: %+v %v", actions, err)
	}
}
