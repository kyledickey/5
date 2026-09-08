package moduleintegration

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
)

// gatewayLogRecorder captures queued structured payloads without live Discord.
type gatewayLogRecorder struct {
	mu       sync.Mutex
	payloads []string
}

// ValidateStaffOnlyChannel accepts the isolated fixture destination.
func (*gatewayLogRecorder) ValidateStaffOnlyChannel(context.Context, string, string) error {
	return nil
}

// SendStaffLog records deliveries; queue drain synchronizes assertions.
func (r *gatewayLogRecorder) SendStaffLog(_ context.Context, _, _ string, payload string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payloads = append(r.payloads, payload)
	return nil
}

// TestLoggingGatewayRetainsBotAuthorsAndDeletionContext covers bot creates,
// partial-author edits, and deletes through the real gateway projection and queue.
func TestLoggingGatewayRetainsBotAuthorsAndDeletionContext(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	// A :memory: database belongs to one connection. The delivery worker and
	// gateway projection must share it rather than open an empty second database.
	sqlDB, err := repository.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	guild, err := repository.UpsertGuild(ctx, model.UpsertGuildParams{DiscordGuildID: "guild", Name: "Guild"})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(repository.DB()), generallogging.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	recorder := &gatewayLogRecorder{}
	service := generallogging.NewService(registry, nil, recorder, nil)
	if _, err := service.UpdateSettings(ctx, generallogging.Actor{GuildID: guild.ID, CanManage: true}, true, generallogging.Defaults().RouteAllTo("logs")); err != nil {
		t.Fatal(err)
	}
	queue := generallogging.NewDeliveryQueue(ctx, service, 10, 1)
	runtime := &Runtime{db: repository.DB(), Logging: service, LoggingQueue: queue}
	original := &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "source", Author: &discordgo.User{ID: "other-bot", Bot: true}, Content: "before", Attachments: []*discordgo.MessageAttachment{{ID: "attachment", Filename: "proof.png"}}}
	runtime.onMessageCreate(nil, &discordgo.MessageCreate{Message: original})
	edited := time.Now().UTC()
	runtime.onMessageUpdate(nil, &discordgo.MessageUpdate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "source", Content: "after", EditedTimestamp: &edited, Attachments: original.Attachments}})
	runtime.onMessageDelete(nil, &discordgo.MessageDelete{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "source"}})
	queue.Close()
	if len(recorder.payloads) != 2 {
		t.Fatalf("missing logs: %+v", recorder.payloads)
	}
	for index, payload := range recorder.payloads {
		var event struct {
			Actor       string                              `json:"actor_id"`
			Before      string                              `json:"before"`
			BeforeKnown bool                                `json:"before_known"`
			Attachments []generallogging.AttachmentMetadata `json:"attachments"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatal(err)
		}
		if event.Actor != "other-bot" || len(event.Attachments) != 1 || event.Attachments[0].Filename != "proof.png" {
			t.Fatalf("author or files lost: %s", payload)
		}
		if index == 0 && (event.Before != "before" || !event.BeforeKnown) {
			t.Fatalf("bot edit cache omitted: %s", payload)
		}
		if index == 1 && event.Before != "after" {
			t.Fatalf("delete cache omitted: %s", payload)
		}
	}
}
