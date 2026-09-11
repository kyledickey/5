package quack_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TestEvidencePageBoundsReadsAndChecksAuthority guards SQL snapshot bounds and
// attachment scope while retaining fresh staff/guild checks for every position.
func TestEvidencePageBoundsReadsAndChecksAuthority(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	staff := templateGuildContext(t, repository, "guild", "mod", uint64(discordgo.PermissionModerateMembers))
	denied := templateGuildContext(t, repository, "guild", "ordinary", 0)
	other := templateGuildContext(t, repository, "other", "mod", uint64(discordgo.PermissionModerateMembers))
	item := model.Case{ULIDModel: model.ULIDModel{ID: "paged-case"}, GuildID: staff.Guild.ID, CaseNumber: 9, Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, TemplateSnapshotJSON: "{}", MetadataJSON: "{}", ContextValuesJSON: "[]"}
	if err := repository.DB().Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var snapshots []model.CaseEvidenceSnapshot
	var attachments []model.CaseEvidenceAttachment
	for i := 1; i <= 50; i++ {
		id := fmt.Sprintf("snapshot-%03d", i)
		snapshots = append(snapshots, model.CaseEvidenceSnapshot{ULIDModel: model.ULIDModel{ID: id, CreatedAt: now}, CaseID: item.ID, GuildID: item.GuildID, Content: id, EmbedsJSON: "[]"})
		attachments = append(attachments, model.CaseEvidenceAttachment{ULIDModel: model.ULIDModel{ID: "file-" + id}, EvidenceID: id, Filename: id + ".png"})
	}
	if err := repository.DB().Create(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Create(&attachments).Error; err != nil {
		t.Fatal(err)
	}
	reads := 0
	if err := repository.DB().Callback().Query().Before("gorm:query").Register("guard_paged_evidence", func(tx *gorm.DB) {
		switch tx.Statement.Table {
		case "case_events", "case_action_executions", "case_action_attempts", "case_notifications":
			tx.AddError(errors.New("unrelated history read"))
		case "case_evidence_snapshots":
			reads++
			if _, count := tx.Statement.Dest.(*int64); count {
				return
			}
			limit, ok := tx.Statement.Clauses["LIMIT"].Expression.(clause.Limit)
			if !ok || limit.Limit == nil || *limit.Limit != 1 {
				tx.AddError(errors.New("unbounded evidence snapshot read"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Callback().Query().After("gorm:query").Register("guard_evidence_attachment_scope", func(tx *gorm.DB) {
		if tx.Statement.Table == "case_evidence_attachments" && tx.RowsAffected > 1 {
			tx.AddError(errors.New("loaded unrelated snapshot attachments"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	service := quack.NewCaseService(repository, nil)
	for _, position := range []int{1, 25, 50, 1000} {
		page, err := service.GetEvidencePage(ctx, staff, item.ID, position)
		want := position
		if want > 50 {
			want = 50
		}
		if err != nil || page.Total != 50 || page.Position != want || len(page.Evidence) != 1 || page.Evidence[0].ID != fmt.Sprintf("snapshot-%03d", want) || len(page.Evidence[0].Attachments) != 1 {
			t.Fatal(page, err)
		}
	}
	before := reads
	if _, err := service.GetEvidencePage(ctx, denied, item.ID, 25); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatal(err)
	}
	if _, err := service.GetEvidencePage(ctx, other, item.ID, 25); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatal(err)
	}
	if reads != before {
		t.Fatal("read evidence before authorization")
	}
}
