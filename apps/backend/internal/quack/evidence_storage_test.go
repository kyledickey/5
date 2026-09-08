package quack_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// storageEvidenceClient exposes channel creation interleavings while recording
// the actual destination selected for attachment preservation.
type storageEvidenceClient struct {
	fakeEvidenceClient
	ensure   func(string) (string, error)
	copiedTo []string
}

// EnsureEvidenceChannel models creation or administrator-controlled existing storage.
func (f *storageEvidenceClient) EnsureEvidenceChannel(_ context.Context, _ string, current string) (string, error) {
	return f.ensure(current)
}

// PreserveEvidenceAttachment records the winning storage channel used by capture.
func (f *storageEvidenceClient) PreserveEvidenceAttachment(_ context.Context, _ string, channel string, _ quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	f.copiedTo = append(f.copiedTo, channel)
	return &quack.PreservedDiscordAttachment{URL: "https://discord.com/channels/guild/" + channel + "/copy", MessageID: "copy", AttachmentID: "file"}, nil
}

// TestEvidenceRepairPreservesConcurrentSettingsAndChannelChoice verifies repair
// only updates its own receipt and attachment capture uses a competing winner.
func TestEvidenceRepairPreservesConcurrentSettingsAndChannelChoice(t *testing.T) {
	for _, changeChannel := range []bool{false, true} {
		t.Run(map[bool]string{false: "settings", true: "channel"}[changeChannel], func(t *testing.T) {
			ctx := context.Background()
			repository := newMigratedStore(t)
			admin := templateGuildContext(t, repository, "guild", "admin", uint64(discordgo.PermissionManageGuild))
			original := model.GuildSettings{ULIDModel: model.ULIDModel{ID: "settings"}, GuildID: admin.Guild.ID, ManagedEvidenceChannelDiscordID: "old", NotificationFooter: "before"}
			if err := repository.DB().Create(&original).Error; err != nil {
				t.Fatal(err)
			}
			client := &storageEvidenceClient{}
			client.ensure = func(current string) (string, error) {
				if current != "old" {
					t.Fatal(current)
				}
				latest, err := repository.GetGuildSettings(ctx, admin.Guild.ID)
				if err != nil {
					t.Fatal(err)
				}
				latest.NotificationFooter = "administrator edit"
				latest.AppealRejoinURL = "https://discord.gg/new"
				if changeChannel {
					latest.ManagedEvidenceChannelDiscordID = "chosen"
				}
				if _, err := repository.UpdateGuildSettings(ctx, model.UpdateGuildSettingsParams{Settings: *latest}); err != nil {
					t.Fatal(err)
				}
				return "created", nil
			}
			evidence := quack.NewEvidenceService(client, repository)
			captured, err := evidence.CaptureUploads(ctx, "guild", "admin", "old", []quack.DiscordAttachmentSnapshot{{Filename: "proof.png", ContentType: "image/png", SizeBytes: 1}})
			if err != nil || len(captured.Attachments) != 1 || captured.Attachments[0].CopyOutcome != "preserved" {
				t.Fatal(captured, err)
			}
			latest, err := repository.GetGuildSettings(ctx, admin.Guild.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "created"
			if changeChannel {
				want = "chosen"
			}
			if latest.ManagedEvidenceChannelDiscordID != want || latest.NotificationFooter != "administrator edit" || latest.AppealRejoinURL != "https://discord.gg/new" || len(client.copiedTo) != 1 || client.copiedTo[0] != want {
				t.Fatal(latest, client.copiedTo)
			}
		})
	}
}

// TestEvidenceCaptureRepairsAfterFailedRecreation preserves metadata on failure
// and retries storage discovery on the next upload without blocking evidence use.
func TestEvidenceCaptureRepairsAfterFailedRecreation(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	admin := templateGuildContext(t, repository, "guild", "admin", uint64(discordgo.PermissionManageGuild))
	if err := repository.DB().Create(&model.GuildSettings{ULIDModel: model.ULIDModel{ID: "settings"}, GuildID: admin.Guild.ID}).Error; err != nil {
		t.Fatal(err)
	}
	failed := true
	client := &storageEvidenceClient{ensure: func(string) (string, error) {
		if failed {
			return "", errors.New("temporary Discord failure")
		}
		return "recreated", nil
	}}
	evidence := quack.NewEvidenceService(client, repository)
	if _, err := evidence.RepairDiscordGuildEvidenceChannel(ctx, "guild"); err == nil {
		t.Fatal("expected failed gateway repair")
	}
	files := []quack.DiscordAttachmentSnapshot{{Filename: "proof.png", ContentType: "image/png", SizeBytes: 1}}
	captured, err := evidence.CaptureUploads(ctx, "guild", "admin", "", files)
	if err != nil || len(captured.Warnings) == 0 || captured.Attachments[0].CopyOutcome != "metadata_only" {
		t.Fatal(captured, err)
	}
	failed = false
	captured, err = evidence.CaptureUploads(ctx, "guild", "admin", "", files)
	if err != nil || captured.Attachments[0].CopyOutcome != "preserved" || client.copiedTo[0] != "recreated" {
		t.Fatal(captured, err)
	}
}

// TestOverlappingEvidenceRepairsConverge reloads storage after waiting for the
// first creator, avoiding duplicate creation in the single service lifecycle.
func TestOverlappingEvidenceRepairsConverge(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	admin := templateGuildContext(t, repository, "guild", "admin", uint64(discordgo.PermissionManageGuild))
	original := model.GuildSettings{ULIDModel: model.ULIDModel{ID: "settings"}, GuildID: admin.Guild.ID}
	if err := repository.DB().Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	creates := 0
	client := &storageEvidenceClient{ensure: func(current string) (string, error) {
		if current != "" {
			return current, nil
		}
		creates++
		close(entered)
		<-release
		return "created", nil
	}}
	evidence := quack.NewEvidenceService(client, repository)
	results := make(chan string, 2)
	var wg sync.WaitGroup
	run := func() {
		defer wg.Done()
		id, err := evidence.EnsureGuildEvidenceChannel(ctx, *admin.Guild, original)
		if err != nil {
			results <- err.Error()
		} else {
			results <- id
		}
	}
	wg.Add(1)
	go run()
	<-entered
	wg.Add(1)
	go run()
	close(release)
	wg.Wait()
	if creates != 1 || <-results != "created" || <-results != "created" {
		t.Fatal("overlapping repair did not converge", creates)
	}
}
