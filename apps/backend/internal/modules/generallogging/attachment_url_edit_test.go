package generallogging_test

import (
	"context"
	"strings"
	"testing"

	logmodule "github.com/quackdiscord/bot/internal/modules/generallogging"
)

// TestSignedAttachmentURLRefreshIsNotAnEdit retains current download URLs for
// deletion logging without mistaking URL expiry rotation for member activity.
func TestSignedAttachmentURLRefreshIsNotAnEdit(t *testing.T) {
	_, service, client, _ := setup(t)
	ctx := context.Background()
	before := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message", Content: "unchanged", Attachments: []logmodule.AttachmentMetadata{{DiscordID: "file", Filename: "proof.png", ContentType: "image/png", Size: 10, URL: "https://cdn.discordapp.com/proof.png?sig=old"}}}
	if err := service.CacheMessage(ctx, before); err != nil {
		t.Fatal(err)
	}
	current := before
	current.Attachments = append([]logmodule.AttachmentMetadata(nil), before.Attachments...)
	current.Attachments[0].URL = "https://cdn.discordapp.com/proof.png?sig=new"
	event, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || event != nil {
		t.Fatalf("URL rotation produced edit: %+v %v", event, err)
	}
	if err := service.Handle(ctx, logmodule.Event{GuildID: "guild-a", MessageDiscordID: "message", Type: logmodule.MessageDelete}); err != nil {
		t.Fatal(err)
	}
	if len(client.payloads) != 1 || !strings.Contains(client.payloads[0], "sig=new") || strings.Contains(client.payloads[0], "sig=old") {
		t.Fatalf("latest URL not cached: %v", client.payloads)
	}
}

// TestAttachmentSemanticChangesStillGenerateEdits preserves the previous
// attachment comparison for identity, filename, content type and byte length.
func TestAttachmentSemanticChangesStillGenerateEdits(t *testing.T) {
	for _, field := range []string{"id", "name", "type", "size", "removed"} {
		t.Run(field, func(t *testing.T) {
			_, service, _, _ := setup(t)
			before := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message", Attachments: []logmodule.AttachmentMetadata{{DiscordID: "file", Filename: "proof.png", ContentType: "image/png", Size: 10, URL: "https://cdn.discordapp.com/old"}}}
			if err := service.CacheMessage(context.Background(), before); err != nil {
				t.Fatal(err)
			}
			current := before
			current.Attachments = append([]logmodule.AttachmentMetadata(nil), before.Attachments...)
			switch field {
			case "id":
				current.Attachments[0].DiscordID = "replacement"
			case "name":
				current.Attachments[0].Filename = "renamed.png"
			case "type":
				current.Attachments[0].ContentType = "image/jpeg"
			case "size":
				current.Attachments[0].Size++
			case "removed":
				current.Attachments = nil
			}
			event, err := service.PrepareMessageEdit(context.Background(), current, &before)
			if err != nil || event == nil {
				t.Fatalf("%s change missed: %+v %v", field, event, err)
			}
		})
	}
}
