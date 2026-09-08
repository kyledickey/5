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

// contextEvidenceClient counts source fetches and archive uploads and records the
// actor used by the live permission-checking evidence adapter.
type contextEvidenceClient struct {
	fakeEvidenceClient
	fetches  int
	uploads  int
	actor    string
	fetchErr error
}

// FetchMessageEvidence preserves the moderator identity required for live checks.
func (c *contextEvidenceClient) FetchMessageEvidence(ctx context.Context, ref quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	c.fetches++
	c.actor = ref.ActorDiscordUserID
	if c.fetchErr != nil {
		return nil, c.fetchErr
	}
	return c.fakeEvidenceClient.FetchMessageEvidence(ctx, ref)
}

// PreserveEvidenceAttachment counts actual archive work, not merely stored rows.
func (c *contextEvidenceClient) PreserveEvidenceAttachment(ctx context.Context, guildID, channelID string, attachment quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	c.uploads++
	return c.fakeEvidenceClient.PreserveEvidenceAttachment(ctx, guildID, channelID, attachment)
}

// TestContextMessageLinkCaptureIsOptionalAndIdempotent covers the preferred
// create-first, paste-link-later flow without rerunning enforcement or uploads.
func TestContextMessageLinkCaptureIsOptionalAndIdempotent(t *testing.T) {
	const guildID = "111111111111111111"
	const channelID = "222222222222222222"
	const messageID = "333333333333333333"
	const link = "https://discord.com/channels/" + guildID + "/" + channelID + "/" + messageID
	for _, scenario := range []string{"captured", "inaccessible", "wrong-author", "cross-guild"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			repository := newMigratedStore(t)
			admin := templateGuildContext(t, repository, guildID, "admin", uint64(discordgo.PermissionManageGuild))
			moderator := templateGuildContext(t, repository, guildID, "mod", uint64(discordgo.PermissionModerateMembers))
			if err := repository.DB().Create(&model.GuildSettings{GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "storage"}).Error; err != nil {
				t.Fatal(err)
			}
			input := validTemplateInput("context-links")
			input.Levels = []quack.TemplateLevelInput{{Name: "Ban", Position: 1, IsDefault: true, Actions: []quack.TemplateActionInput{{ActionType: model.ActionBanUser}}}}
			template := createAppTemplate(t, ctx, repository, admin, input)
			client := &contextEvidenceClient{fakeEvidenceClient: fakeEvidenceClient{message: quack.DiscordMessageSnapshot{GuildID: guildID, ChannelID: channelID, MessageID: messageID, AuthorDiscordUserID: "member", URL: link, Content: "source text", Attachments: []quack.DiscordAttachmentSnapshot{{ID: "file", Filename: "proof.png", ContentType: "image/png", SizeBytes: 100, URL: "https://cdn.discordapp.com/proof.png"}}}, preserved: quack.PreservedDiscordAttachment{URL: "https://cdn.discordapp.com/saved.png", MessageID: "archive", AttachmentID: "copy"}}}
			if scenario == "inaccessible" {
				client.fetchErr = &quack.EvidenceUnavailableError{Outcome: "inaccessible", Message: "Source channel is unavailable."}
			}
			if scenario == "wrong-author" {
				client.message.AuthorDiscordUserID = "other"
			}
			service := quack.NewCaseService(repository).WithEvidenceCapture(quack.NewEvidenceService(client, repository))
			created, err := service.Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "member"})
			if err != nil {
				t.Fatal(err)
			}
			pasted := link
			if scenario == "cross-guild" {
				pasted = "https://discord.com/channels/999999999999999999/" + channelID + "/" + messageID
			}
			text := "Moderator notes: [message](" + pasted + ") and <" + pasted + "?jump=1>."
			detail, err := service.UpdateContext(ctx, moderator, created.ID, text)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.ContextValues) != 1 || detail.ContextValues[0].Value != text || len(detail.Actions) != 1 || detail.Actions[0].ID != created.Actions[0].ID {
				t.Fatal("capture changed text or moderation")
			}
			if scenario == "captured" {
				if len(detail.Evidence) != 1 || detail.EvidenceIncomplete || client.fetches != 1 || client.uploads != 1 || client.actor != "mod" {
					t.Fatalf("capture did not preserve one message: %+v fetches=%d uploads=%d actor=%s", detail, client.fetches, client.uploads, client.actor)
				}
				// Repeated edits and URL-host aliases must reuse the already archived message.
				text = "Typo fixed: https://canary.discord.com/channels/" + guildID + "/" + channelID + "/" + messageID
				detail, err = service.UpdateContext(ctx, moderator, created.ID, text)
				if err != nil || len(detail.Evidence) != 1 || client.fetches != 1 || client.uploads != 1 {
					t.Fatalf("typo edit recopied evidence: %v fetches=%d uploads=%d", err, client.fetches, client.uploads)
				}
			} else {
				if !detail.EvidenceIncomplete || client.uploads != 0 {
					t.Fatal("failed optional capture not reported safely")
				}
				if scenario == "cross-guild" && client.fetches != 0 {
					t.Fatal("cross-guild source was fetched")
				}
			}
		})
	}
}
