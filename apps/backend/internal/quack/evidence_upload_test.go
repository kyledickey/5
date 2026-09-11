package quack_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestStaffUploadsPreserveFilesBeforeCreationAndWithoutRepeatingActions covers
// both attachment entry points using the same moderated case and storage channel.
func TestStaffUploadsPreserveFilesBeforeCreationAndWithoutRepeatingActions(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	admin := templateGuildContext(t, repository, "guild", "admin", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repository, "guild", "mod", uint64(discordgo.PermissionModerateMembers))
	if err := repository.DB().Create(&model.GuildSettings{GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "storage"}).Error; err != nil {
		t.Fatal(err)
	}
	input := validTemplateInput("screenshots")
	input.Levels = []quack.TemplateLevelInput{{Name: "Ban", Position: 1, IsDefault: true, Actions: []quack.TemplateActionInput{{ActionType: model.ActionBanUser}}}}
	template := createAppTemplate(t, ctx, repository, admin, input)
	client := &fakeEvidenceClient{preserved: quack.PreservedDiscordAttachment{URL: "https://discord.com/channels/guild/storage/copy", MessageID: "copy", AttachmentID: "saved-file"}}
	service := quack.NewCaseService(repository, nil).WithEvidenceCapture(quack.NewEvidenceService(client, repository))
	file := quack.DiscordAttachmentSnapshot{ID: "file", Filename: "screenshot.png", ContentType: "image/png", SizeBytes: 100, URL: "https://cdn.discordapp.com/attachments/channel/file/screenshot.png"}
	created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "member", Attachments: []quack.DiscordAttachmentSnapshot{file}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := repository.GetCaseByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := service.AddEvidence(ctx, moderator, created.ID, nil, []quack.DiscordAttachmentSnapshot{file})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Evidence) != 2 || len(detail.Actions) != 1 || detail.Actions[0].ID != created.Actions[0].ID {
		t.Fatalf("upload changed moderation: %+v", detail)
	}
	for _, evidence := range detail.Evidence {
		if evidence.CaptureOutcome != "uploaded" || evidence.AuthorDiscordUserID != "mod" || len(evidence.Attachments) != 1 || evidence.Attachments[0].CopyOutcome != "preserved" {
			t.Fatalf("upload was not preserved with staff attribution: %+v", evidence)
		}
	}
	after, err := repository.GetCaseByID(ctx, created.ID)
	if err != nil || before.TemplateSnapshotJSON != after.TemplateSnapshotJSON {
		t.Fatalf("upload changed rule: %v", err)
	}
	ordinary := templateGuildContext(t, repository, "guild", "ordinary", 0)
	if _, err := service.AddEvidence(ctx, ordinary, created.ID, nil, []quack.DiscordAttachmentSnapshot{file}); !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("ordinary member uploaded evidence: %v", err)
	}
	file.SizeBytes = quack.MaxPreservedAttachmentBytes + 1
	detail, err = service.AddEvidence(ctx, moderator, created.ID, nil, []quack.DiscordAttachmentSnapshot{file})
	if err != nil || !detail.EvidenceIncomplete || len(detail.Actions) != 1 {
		t.Fatalf("failed preservation was not recorded separately: %+v err=%v", detail, err)
	}
}
