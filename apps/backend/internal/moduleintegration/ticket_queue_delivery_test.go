package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestTicketTranscriptRecreatesDeletedQueueMessage exercises the real service,
// adapter and HTTP transport: a missing edit returns to durable replacement
// admission before a fresh multipart send and source deletion.
func TestTicketTranscriptRecreatesDeletedQueueMessage(t *testing.T) {
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
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := tickets.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(db), nil)
	actor := tickets.Actor{GuildID: "internal", DiscordUserID: "owner", CanManage: true}
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = "entry", "queue"
	if _, err := service.UpdateSettings(context.Background(), actor, true, settings); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Open(context.Background(), actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(context.Background(), actor, ticket.ID, "preserved conversation"); err != nil {
		t.Fatal(err)
	}
	if err := db.Table("tickets").Where("id = ?", ticket.ID).Updates(map[string]any{"log_channel_discord_id": "queue", "log_message_discord_id": "original"}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	writes, deletes := 0, 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch {
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/channels/thread"):
			deletes++
			if writes != 2 {
				t.Fatal("source deleted before replacement")
			}
			body = `{"id":"thread"}`
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/queue"):
			body = fmt.Sprintf(`{"id":"queue","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"%d","allow":"0"}]}`, discordgo.PermissionViewChannel)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			body = `{"id":"guild","roles":[{"id":"bot-role","permissions":"8"}]}`
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/members/bot"):
			body = `{"user":{"id":"bot"},"roles":["bot-role"]}`
		case r.Method == http.MethodPatch || r.Method == http.MethodPost:
			writes++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			var payload struct {
				Components []struct {
					Components []struct {
						CustomID string `json:"custom_id"`
						Label    string `json:"label"`
					} `json:"components"`
				} `json:"components"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload.Components) != 1 || len(payload.Components[0].Components) != 1 || payload.Components[0].Components[0].Label != "View ticket" || !strings.Contains(payload.Components[0].Components[0].CustomID, "view") {
				t.Fatalf("transcript lost recovery control: %+v", payload)
			}
			files := r.MultipartForm.File["files[0]"]
			if len(files) != 1 {
				t.Fatalf("missing transcript: %+v", r.MultipartForm.File)
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
			if string(data) != "preserved conversation" || files[0].Filename != "ticket-"+ticket.ID+".txt" {
				t.Fatalf("wrong transcript: %q %q", files[0].Filename, data)
			}
			if writes == 1 {
				if r.Method != http.MethodPatch {
					t.Fatal("did not update original queue message")
				}
				status, body = http.StatusNotFound, `{"code":10008,"message":"Unknown Message"}`
			} else {
				if r.Method != http.MethodPost {
					t.Fatal("did not recreate deleted message")
				}
				var fence struct{ LogChannelDiscordID, LogMessageDiscordID, TranscriptURL string }
				if err := db.Table("tickets").Where("id = ?", ticket.ID).Find(&fence).Error; err != nil {
					t.Fatal(err)
				}
				if fence.LogChannelDiscordID != "queue" || fence.LogMessageDiscordID != "" || fence.TranscriptURL != "" {
					t.Fatalf("replacement was not fenced: %+v", fence)
				}
				body = `{"id":"replacement","attachments":[{"id":"file","filename":"ticket-ticket.txt"}]}`
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client := ticketDiscordClient{session: session, resolver: guildResolver{db: db}}
	closed, err := tickets.NewDiscordAdapter(service, client).Close(context.Background(), actor, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if writes != 2 || deletes != 1 || closed.TranscriptURL != "https://discord.com/channels/guild/queue/replacement" {
		t.Fatalf("incorrect receipt: %+v writes=%d deletes=%d", closed, writes, deletes)
	}
	if pending, err := service.ClosurePending(context.Background(), actor, ticket.ID); err != nil || pending {
		t.Fatal("closure remained pending", pending, err)
	}
}

// TestTicketQueueFailureClassification prevents network/server uncertainty from
// being mistaken for a safe initial-send retry.
func TestTicketQueueFailureClassification(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502} {
		err := ticketQueueSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, tickets.ErrQueueNotSent) != (status < 500) {
			t.Fatal(status, err)
		}
	}
	if errors.Is(ticketQueueSendError(errors.New("connection lost")), tickets.ErrQueueNotSent) {
		t.Fatal("uncertain send marked safe")
	}
}

// TestTicketQueueReceiptRead distinguishes definite absence from denied or failed
// reads, which must not authorize replacement or source deletion.
func TestTicketQueueReceiptRead(t *testing.T) {
	for _, test := range []struct {
		name, body     string
		status         int
		exists, failed bool
	}{
		{"present", `{"id":"message"}`, 200, true, false},
		{"deleted message", `{"code":10008}`, 404, false, false},
		{"deleted channel", `{"code":10003}`, 404, false, false},
		{"denied", `{"code":50013}`, 403, false, true},
		{"unavailable", `{"message":"unavailable"}`, 503, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/channels/queue/messages/message") {
					t.Fatal("unexpected receipt request", r.Method, r.URL.Path)
				}
				return &http.Response{StatusCode: test.status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			exists, err := (ticketDiscordClient{session: session}).TicketQueueMessageExists(context.Background(), "queue", "message")
			if exists != test.exists || (err != nil) != test.failed {
				t.Fatal(exists, err)
			}
		})
	}
}
