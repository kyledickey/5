package moduleintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestTicketEntryReusesPanel limits recreation to an explicit missing message;
// permission failures must not turn a setup retry into duplicate panels.
func TestTicketEntryReusesPanel(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		sends  int
		fail   bool
	}{
		{"existing", 200, `{"id":"panel"}`, 0, false},
		{"deleted", 404, `{"code":10008,"message":"Unknown Message"}`, 1, false},
		{"forbidden", 403, `{"code":50013,"message":"Missing Permissions"}`, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			edits, sends := 0, 0
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				status, body := test.status, test.body
				switch r.Method {
				case http.MethodPatch:
					edits++
				case http.MethodPost:
					sends++
					status = 200
					body = `{"id":"replacement"}`
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			_, err = (ticketDiscordClient{session: session}).publishTicketEntry(context.Background(), tickets.Settings{EntryChannelDiscordID: "entry", EntryPanelChannelID: "entry", EntryPanelMessageID: "panel"})
			if (err != nil) != test.fail || edits != 1 || sends != test.sends {
				t.Fatalf("edits=%d sends=%d error=%v", edits, sends, err)
			}
		})
	}
}

// TestTicketEntryMoveRetiresOldPanelBeforePublishing preserves a retryable old
// reference on retirement failure and verifies a moved panel has no active controls.
func TestTicketEntryMoveRetiresOldPanelBeforePublishing(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		wantSend bool
	}{
		{"retired", 200, `{"id":"old-panel"}`, true},
		{"message deleted", 404, `{"code":10008,"message":"Unknown Message"}`, true},
		{"channel deleted", 404, `{"code":10003,"message":"Unknown Channel"}`, true},
		{"permission lost", 403, `{"code":50013,"message":"Missing Permissions"}`, false},
		{"unavailable", 503, `{"message":"Unavailable"}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			var requests []string
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				status, body := scenario.status, scenario.body
				switch r.Method {
				case http.MethodPatch:
					if !strings.HasSuffix(r.URL.Path, "/channels/old-entry/messages/old-panel") {
						t.Fatal("edited the wrong panel")
					}
					var payload struct {
						Content    string            `json:"content"`
						Components []json.RawMessage `json:"components"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(payload.Content, "<#new-entry>") || payload.Components == nil || len(payload.Components) != 0 {
						t.Fatal("retired panel must redirect and clear controls")
					}
				case http.MethodPost:
					if len(requests) != 2 || !strings.HasSuffix(r.URL.Path, "/channels/new-entry/messages") {
						t.Fatal("published before retiring old panel")
					}
					status, body = 200, `{"id":"new-panel"}`
				default:
					t.Fatalf("unexpected request: %s", r.Method)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			_, err := (ticketDiscordClient{session: session}).publishTicketEntry(context.Background(), tickets.Settings{EntryChannelDiscordID: "new-entry", EntryPanelChannelID: "old-entry", EntryPanelMessageID: "old-panel"})
			if (err == nil) != scenario.wantSend || (len(requests) == 2) != scenario.wantSend {
				t.Fatalf("requests=%v err=%v", requests, err)
			}
		})
	}
}

// TestTicketSetupUsesLiveChannelPermissions verifies overwrite denials are caught
// even when gateway state still grants the bot administrator permission.
func TestTicketSetupUsesLiveChannelPermissions(t *testing.T) {
	all := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionCreatePrivateThreads | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionManageThreads | discordgo.PermissionAttachFiles)
	for _, test := range []struct {
		name, channel, want string
		deny                int64
	}{
		{name: "ready"},
		{name: "cannot close threads", channel: "entry", deny: discordgo.PermissionManageThreads, want: "Manage Threads"},
		{name: "cannot retain transcript", channel: "queue", deny: discordgo.PermissionAttachFiles, want: "Attach Files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.State.User = &discordgo.User{ID: "bot"}
			if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{{ID: "guild", Permissions: discordgo.PermissionAdministrator}}}); err != nil {
				t.Fatal(err)
			}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("preflight mutated Discord: %s", r.Method)
				}
				body := ""
				switch {
				case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					body = fmt.Sprintf(`{"id":"guild","owner_id":"owner","roles":[{"id":"guild","permissions":"%d"}]}`, all)
				case strings.HasSuffix(r.URL.Path, "/members/bot"):
					body = `{"user":{"id":"bot"},"roles":[]}`
				case strings.Contains(r.URL.Path, "/channels/"):
					parts := strings.Split(r.URL.Path, "/")
					id := parts[len(parts)-1]
					deny := int64(0)
					if id == test.channel {
						deny = test.deny
					}
					body = fmt.Sprintf(`{"id":"%s","guild_id":"guild","type":0,"permission_overwrites":[{"id":"bot","type":1,"deny":"%d","allow":"0"}]}`, id, deny)
				default:
					t.Fatalf("unexpected request %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			err = (ticketDiscordClient{session: session}).validateTicketBotPermissions(context.Background(), "guild", "entry", "queue")
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "<#"+test.channel+">") {
				t.Fatalf("wrong permission guidance: %v", err)
			}
		})
	}
}

// TestTicketTranscriptKeepsStableOrderAndAttachmentContext covers overlapping
// pages and equal timestamps, while retaining readable author attribution.
func TestTicketTranscriptKeepsStableOrderAndAttachmentContext(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	calls := 0
	stamp := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		var page []*discordgo.Message
		if calls == 1 {
			for id := 200; id > 100; id-- {
				page = append(page, &discordgo.Message{ID: fmt.Sprint(id), Timestamp: stamp, Content: fmt.Sprintf("message-%d", id), Author: &discordgo.User{ID: "member", Username: "Member"}})
			}
		} else {
			if r.URL.Query().Get("before") != "101" {
				t.Fatalf("incorrect history cursor: %s", r.URL.RawQuery)
			}
			page = []*discordgo.Message{{ID: "101", Timestamp: stamp, Content: "message-101", Author: &discordgo.User{ID: "member", Username: "Member"}}, {ID: "100", Timestamp: stamp, Content: "first", Attachments: []*discordgo.MessageAttachment{nil, {Filename: "proof.png", Size: 42, URL: "https://cdn.discordapp.com/attachments/file"}}}}
		}
		body, _ := json.Marshal(page)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	transcript, err := (ticketDiscordClient{session: session}).CaptureTicketTranscript(context.Background(), "thread")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Count(transcript, "message-101") != 1 || strings.Index(transcript, "first") > strings.Index(transcript, "message-101") || !strings.Contains(transcript, "Member (member)") || !strings.Contains(transcript, "original attachment URL (may expire)") {
		t.Fatalf("incorrect transcript: %s", transcript)
	}
}

// TestTicketTranscriptRejectsRepeatedPage ensures closure cannot silently loop
// forever when Discord fails to advance its history cursor.
func TestTicketTranscriptRejectsRepeatedPage(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	calls := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("pagination did not terminate")
		}
		page := make([]*discordgo.Message, 100)
		for i := range page {
			page[i] = &discordgo.Message{ID: fmt.Sprint(200 - i)}
		}
		body, _ := json.Marshal(page)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	if _, err := (ticketDiscordClient{session: session}).CaptureTicketTranscript(context.Background(), "thread"); err == nil {
		t.Fatal("repeated history page accepted")
	}
}

// TestDeletedTicketChannelGatewayLookup verifies the service lookup still forwards
// resolved threads to the adapter and ignores unrelated channels and guilds.
func TestDeletedTicketChannelGatewayLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "gateway.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(modules.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(tickets.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: id}, DiscordGuildID: "discord-" + id, Name: id, IsActive: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(db), nil)
	ctx := context.Background()
	actor := tickets.Actor{GuildID: "one", DiscordUserID: "owner", CanManage: true}
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = "entry", "queue"
	if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
		t.Fatal(err)
	}
	ticket, err := service.Open(ctx, actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resolve(ctx, actor, ticket.ID, "transcript"); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{db: db, Tickets: service, TicketDiscord: tickets.NewDiscordAdapter(service, nil)}
	for _, event := range []struct{ guild, channel string }{
		{"discord-two", "thread"}, {"discord-one", "unrelated"}, {"discord-one", "thread"},
	} {
		runtime.onChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{GuildID: event.guild, ID: event.channel}})
	}
	current, events, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	missing := 0
	for _, event := range events {
		if event.Type == tickets.EventChannelMissing {
			missing++
		}
	}
	if missing != 1 || current.Status != tickets.StatusResolved {
		t.Fatalf("deleted-thread repair changed: missing events=%d status=%s", missing, current.Status)
	}
}
