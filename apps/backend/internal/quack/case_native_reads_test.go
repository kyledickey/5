package quack_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"gorm.io/gorm/clause"
	"reflect"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestNativeDetailBoundsHistoryPreservesOutput checks identical native output and
// recovery controls, bounded event reads, skipped attempts, and guild authority.
func TestNativeDetailBoundsHistoryPreservesOutput(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	staff := templateGuildContext(t, repository, "guild", "mod", uint64(discordgo.PermissionModerateMembers))
	other := templateGuildContext(t, repository, "other-guild", "mod", uint64(discordgo.PermissionModerateMembers))
	denied := templateGuildContext(t, repository, "guild", "ordinary", 0)
	item := model.Case{ULIDModel: model.ULIDModel{ID: "evidence-view-case"}, GuildID: staff.Guild.ID, CaseNumber: 17, Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
	if err := repository.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: "snapshot"}, CaseID: item.ID, GuildID: item.GuildID, Content: "retained original", CaptureWarning: "copy incomplete", EmbedsJSON: "[]"}
	if err := repository.DB().Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	file := model.CaseEvidenceAttachment{ULIDModel: model.ULIDModel{ID: "file"}, EvidenceID: snapshot.ID, Filename: "proof.png", PreservedURL: "https://example.com/proof.png", CopyOutcome: "copied", Warning: "file warning"}
	if err := repository.DB().Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		event := model.CaseEvent{ULIDModel: model.ULIDModel{ID: fmt.Sprintf("event-%02d", i), CreatedAt: time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)}, CaseID: item.ID, GuildID: item.GuildID, EventType: model.CaseEventCreated, Body: fmt.Sprintf("event %d", i)}
		if err := repository.DB().Create(&event).Error; err != nil {
			t.Fatal(err)
		}
	}
	action := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "failed-action"}, CaseID: item.ID, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionFailed, LastErrorCode: "missing_permission", ConfigSnapshotJSON: "{}"}
	if err := repository.DB().Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	attempt := model.CaseActionAttempt{ULIDModel: model.ULIDModel{ID: "attempt"}, ExecutionID: action.ID, AttemptNumber: 1, Status: model.ActionAttemptFailed}
	if err := repository.DB().Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	service := quack.NewCaseService(repository, nil)
	full, err := service.Get(ctx, staff, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Events) != 20 || len(full.Actions[0].Attempts) != 1 {
		t.Fatal("full detail lost history", full)
	}
	blocked := errors.New("unrelated detail query")
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("evidence_only_read", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_action_attempts":
			tx.AddError(blocked)
		case "case_events":
			limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
			if !ok || limit.Limit == nil || *limit.Limit != 6 {
				tx.AddError(blocked)
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{item.ID, " 17 "} {
		detail, err := service.GetNativeDetail(ctx, staff, ref)
		if err != nil || len(detail.Events) != 6 || !reflect.DeepEqual(views.CaseDetailMessage(detail), views.CaseDetailMessage(full)) {
			t.Fatal("evidence projection changed", detail, err)
		}
	}
	if _, err := service.GetNativeDetail(ctx, other, item.ID); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatal("crossguild access", err)
	}
	if _, err := service.GetNativeDetail(ctx, denied, item.ID); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatal("staff authority missing", err)
	}
	if _, err := service.GetNativeDetail(ctx, staff, " "); err == nil {
		t.Fatal("empty reference accepted")
	}
	if _, err := service.GetNativeDetail(ctx, staff, "missing"); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatal("missing case changed", err)
	}
	if _, err := service.Get(ctx, staff, item.ID); !errors.Is(err, blocked) {
		t.Fatal("full API detail unexpectedly stopped reading history", err)
	}
}
