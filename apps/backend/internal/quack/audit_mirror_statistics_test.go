package quack_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

type fakeAuditMirrorSender struct {
	mu       sync.Mutex
	messages []quack.AuditMirrorMessage
	err      error
}

func (f *fakeAuditMirrorSender) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	return f.err
}

func TestAuditMirrorWorkerIsNonBlockingRedactedAndRepairable(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	moderator := templateGuildContext(t, repository, "mirror-guild", "moderator", uint64(discordgo.PermissionModerateMembers))
	if _, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "mirror-guild", Name: "Guild", OwnerDiscordUserID: "owner-1"}); err != nil {
		t.Fatal(err)
	}
	settings, err := repository.GetGuildSettings(ctx, moderator.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.AuditMirrorChannelDiscordID = "123456789012345678"
	if err := repository.DB().Model(&model.GuildSettings{}).Where("id = ?", settings.ID).Update("audit_mirror_channel_discord_id", settings.AuditMirrorChannelDiscordID).Error; err != nil {
		t.Fatal(err)
	}
	sender := &fakeAuditMirrorSender{}
	worker := quack.NewAuditMirrorWorker(repository, sender, time.Millisecond)
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	sender.messages = nil
	sender.mu.Unlock()
	entry := model.AuditLogEntry{GuildID: moderator.Guild.ID, ActorDiscordUserID: "moderator", Source: model.AuditSourceDiscord, Action: string(model.AuditActionCaseCreate), ResourceType: "case", ResourceID: "case-1", Result: model.AuditResultSuccess, MetadataJSON: `{"token":"do-not-send","case_id":"case-1"}`}
	if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0].AuditEntryID != entry.ID || sender.messages[0].MetadataJSON != `{"case_id":"case-1","token":"[REDACTED]"}` {
		t.Fatalf("unexpected redacted mirror delivery: %+v", sender.messages)
	}
	if err := worker.PollOnce(ctx); err != nil || len(sender.messages) != 1 {
		t.Fatalf("delivered entry was mirrored more than once: count=%d err=%v", len(sender.messages), err)
	}
	concurrent := model.AuditLogEntry{GuildID: moderator.Guild.ID, Source: model.AuditSourceSystem, Action: string(model.AuditActionCaseVoid), ResourceType: "case", ResourceID: "case-concurrent", Result: model.AuditResultSuccess, MetadataJSON: "{}"}
	if err := repository.CreateAuditLogEntry(ctx, &concurrent); err != nil {
		t.Fatal(err)
	}
	var polls sync.WaitGroup
	for range 20 {
		polls.Add(1)
		go func() {
			defer polls.Done()
			if err := worker.PollOnce(ctx); err != nil {
				t.Errorf("concurrent poll: %v", err)
			}
		}()
	}
	polls.Wait()
	if len(sender.messages) != 2 {
		t.Fatalf("concurrent polls duplicated mirror delivery: %+v", sender.messages)
	}

	second := model.AuditLogEntry{GuildID: moderator.Guild.ID, Source: model.AuditSourceSystem, Action: string(model.AuditActionCaseVoid), ResourceType: "case", ResourceID: "case-2", Result: model.AuditResultSuccess, MetadataJSON: "{}"}
	if err := repository.CreateAuditLogEntry(ctx, &second); err != nil {
		t.Fatal(err)
	}
	sender.err = quack.ErrAuditMirrorChannelUnavailable
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	repaired, err := repository.GetGuildSettings(ctx, moderator.Guild.ID)
	if err != nil || repaired.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("expected inaccessible mirror channel to be cleared, settings=%+v err=%v", repaired, err)
	}
	failures, _ := repository.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: string(model.AuditActionMirrorFailed), Limit: 10})
	repairs, _ := repository.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: string(model.AuditActionMirrorRepaired), Limit: 10})
	if failures.Total != 0 || repairs.Total != 0 {
		t.Fatalf("mirror bookkeeping leaked into staff history, failures=%+v repairs=%+v", failures, repairs)
	}
}

func TestStaffStatisticsAreGuildScopedDerivedAndUnranked(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	moderator := templateGuildContext(t, repository, "stats-guild", "moderator", uint64(discordgo.PermissionModerateMembers))
	other := templateGuildContext(t, repository, "other-stats-guild", "other", uint64(discordgo.PermissionModerateMembers))
	now := time.Now().UTC()
	baselineAudits, err := repository.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, CreatedAfter: now.Add(-24 * time.Hour).Format(time.RFC3339Nano), CreatedBefore: now.Add(time.Hour).Format(time.RFC3339Nano), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	templateID := "01J00000000000000000000001"
	caseID := "01J00000000000000000000002"
	otherCaseID := "01J00000000000000000000003"
	rows := []model.Case{
		{ULIDModel: model.ULIDModel{ID: caseID, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: moderator.Guild.ID, CaseNumber: 1, TemplateID: &templateID, TargetDiscordUserID: "member", ModeratorDiscordUserID: "moderator", Reason: "Rule", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, MetadataJSON: "{}", ContextValuesJSON: "{}", TemplateSnapshotJSON: "{}"},
		{ULIDModel: model.ULIDModel{ID: otherCaseID, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: other.Guild.ID, CaseNumber: 1, TargetDiscordUserID: "member", ModeratorDiscordUserID: "other", Reason: "Other", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, MetadataJSON: "{}", ContextValuesJSON: "{}", TemplateSnapshotJSON: "{}"},
	}
	for i := range rows {
		if err := repository.DB().Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	action := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "01J00000000000000000000004", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, CaseID: caseID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, IdempotencyKey: "stats-action", ConfigSnapshotJSON: "{}"}
	appealCaseID := caseID
	appeal := model.Appeal{ULIDModel: model.ULIDModel{ID: "01J00000000000000000000005", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: moderator.Guild.ID, CaseID: &appealCaseID, TargetDiscordUserID: "member", Status: model.AppealStatusAccepted, MetadataJSON: "{}"}
	if err := repository.DB().Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Create(&appeal).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAuditLogEntry(ctx, &model.AuditLogEntry{GuildID: moderator.Guild.ID, Source: model.AuditSourceDiscord, Action: string(model.AuditActionCaseCreate), ResourceType: "case", ResourceID: caseID, Result: model.AuditResultSuccess, MetadataJSON: "{}"}); err != nil {
		t.Fatal(err)
	}

	service := quack.NewStaffStatisticsService(repository)
	result, err := service.Get(ctx, moderator, quack.StatisticsInput{From: now.Add(-24 * time.Hour).Format(time.RFC3339), To: now.Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseTotal != 1 || result.ActionTotal != 1 || result.AppealTotal != 1 || result.AuditTotal != baselineAudits.Total+1 {
		t.Fatalf("statistics crossed guild boundaries or persisted a second truth: %+v", result)
	}
	if len(result.CasesByTemplate) != 1 || result.CasesByTemplate[0].Key != templateID || len(result.ActionsByType) != 1 || result.ActionsByType[0].Key != string(model.ActionTimeoutUser) || len(result.AppealsByStatus) != 1 || result.AppealsByStatus[0].Key != string(model.AppealStatusAccepted) {
		t.Fatalf("missing required breakdowns: %+v", result)
	}
	ordinary := templateGuildContext(t, repository, "stats-guild", "ordinary", uint64(discordgo.PermissionSendMessages))
	if _, err := service.Get(ctx, ordinary, quack.StatisticsInput{}); !errors.Is(err, quack.ErrStatisticsPermissionDenied) {
		t.Fatalf("expected statistics permission denial, got %v", err)
	}
}
