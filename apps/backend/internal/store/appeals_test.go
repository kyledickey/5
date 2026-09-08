package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

func newAppealTestStore(t *testing.T) (*Store, *model.Guild) {
	t.Helper()
	db := openSQLiteMigrationDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open appeal test connection: %v", err)
	}
	// SQLite ignores SELECT FOR UPDATE. Keep one connection so concurrent
	// transition tests model MySQL's row-lock serialization instead of failing
	// both transactions with shared-cache table-lock errors.
	sqlDB.SetMaxOpenConns(1)
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("migrate appeals schema: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), model.UpsertGuildParams{DiscordGuildID: "appeal-guild", Name: "Appeal Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create guild: %v", err)
	}
	return repository, guild
}

func createAppealableCase(t *testing.T, repository *Store, guildID, target string, appealable bool) *model.Case {
	t.Helper()
	snapshot := `{"template":{"appealable":true}}`
	if !appealable {
		snapshot = `{"template":{"appealable":false}}`
	}
	created, err := repository.CreateCase(context.Background(), model.CreateCaseParams{
		Case:  model.Case{GuildID: guildID, TemplateVersion: 1, TemplateSnapshotJSON: snapshot, TargetDiscordUserID: target, ModeratorDiscordUserID: "moderator", Reason: "Official reason", Validity: model.CaseValidityValid, Source: model.CaseSourceDashboard, MetadataJSON: "{}", ContextValuesJSON: "[]"},
		Event: model.CaseEvent{EventType: model.CaseEventCreated, ActorDiscordUserID: "moderator", ActorType: "staff", Visibility: model.EventVisibilityPublic, Body: "Case created", MetadataJSON: "{}"},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	return &created.Case
}

func TestLogical0200MigrationCreatesAppealContracts(t *testing.T) {
	repository, _ := newAppealTestStore(t)
	for _, table := range []any{&AppealRecord{}, &AppealEventRecord{}, &GuildAppealSettingsRecord{}, &AppealNotificationRecord{}} {
		if !repository.db.Migrator().HasTable(table) {
			t.Fatalf("missing appeal table for %T", table)
		}
	}
	if !repository.db.Migrator().HasIndex(&AppealRecord{}, "CaseID") {
		t.Fatal("missing one-appeal-per-case unique index")
	}
}

func TestLogical0200MigrationPreservesPlaceholderAppealsSafely(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:8]); err != nil {
		t.Fatalf("migrate baseline: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), model.UpsertGuildParams{DiscordGuildID: "legacy-appeal", Name: "Legacy Appeal", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create guild: %v", err)
	}
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	now := time.Now().UTC()
	legacy := migration0200LegacyAppeal{ID: "01KXLEGACYAPPEAL0000000001", GuildID: guild.ID, CaseID: &item.ID, TargetDiscordUserID: "target", Status: string(model.AppealStatusPending), Content: "legacy content", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := insertLegacyAppeal(db, &legacy); err != nil {
		t.Fatalf("insert legacy appeal: %v", err)
	}
	legacyEvent := AppealEventRecord{ULIDModelRecord: ULIDModelRecord{ID: "01KXLEGACYAPPEALEVENT0001", CreatedAt: now, UpdatedAt: now}, AppealID: legacy.ID, GuildID: guild.ID, EventType: "reviewed", ActorDiscordUserID: "legacy-moderator", Body: "legacy review", MetadataJSON: "{}"}
	// The pre-appeals placeholder predates actor classification; keep this fixture
	// faithful to that historical table while production uses the current record.
	if err := db.Omit("ActorType").Create(&legacyEvent).Error; err != nil {
		t.Fatalf("insert legacy event: %v", err)
	}
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("upgrade legacy appeal: %v", err)
	}
	var upgraded AppealRecord
	if err := db.First(&upgraded, "id = ?", legacy.ID).Error; err != nil {
		t.Fatalf("read upgraded appeal: %v", err)
	}
	if upgraded.Content != legacy.Content || !strings.Contains(upgraded.QuestionSnapshotJSON, "legacy_content") || !strings.Contains(upgraded.AnswersJSON, legacy.Content) || upgraded.Version != 1 {
		t.Fatalf("legacy appeal was not preserved and backfilled: %+v", upgraded)
	}
	legacyResponse, err := quack.NewAppealService(repository).GetMember(context.Background(), legacy.ID, "target")
	if err != nil || len(legacyResponse.Questions) != 1 || len(legacyResponse.Answers) != 1 {
		t.Fatalf("upgraded legacy appeal is not readable: %+v err=%v", legacyResponse, err)
	}
	var upgradedEvent AppealEventRecord
	if err := db.First(&upgradedEvent, "id = ?", legacyEvent.ID).Error; err != nil || upgradedEvent.ActorType != "staff" {
		t.Fatalf("legacy staff identity was not safely classified: %+v err=%v", upgradedEvent, err)
	}
}

func TestMySQLLogical0200AppealMigrationAndAcceptance(t *testing.T) {
	db := openMySQLMigrationDB(t)
	if err := runMigrations(db, registeredMigrations()[:8]); err != nil {
		t.Fatalf("migrate MySQL baseline: %v", err)
	}
	repository := New(db, nil)
	guild, err := repository.UpsertGuild(context.Background(), model.UpsertGuildParams{DiscordGuildID: "mysql-appeal", Name: "MySQL Appeal", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatalf("create MySQL guild: %v", err)
	}
	legacyCase := createAppealableCase(t, repository, guild.ID, "legacy-target", true)
	now := time.Now().UTC()
	legacy := migration0200LegacyAppeal{ID: "01KXMYSQLLEGACYAPPEAL00001", GuildID: guild.ID, CaseID: &legacyCase.ID, TargetDiscordUserID: "legacy-target", Status: string(model.AppealStatusPending), Content: "preserved MySQL content", MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
	if err := insertLegacyAppeal(db, &legacy); err != nil {
		t.Fatalf("insert MySQL legacy appeal: %v", err)
	}
	migrations := registeredMigrations()
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("migrate MySQL appeal schema: %v", err)
	}
	var upgraded AppealRecord
	if err := db.First(&upgraded, "id = ?", legacy.ID).Error; err != nil || upgraded.Content != legacy.Content || upgraded.Version != 1 {
		t.Fatalf("MySQL legacy appeal was not preserved: %+v err=%v", upgraded, err)
	}
	// Historical migration verification above is complete. Current acceptance
	// transactions also write durable public-receipt refresh requests.
	if err := db.AutoMigrate(&model.CasePublication{}); err != nil {
		t.Fatalf("prepare current publication runtime schema: %v", err)
	}
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	service := quack.NewAppealService(repository)
	appeal, err := service.Submit(context.Background(), item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit MySQL appeal: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
	if _, err := service.Accept(context.Background(), moderator, appeal.ID, "Accepted after review."); err != nil {
		t.Fatalf("accept MySQL appeal: %v", err)
	}
	persisted, err := repository.GetCaseByID(context.Background(), item.ID)
	if err != nil || persisted.Validity != model.CaseValidityVoided {
		t.Fatalf("MySQL acceptance did not atomically void case: %+v err=%v", persisted, err)
	}
}

func TestAppealServiceOwnershipSnapshotTimelineAndAtomicAcceptance(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	// The shared fixture intentionally tests historical migrations; this service
	// test additionally needs the current publication transaction dependency.
	if err := repository.db.AutoMigrate(&model.CasePublication{}); err != nil {
		t.Fatalf("prepare current publication runtime schema: %v", err)
	}

	caseModel := createAppealableCase(t, repository, guild.ID, "target", true)
	now := time.Now().UTC()
	originalAction := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "01KXAPPEALACTION0000000001", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Position: 0, ActionType: model.ActionBanUser, Status: model.ActionExecutionSucceeded, IdempotencyKey: "appeal-original-ban", ConfigSnapshotJSON: "{}", SafeForRetry: false, Irreversible: true}
	if err := repository.db.Create(&originalAction).Error; err != nil {
		t.Fatalf("create original enforcement: %v", err)
	}
	queuedAction := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "01KXAPPEALACTION0000000002", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Position: 1, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionPending, IdempotencyKey: "appeal-pending-timeout", ConfigSnapshotJSON: "{}", SafeForRetry: true}
	if err := repository.db.Create(&queuedAction).Error; err != nil {
		t.Fatalf("create queued enforcement: %v", err)
	}
	queuedCaseNotification := model.CaseNotification{ULIDModel: model.ULIDModel{ID: "01KXAPPEALNOTICE00000000001", CreatedAt: now, UpdatedAt: now}, CaseID: caseModel.ID, Status: model.NotificationPending}
	if err := repository.db.Create(&queuedCaseNotification).Error; err != nil {
		t.Fatalf("create queued case notification: %v", err)
	}
	service := quack.NewAppealService(repository)

	settings, err := service.GetSettings(ctx, guild.ID)
	if err != nil || !settings.Default || len(settings.Questions) == 0 {
		t.Fatalf("default settings: %+v err=%v", settings, err)
	}
	// Pre-release custom forms must not change the shared single-statement form.
	if _, err := repository.UpdateGuildAppealSettings(ctx, model.UpdateGuildAppealSettingsParams{Settings: model.GuildAppealSettings{GuildID: guild.ID, QuestionsJSON: `[{"id":"old","prompt":"Old custom form","type":"long_text","required":true,"position":0}]`}}); err != nil {
		t.Fatal(err)
	}
	answers := []model.AppealAnswer{{QuestionID: "reason", Value: "The decision should be reconsidered."}}
	appeal, err := service.Submit(ctx, caseModel.ID, "target", quack.AppealSubmissionInput{Answers: answers})
	if err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	if appeal.Status != model.AppealStatusPending || len(appeal.Questions) != 1 || len(appeal.Events) != 1 {
		t.Fatalf("unexpected submitted appeal: %+v", appeal)
	}
	snapshotted, err := service.GetMember(ctx, appeal.ID, "target")
	if err != nil || len(snapshotted.Questions) != 1 || snapshotted.Questions[0].ID != "reason" {
		t.Fatalf("appeal form was not snapshotted: %+v err=%v", snapshotted, err)
	}
	if _, err := service.Submit(ctx, caseModel.ID, "target", quack.AppealSubmissionInput{Answers: answers}); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("expected one appeal per case, got %v", err)
	}
	if _, err := service.GetMember(ctx, appeal.ID, "other"); !errors.Is(err, quack.ErrAppealNotFound) {
		t.Fatalf("unrelated member read should be hidden, got %v", err)
	}

	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, ActorDiscordUserID: "moderator", Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
	memberView, err := service.GetMember(ctx, appeal.ID, "target")
	if err != nil {
		t.Fatalf("member read: %v", err)
	}
	for _, event := range memberView.Events {
		if event.ActorType == "staff" && event.ActorDiscordUserID != "" {
			t.Fatalf("member timeline leaked staff identity: %+v", event)
		}
	}
	if err := repository.db.Create(&GuildSettingsRecord{ULIDModelRecord: ULIDModelRecord{ID: "01KXAPPEALSETTINGS000000001"}, GuildID: guild.ID, AppealRejoinURL: "https://discord.gg/pond"}).Error; err != nil {
		t.Fatal(err)
	}
	accepted, err := service.Accept(ctx, moderator, appeal.ID, "The statement changes the decision.")
	if err != nil || accepted.Status != model.AppealStatusAccepted || len(accepted.ReversalOffers) != 0 {
		t.Fatalf("accept appeal: %+v err=%v", accepted, err)
	}
	actionsBeforeReversal, err := repository.ListCaseActionExecutions(ctx, caseModel.ID)
	if err != nil || len(actionsBeforeReversal) != 3 || actionsBeforeReversal[2].ActionType != model.ActionUnbanUser || actionsBeforeReversal[2].Status != model.ActionExecutionPending || actionsBeforeReversal[1].Status != model.ActionExecutionCancelled || actionsBeforeReversal[1].LastErrorCode != "case_voided" {
		t.Fatalf("acceptance did not queue punishment removal: actions=%+v err=%v", actionsBeforeReversal, err)
	}
	var cancelledNotification model.CaseNotification
	if err := repository.db.First(&cancelledNotification, "id = ?", queuedCaseNotification.ID).Error; err != nil || cancelledNotification.Status != model.NotificationFailed || cancelledNotification.LastErrorCode != "case_voided" {
		t.Fatalf("appeal acceptance did not cancel queued case notification: %+v err=%v", cancelledNotification, err)
	}
	appealID := appeal.ID
	queued, err := repository.QueueCaseReversal(ctx, model.QueueCaseReversalParams{GuildID: guild.ID, CaseID: caseModel.ID, ActorDiscordUserID: "moderator", OriginalExecutionID: originalAction.ID, ActionType: model.ActionUnbanUser, AppealID: &appealID})
	if err != nil || queued == nil || queued.ReversalAppealID == nil || *queued.ReversalAppealID != appeal.ID {
		t.Fatalf("explicit accepted-appeal reversal was not linked: %+v err=%v", queued, err)
	}
	persistedCase, err := repository.GetCaseByID(ctx, caseModel.ID)
	if err != nil || persistedCase.Validity != model.CaseValidityVoided {
		t.Fatalf("accepted appeal did not atomically void case: %+v err=%v", persistedCase, err)
	}
	if _, err := service.Reject(ctx, moderator, appeal.ID, "late competing decision"); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("accepted appeal allowed competing decision: %v", err)
	}
	caseService := quack.NewCaseService(repository)
	memberDetail, err := caseService.GetMemberCase(ctx, caseModel.ID, "target")
	if err != nil || memberDetail.Validity != model.CaseValidityVoided || memberDetail.AppealStatus != model.AppealStatusAccepted || memberDetail.Appealable {
		t.Fatalf("member case projection did not retain voided accepted appeal: %+v err=%v", memberDetail, err)
	}
	memberCases, err := caseService.ListMemberCases(ctx, guild.ID, "target", quack.CaseListInput{})
	if err != nil || len(memberCases.Cases) != 1 || memberCases.Cases[0].Validity != model.CaseValidityVoided {
		t.Fatalf("member history omitted voided case: %+v err=%v", memberCases, err)
	}
	encodedMember, _ := json.Marshal(memberDetail)
	if strings.Contains(string(encodedMember), "moderator") || strings.Contains(string(encodedMember), "worker") || strings.Contains(string(encodedMember), "last_error") {
		t.Fatalf("member case projection exposed staff or internal fields: %s", encodedMember)
	}
	var notificationRecords []AppealNotificationRecord
	err = repository.db.Where("status = ?", model.AppealNotificationPending).Order("created_at ASC").Find(&notificationRecords).Error
	notifications := make([]model.AppealNotification, 0, len(notificationRecords))
	for _, record := range notificationRecords {
		notifications = append(notifications, appealNotificationModel(record))
	}
	if err != nil || len(notifications) < 2 {
		t.Fatalf("expected staff and member notifications, got %+v err=%v", notifications, err)
	}
	for _, notification := range notifications {
		if notification.Audience != model.AppealNotificationMember {
			continue
		}
		var intent model.AppealDecisionIntent
		if err := json.Unmarshal([]byte(notification.DecisionIntentJSON), &intent); err != nil || intent.Version != 1 || intent.Status != model.AppealStatusAccepted || intent.RejoinURL != "https://discord.gg/pond" || intent.Reason == "" || notification.Body != "" {
			t.Fatalf("accepted notice lost decision snapshot: %+v %v", notification, err)
		}
		if strings.Contains(notification.DecisionIntentJSON, "moderator") {
			t.Fatalf("member intent leaked staff: %+v", notification)
		}
	}

	client := &appealNotificationClientStub{}
	var dispatchErrors [2]error
	var dispatchWait sync.WaitGroup
	for index := range dispatchErrors {
		dispatchWait.Add(1)
		go func(index int) {
			defer dispatchWait.Done()
			dispatchErrors[index] = quack.NewAppealNotificationDispatcher(repository, client).DispatchPending(ctx, 10)
		}(index)
	}
	dispatchWait.Wait()
	for _, dispatchErr := range dispatchErrors {
		if dispatchErr != nil {
			t.Fatalf("dispatch appeal notifications: %v", dispatchErr)
		}
	}
	var remaining int64
	err = repository.db.Model(&AppealNotificationRecord{}).Where("status IN ?", []model.AppealNotificationStatus{model.AppealNotificationPending, model.AppealNotificationClaimed}).Count(&remaining).Error
	if client.lastStaff == nil || client.lastStaff.CaseNumber != caseModel.CaseNumber || client.lastStaff.Status != model.AppealStatusAccepted || len(client.lastStaff.Answers) != 1 {
		t.Fatalf("delayed queue delivery lost current appeal context: %+v", client.lastStaff)
	}
	memberSends, staffSends := client.counts()
	if err != nil || remaining != 0 || memberSends == 0 || staffSends == 0 || memberSends+staffSends != len(notifications) {
		t.Fatalf("notification adapter did not deliver each item once: remaining=%d member=%d staff=%d expected=%d err=%v", remaining, memberSends, staffSends, len(notifications), err)
	}
}

func TestAppealServiceRejectsIneligibleCasesAndConcurrentDecisions(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	service := quack.NewAppealService(repository)
	nonAppealable := createAppealableCase(t, repository, guild.ID, "target", false)
	if _, err := service.Submit(ctx, nonAppealable.ID, "target", quack.AppealSubmissionInput{}); !errors.Is(err, model.ErrAppealCaseIneligible) {
		t.Fatalf("non-appealable case accepted: %v", err)
	}
	eligible := createAppealableCase(t, repository, guild.ID, "target", true)
	appeal, err := service.Submit(ctx, eligible.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit eligible appeal: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
	var successes int
	var mutex sync.Mutex
	var wait sync.WaitGroup
	for _, accept := range []bool{true, false} {
		accept := accept
		wait.Add(1)
		go func() {
			defer wait.Done()
			var transitionErr error
			if accept {
				_, transitionErr = service.Accept(ctx, moderator, appeal.ID, "accepted concurrently")
			} else {
				_, transitionErr = service.Reject(ctx, moderator, appeal.ID, "rejected concurrently")
			}
			if transitionErr == nil {
				mutex.Lock()
				successes++
				mutex.Unlock()
			}
		}()
	}
	wait.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly one concurrent decision, got %d", successes)
	}
}

func TestAppealNotificationClaimRecoversExpiredLeaseAndRejectsStaleCompletion(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	service := quack.NewAppealService(repository)
	if _, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}}); err != nil {
		t.Fatalf("submit appeal: %v", err)
	}
	first, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(first) != 1 || first[0].Status != model.AppealNotificationClaimed || first[0].LeaseToken == "" {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.db.Model(&AppealNotificationRecord{}).Where("id = ?", first[0].ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatalf("expire first claim: %v", err)
	}
	second, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(second) != 1 || second[0].ID != first[0].ID || second[0].LeaseToken == first[0].LeaseToken {
		t.Fatalf("reclaimed notification: first=%+v second=%+v err=%v", first, second, err)
	}
	if err := repository.CompleteAppealNotification(ctx, model.CompleteAppealNotificationParams{NotificationID: first[0].ID, LeaseToken: first[0].LeaseToken, Status: model.AppealNotificationSent}); !errors.Is(err, model.ErrAppealStateConflict) {
		t.Fatalf("stale lease completed reclaimed notification: %v", err)
	}
	if err := repository.BeginAppealNotificationDelivery(ctx, first[0].ID, first[0].LeaseToken); !errors.Is(err, model.ErrAppealStateConflict) {
		t.Fatalf("stale worker began send: %v", err)
	}
	if err := repository.BeginAppealNotificationDelivery(ctx, second[0].ID, second[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteAppealNotification(ctx, model.CompleteAppealNotificationParams{NotificationID: second[0].ID, LeaseToken: second[0].LeaseToken, Status: model.AppealNotificationSent}); err != nil {
		t.Fatalf("current lease completion: %v", err)
	}
}

// TestAppealDecisionsAreTerminal verifies close is rejection and neither decision
// permits a second submission, reopening, or a competing outcome.
func TestAppealDecisionsAreTerminal(t *testing.T) {
	for _, closeInstead := range []bool{false, true} {
		t.Run(map[bool]string{false: "reject", true: "close"}[closeInstead], func(t *testing.T) {
			ctx := context.Background()
			repository, guild := newAppealTestStore(t)
			service := quack.NewAppealService(repository)
			item := createAppealableCase(t, repository, guild.ID, "target", true)
			input := quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}}
			appeal, err := service.Submit(ctx, item.ID, "target", input)
			if err != nil {
				t.Fatal(err)
			}
			moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
			decide := service.Reject
			if closeInstead {
				decide = service.Close
			}
			rejected, err := decide(ctx, moderator, appeal.ID, "The case stands.")
			if err != nil || rejected.Status != model.AppealStatusRejected || len(rejected.Events) != 2 {
				t.Fatalf("decision: %+v %v", rejected, err)
			}
			if _, err := service.Accept(ctx, moderator, appeal.ID, "Changed mind"); !errors.Is(err, quack.ErrAppealConflict) {
				t.Fatalf("terminal appeal accepted: %v", err)
			}
			if _, err := service.Submit(ctx, item.ID, "target", input); !errors.Is(err, quack.ErrAppealConflict) {
				t.Fatalf("second appeal submitted: %v", err)
			}
			if _, err := repository.TransitionAppeal(ctx, model.TransitionAppealParams{GuildID: guild.ID, AppealID: appeal.ID, AllowedFrom: []model.AppealStatus{model.AppealStatusRejected}, To: model.AppealStatusPending}); !errors.Is(err, model.ErrAppealStateConflict) {
				t.Fatalf("storage reopened rejected appeal: %v", err)
			}
			persisted, err := repository.GetCaseByID(ctx, item.ID)
			if err != nil || persisted.Validity != model.CaseValidityValid {
				t.Fatalf("rejection changed case: %+v %v", persisted, err)
			}
		})
	}
}

func TestAppealAcceptanceAndDirectVoidCannotProduceAcceptedValidCase(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	service := quack.NewAppealService(repository)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	appeal, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		_, _ = service.Accept(ctx, moderator, appeal.ID, "Accepted concurrently.")
	}()
	go func() {
		defer wait.Done()
		_, _ = repository.VoidCase(ctx, model.VoidCaseParams{GuildID: guild.ID, CaseID: item.ID, ActorDiscordUserID: "other-moderator", Reason: "Direct correction"})
	}()
	wait.Wait()
	persistedAppeal, err := repository.GetAppealByID(ctx, appeal.ID)
	if err != nil {
		t.Fatalf("read appeal: %v", err)
	}
	persistedCase, err := repository.GetCaseByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	if persistedAppeal.Status == model.AppealStatusAccepted && persistedCase.Validity != model.CaseValidityVoided {
		t.Fatalf("race produced accepted appeal with valid case: appeal=%+v case=%+v", persistedAppeal, persistedCase)
	}
}

type appealNotificationClientStub struct {
	mutex     sync.Mutex
	member    int
	staff     int
	lastStaff *quack.AppealResponse
}

func insertLegacyAppeal(db *gorm.DB, appeal *migration0200LegacyAppeal) error {
	return db.Select("id", "guild_id", "case_id", "target_discord_user_id", "status", "content", "decision_reason", "reviewed_by_discord_user_id", "reviewed_at", "review_message_discord_id", "metadata_json", "created_at", "updated_at").Create(appeal).Error
}

func (c *appealNotificationClientStub) SendAppealMemberNotification(context.Context, string, quack.AppealMemberNotification) (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.member++
	return "member-message", nil
}

func (c *appealNotificationClientStub) SendAppealStaffNotification(_ context.Context, _ string, appeal *quack.AppealResponse, receipt quack.AppealQueueReceipt) (quack.AppealQueueReceipt, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.staff++
	c.lastStaff = appeal
	return quack.AppealQueueReceipt{ChannelID: "staff-channel", MessageID: "staff-message"}, nil
}

func (c *appealNotificationClientStub) counts() (int, int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.member, c.staff
}

// TestAppealNotificationRecoverySeparatesSafeFailureFromUnknownSend prevents
// both lost queue deliveries after setup repair and duplicate ambiguous sends.
func TestAppealNotificationRecoverySeparatesSafeFailureFromUnknownSend(t *testing.T) {
	ctx := context.Background()
	repository, guild := newAppealTestStore(t)
	item := createAppealableCase(t, repository, guild.ID, "target", true)
	if _, err := quack.NewAppealService(repository).Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}}); err != nil {
		t.Fatal(err)
	}
	claim, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(claim) != 1 {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	first := claim[0]
	if err := repository.BeginAppealNotificationDelivery(ctx, first.ID, first.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteAppealNotification(ctx, model.CompleteAppealNotificationParams{NotificationID: first.ID, LeaseToken: first.LeaseToken, Status: model.AppealNotificationFailed, ErrorCode: "delivery_deferred"}); err != nil {
		t.Fatal(err)
	}
	if immediate, err := repository.ClaimPendingAppealNotifications(ctx, 1); err != nil || len(immediate) != 0 {
		t.Fatalf("retry ignored backoff: %+v %v", immediate, err)
	}
	if err := repository.db.Model(&AppealNotificationRecord{}).Where("id = ?", first.ID).Update("updated_at", time.Now().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	retried, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(retried) != 1 || retried[0].ID != first.ID {
		t.Fatalf("safe failure was lost: %+v %v", retried, err)
	}
	if err := repository.BeginAppealNotificationDelivery(ctx, first.ID, retried[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.Model(&AppealNotificationRecord{}).Where("id = ?", first.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	next, err := repository.ClaimPendingAppealNotifications(ctx, 1)
	if err != nil || len(next) != 0 {
		t.Fatalf("ambiguous send repeated: %+v %v", next, err)
	}
	var failed AppealNotificationRecord
	if err := repository.db.First(&failed, "id = ?", first.ID).Error; err != nil || failed.Status != model.AppealNotificationFailed || failed.LastErrorCode != "delivery_outcome_unknown" {
		t.Fatalf("unknown outcome lost: %+v %v", failed, err)
	}
}

// TestAppealDecisionRefreshSurvivesInFlightDelivery verifies service decisions
// refresh the original staff message even when its first send finishes later.
func TestAppealDecisionRefreshSurvivesInFlightDelivery(t *testing.T) {
	for _, inFlight := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "sending"}[inFlight], func(t *testing.T) {
			ctx := context.Background()
			repository, guild := newAppealTestStore(t)
			item := createAppealableCase(t, repository, guild.ID, "target", true)
			service := quack.NewAppealService(repository)
			appeal, err := service.Submit(ctx, item.ID, "target", quack.AppealSubmissionInput{Answers: []model.AppealAnswer{{QuestionID: "reason", Value: "Please reconsider."}}})
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := repository.ClaimPendingAppealNotifications(ctx, 10)
			if err != nil || len(claimed) != 1 {
				t.Fatalf("initial queue: %+v %v", claimed, err)
			}
			notification := claimed[0]
			if err := repository.BeginAppealNotificationDelivery(ctx, notification.ID, notification.LeaseToken); err != nil {
				t.Fatal(err)
			}
			complete := func() {
				t.Helper()
				if err := repository.CompleteAppealNotification(ctx, model.CompleteAppealNotificationParams{NotificationID: notification.ID, LeaseToken: notification.LeaseToken, DeliveryChannelID: "queue", DeliveryMessageID: "original", Status: model.AppealNotificationSent}); err != nil {
					t.Fatal(err)
				}
			}
			if !inFlight {
				complete()
			}
			moderator := &quack.GuildStaffContext{Guild: guild, Staff: &model.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionAppealReview: true}}
			if _, err := service.Reject(ctx, moderator, appeal.ID, "Decision stands."); err != nil {
				t.Fatal(err)
			}
			if inFlight {
				complete()
			}
			claimed, err = repository.ClaimPendingAppealNotifications(ctx, 10)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, refresh := range claimed {
				if refresh.Audience != model.AppealNotificationStaff {
					continue
				}
				found = true
				if refresh.ID != notification.ID || refresh.DeliveryChannelID != "queue" || refresh.DeliveryMessageID != "original" || refresh.RefreshRequested {
					t.Fatalf("lost refresh receipt: %+v", refresh)
				}
				if err := repository.BeginAppealNotificationDelivery(ctx, refresh.ID, refresh.LeaseToken); err != nil {
					t.Fatal(err)
				}
				notification = refresh
				complete()
			}
			if !found {
				t.Fatal("decision did not queue staff refresh")
			}
			var stored AppealNotificationRecord
			if err := repository.db.First(&stored, "id = ?", notification.ID).Error; err != nil || stored.Status != model.AppealNotificationSent {
				t.Fatalf("refresh did not settle: %+v %v", stored, err)
			}
		})
	}
}
