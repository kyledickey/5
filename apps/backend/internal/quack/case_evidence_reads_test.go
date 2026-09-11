package quack_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
)

// TestEvidenceViewReadsOnlyAuthorizedEvidence preserves exact evidence projection
// and case-reference checks while making unrelated staff-history queries fail.
func TestEvidenceViewReadsOnlyAuthorizedEvidence(t *testing.T) {
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
	service := quack.NewCaseService(repository, nil)
	full, err := service.Get(ctx, staff, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	blocked := errors.New("unrelated detail query")
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("evidence_only_read", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_events", "case_action_executions", "case_action_attempts", "case_notifications":
			tx.AddError(blocked)
		}
	}); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{item.ID, " 17 "} {
		detail, err := service.GetEvidenceView(ctx, staff, ref)
		if err != nil || detail.ID != full.ID || detail.CaseNumber != full.CaseNumber || !reflect.DeepEqual(detail.Evidence, full.Evidence) {
			t.Fatal("evidence projection changed", detail, err)
		}
	}
	if _, err := service.GetEvidenceView(ctx, other, item.ID); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatal("crossguild access", err)
	}
	if _, err := service.GetEvidenceView(ctx, denied, item.ID); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatal("staff authority missing", err)
	}
	if _, err := service.GetEvidenceView(ctx, staff, " "); err == nil {
		t.Fatal("empty reference accepted")
	}
	if _, err := service.GetEvidenceView(ctx, staff, "missing"); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatal("missing case changed", err)
	}
	if _, err := service.Get(ctx, staff, item.ID); !errors.Is(err, blocked) {
		t.Fatal("full API detail unexpectedly stopped reading history", err)
	}
}
