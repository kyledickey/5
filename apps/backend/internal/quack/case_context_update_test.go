package quack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestContextEditLeavesModerationUntouched proves editing and clearing context
// never creates another case, changes its policy snapshot, or queues punishment.
func TestContextEditLeavesModerationUntouched(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	admin := templateGuildContext(t, repository, "guild", "admin", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repository, "guild", "mod", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("spam")
	input.Levels = []quack.TemplateLevelInput{{Name: "Ban", Position: 1, IsDefault: true, Actions: []quack.TemplateActionInput{{ActionType: model.ActionBanUser}}}}
	template := createAppTemplate(t, ctx, repository, admin, input)
	service := quack.NewCaseService(repository)
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	before, err := repository.GetCaseByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Staff-only incident details", "Fixed a typo", ""} {
		detail, err := service.UpdateContext(ctx, moderator, created.ID, text)
		if err != nil {
			t.Fatal(err)
		}
		if text != "" && (len(detail.ContextValues) != 1 || detail.ContextValues[0].Value != text) {
			t.Fatalf("context not updated: %+v", detail.ContextValues)
		}
		if text == "" && len(detail.ContextValues) != 0 {
			t.Fatal("context not cleared")
		}
		after, err := repository.GetCaseByID(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if after.TemplateSnapshotJSON != before.TemplateSnapshotJSON || after.Reason != before.Reason || after.Validity != before.Validity || len(detail.Actions) != len(created.Actions) {
			t.Fatal("context edit changed the moderation decision")
		}
	}
	audits, err := repository.ListAuditLogEntriesFiltered(ctx, model.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: "case.update"})
	if err != nil || audits.Total != 3 {
		t.Fatalf("missing context attribution: %+v err=%v", audits, err)
	}
	for _, event := range audits.Entries {
		if event.ActorDiscordUserID != "mod" || event.ResourceID != created.ID {
			t.Fatalf("wrong attribution: %+v", event)
		}
	}
	ordinary := templateGuildContext(t, repository, "guild", "member", 0)
	if _, err := service.UpdateContext(ctx, ordinary, created.ID, "unauthorized"); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("ordinary member edited context: %v", err)
	}
	otherGuild := templateGuildContext(t, repository, "other-guild", "mod", uint64(discordgo.PermissionModerateMembers))
	if _, err := service.UpdateContext(ctx, otherGuild, created.ID, "cross-guild"); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("cross-guild edit: %v", err)
	}
}
