package moduleintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestDeletedAttachmentLinksSurviveGatewayProjection checks both single and bulk
// log representations against actual gateway attachment metadata.
func TestDeletedAttachmentLinksSurviveGatewayProjection(t *testing.T) {
	const link = "https://cdn.discordapp.com/attachments/channel/file/proof.png?ex=123&sig=abc"
	cached := cachedMessage("guild", &discordgo.Message{ID: "message", ChannelID: "channel", Author: &discordgo.User{ID: "member"}, Attachments: []*discordgo.MessageAttachment{{ID: "file", Filename: "proof.png", URL: link}}})
	for _, bulk := range []bool{false, true} {
		payload := map[string]any{"event": "message_delete", "attachments": cached.Attachments}
		if bulk {
			payload = map[string]any{"event": "message_bulk_delete", "messages": []any{map[string]any{"message_id": cached.MessageDiscordID, "actor_id": cached.AuthorDiscordUserID, "attachments": cached.Attachments}}}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		message := views.StaffLogMessage(string(encoded))
		if !strings.Contains(message.Content, "[proof.png](<"+link+">)") {
			t.Fatalf("bulk=%v lost downloadable link: %s", bulk, message.Content)
		}
	}
}

// TestExternalBanLoggingSeparatesActorAndTarget prevents duplicate Quack entries
// without suppressing another moderator's action against the same member.
func TestExternalBanLoggingSeparatesActorAndTarget(t *testing.T) {
	for _, kind := range []discordgo.AuditLogAction{discordgo.AuditLogActionMemberBanAdd, discordgo.AuditLogActionMemberBanRemove} {
		entry := &discordgo.GuildAuditLogEntryCreate{GuildID: "guild", AuditLogEntry: &discordgo.AuditLogEntry{ID: "audit", TargetID: "member", UserID: "quack", ActionType: &kind, Reason: "rule"}}
		if _, ok := externalBanEvent(entry, "quack"); ok {
			t.Fatal("Quack action duplicated")
		}
		entry.UserID = "moderator"
		event, ok := externalBanEvent(entry, "quack")
		if !ok || event.ActorDiscordUserID != "moderator" || event.Metadata["target_id"] != "member" || event.Metadata["reason"] != "rule" {
			t.Fatalf("external action lost attribution: %+v", event)
		}
		if kind == discordgo.AuditLogActionMemberBanAdd && event.Type != generallogging.DiscordBan || kind == discordgo.AuditLogActionMemberBanRemove && event.Type != generallogging.DiscordUnban {
			t.Fatal("wrong event kind")
		}
	}
	if _, ok := externalBanEvent(nil, "quack"); ok {
		t.Fatal("nil audit event accepted")
	}
}

// TestLongLogDeliveryPreservesContentAndChecksAttachments exercises the actual
// multipart Discord request and a current permission denial before that send.
func TestLongLogDeliveryPreservesContentAndChecksAttachments(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, allowFiles := range []bool{true, false} {
		t.Run(fmt.Sprint(allowFiles), func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			sent := false
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch {
				case r.Method == http.MethodPost:
					sent = true
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Fatal(err)
					}
					defer r.MultipartForm.RemoveAll()
					files := r.MultipartForm.File["files[0]"]
					if len(files) != 1 {
						t.Fatal("long event was not attached")
					}
					f, err := files[0].Open()
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					data, err := io.ReadAll(f)
					if err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(string(data), "final-marker") {
						t.Fatal("long event lost its tail")
					}
					body = `{"id":"delivered"}`
				case strings.HasSuffix(r.URL.Path, "/channels/log"):
					permissions := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory | discordgo.PermissionSendMessages)
					if allowFiles {
						permissions |= discordgo.PermissionAttachFiles
					}
					body = fmt.Sprintf(`{"id":"log","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"1024"},{"id":"bot","type":1,"allow":"%d"}]}`, permissions)
				case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					body = `{"id":"guild","roles":[{"id":"guild","permissions":"0"}]}`
				case strings.HasSuffix(r.URL.Path, "/members/bot"):
					body = `{"user":{"id":"bot"},"roles":[]}`
				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			payload, _ := json.Marshal(map[string]string{"event": "message_delete", "before": strings.Repeat("message content ", 300) + "final-marker"})
			err := (loggingDiscordClient{session: session, resolver: guildResolver{db: db}}).SendStaffLog(context.Background(), "internal", "log", string(payload))
			if allowFiles && err != nil || !allowFiles && err == nil || sent != allowFiles {
				t.Fatalf("sent=%v allow=%v error=%v", sent, allowFiles, err)
			}
		})
	}
}

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
