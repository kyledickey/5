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
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestTicketTranscriptEditsReplacePriorAttachments exercises adopted delivery
// and repeated canonical updates through DiscordGo's actual multipart encoding.
// Every PATCH must retain only its new upload, never an older attachment ID.
func TestTicketTranscriptEditsReplacePriorAttachments(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	updates := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/queue"):
			body = fmt.Sprintf(`{"id":"queue","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"%d","allow":"0"}]}`, discordgo.PermissionViewChannel)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			body = `{"id":"guild","roles":[{"id":"bot-role","permissions":"8"}]}`
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/members/bot"):
			body = `{"user":{"id":"bot"},"roles":["bot-role"]}`
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/queue/messages/adopted"):
			updates++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			var payload struct {
				Attachments *[]struct{ ID, Filename string } `json:"attachments"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Attachments == nil || len(*payload.Attachments) != 1 {
				t.Fatalf("PATCH omitted explicit upload-only attachment list: %s", r.FormValue("payload_json"))
			}
			attachment := (*payload.Attachments)[0]
			if attachment.ID != "0" || attachment.Filename != "ticket-ticket.txt" {
				t.Fatalf("retained a prior attachment instead of files[0]: %+v", attachment)
			}
			files := r.MultipartForm.File["files[0]"]
			if len(files) != 1 || len(r.MultipartForm.File) != 1 {
				t.Fatal("expected one canonical upload", r.MultipartForm.File)
			}
			file, err := files[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil || string(content) != "canonical transcript" {
				t.Fatal("incorrect replacement content", string(content), err)
			}
			body = fmt.Sprintf(`{"id":"adopted","attachments":[{"id":"prior-%d","filename":"ticket-ticket.txt"}]}`, updates)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client := ticketDiscordClient{session: session, resolver: guildResolver{db: db}}
	ticket := &tickets.Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "owner", LogChannelDiscordID: "queue", LogMessageDiscordID: "adopted"}
	for range 3 {
		receipt, err := client.PublishTicketQueue(context.Background(), ticket, tickets.Settings{QueueChannelDiscordID: "queue"}, &tickets.Transcript{Content: "canonical transcript"})
		if err != nil || receipt.MessageID != "adopted" {
			t.Fatal(receipt, err)
		}
	}
	if updates != 3 {
		t.Fatal("missing repeated edit", updates)
	}
}

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
	if err := db.AutoMigrate(modules.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(tickets.SchemaTypes()...); err != nil {
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
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/@me/channels"):
			status, body = http.StatusForbidden, `{"code":50007,"message":"Cannot send messages to this user"}`
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
			if len(payload.Components) != 1 || len(payload.Components[0].Components) != 1 || payload.Components[0].Components[0].Label != "Recovery" || !strings.Contains(payload.Components[0].Components[0].CustomID, "view") {
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

// TestValidateTicketQueueMessage exercises real Discord REST decoding and fresh
// identity reads, rejecting lookalikes without sending or changing any message.
func TestValidateTicketQueueMessage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "11", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name          string
		rawURL        string
		mutate        func(*discordgo.Message)
		channelGuild  string
		failurePath   string
		failureStatus int
		wantOK        bool
		wantNoReads   bool
	}{
		{name: "open queue", wantOK: true},
		{name: "closed queue", mutate: func(m *discordgo.Message) {
			m.Components = []discordgo.MessageComponent{ui.Row(ui.Button("ticket:view:v1:ticket-id", "View ticket", discordgo.SecondaryButton, false))}
		}, wantOK: true},
		{name: "wrong link guild", rawURL: "https://discord.com/channels/99/22/33", wantNoReads: true},
		{name: "wrong destination", rawURL: "https://discord.com/channels/11/99/33", wantNoReads: true},
		{name: "lookalike host", rawURL: "https://discord.com.attacker.test/channels/11/22/33", wantNoReads: true},
		{name: "credentials", rawURL: "https://secret@discord.com/channels/11/22/33", wantNoReads: true},
		{name: "query", rawURL: "https://discord.com/channels/11/22/33?secret=value", wantNoReads: true},
		{name: "attachment URL", rawURL: "https://cdn.discordapp.com/attachments/22/33/ticket-ticket-id.txt", wantNoReads: true},
		{name: "wrong returned message", mutate: func(m *discordgo.Message) { m.ID = "99" }},
		{name: "wrong returned channel", mutate: func(m *discordgo.Message) { m.ChannelID = "99" }},
		{name: "wrong returned guild", mutate: func(m *discordgo.Message) { m.GuildID = "99" }},
		{name: "wrong channel guild", channelGuild: "99"},
		{name: "wrong author", mutate: func(m *discordgo.Message) { m.Author.ID = "99" }},
		{name: "missing author", mutate: func(m *discordgo.Message) { m.Author = nil }},
		{name: "webhook", mutate: func(m *discordgo.Message) { m.WebhookID = "99" }},
		{name: "other ticket", mutate: func(m *discordgo.Message) {
			m.Components = []discordgo.MessageComponent{ui.Row(ui.Button("ticket:view:v1:other-ticket", "View", discordgo.SecondaryButton, false))}
		}},
		{name: "mixed ticket controls", mutate: func(m *discordgo.Message) {
			m.Components = append(m.Components, ui.Row(ui.Button("ticket:close:v1:other-ticket", "Close", discordgo.SecondaryButton, false)))
		}},
		{name: "malformed control", mutate: func(m *discordgo.Message) {
			m.Components = append(m.Components, ui.Row(ui.Button("ticket:close", "Close", discordgo.SecondaryButton, false)))
		}},
		{name: "unsupported version", mutate: func(m *discordgo.Message) {
			m.Components = []discordgo.MessageComponent{ui.Row(ui.Button("ticket:view:v2:ticket-id", "View", discordgo.SecondaryButton, false))}
		}},
		{name: "unsupported action", mutate: func(m *discordgo.Message) {
			m.Components = append(m.Components, ui.Row(ui.Button("ticket:repair:v1:ticket-id", "Repair", discordgo.SecondaryButton, false)))
		}},
		{name: "attachment without controls", mutate: func(m *discordgo.Message) {
			m.Components = nil
			m.Attachments = []*discordgo.MessageAttachment{{ID: "file", Filename: "ticket-ticket-id.txt", URL: "https://cdn.discordapp.com/attachments/22/file/transcript.txt"}}
		}},
		{name: "message forbidden", failurePath: "/channels/22/messages/33", failureStatus: http.StatusForbidden},
		{name: "message missing", failurePath: "/channels/22/messages/33", failureStatus: http.StatusNotFound},
		{name: "message network failure", failurePath: "/channels/22/messages/33"},
		{name: "message server failure", failurePath: "/channels/22/messages/33", failureStatus: http.StatusServiceUnavailable},
		{name: "channel forbidden", failurePath: "/channels/22", failureStatus: http.StatusForbidden},
		{name: "identity unavailable", failurePath: "/users/@me", failureStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := &discordgo.Message{ID: "33", ChannelID: "22", Author: &discordgo.User{ID: "44", Bot: true}, Components: []discordgo.MessageComponent{&discordgo.Container{Components: []discordgo.MessageComponent{ui.Row(ui.Button("ticket:view:v1:ticket-id", "Join thread", discordgo.SecondaryButton, false), ui.Button("ticket:close:v1:ticket-id", "Close", discordgo.SecondaryButton, false))}}}}
			if tt.mutate != nil {
				tt.mutate(message)
			}
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.State.User = &discordgo.User{ID: "stale-bot"}
			calls := map[string]int{}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("reconciliation mutated Discord: %s %s", r.Method, r.URL.Path)
				}
				calls[r.URL.Path]++
				if calls[r.URL.Path] > 1 {
					t.Fatalf("reconciliation retried %s", r.URL.Path)
				}
				status := http.StatusOK
				var value any
				switch {
				case strings.HasSuffix(r.URL.Path, "/channels/22/messages/33"):
					value = struct {
						*discordgo.Message
						Components []discordgo.MessageComponent `json:"components"`
					}{Message: message, Components: message.Components}
				case strings.HasSuffix(r.URL.Path, "/channels/22"):
					guild := tt.channelGuild
					if guild == "" {
						guild = "11"
					}
					value = &discordgo.Channel{ID: "22", GuildID: guild, Type: discordgo.ChannelTypeGuildText}
				case strings.HasSuffix(r.URL.Path, "/users/@me"):
					value = &discordgo.User{ID: "44", Bot: true}
				default:
					t.Fatalf("unexpected REST read: %s", r.URL.Path)
				}
				if tt.failurePath != "" && strings.HasSuffix(r.URL.Path, tt.failurePath) {
					if tt.failureStatus == 0 {
						return nil, errors.New("private network failure https://secret.invalid")
					}
					status = tt.failureStatus
					value = map[string]any{"code": 50001, "message": "private Discord failure"}
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(encoded))), Request: r}, nil
			})}
			rawURL := tt.rawURL
			if rawURL == "" {
				rawURL = "https://discord.com/channels/11/22/33"
			}
			receipt, err := (ticketDiscordClient{session: session, resolver: guildResolver{db: db}}).ValidateTicketQueueMessage(context.Background(), &tickets.Ticket{ID: "ticket-id", GuildID: "internal", LogChannelDiscordID: "22"}, rawURL)
			if tt.wantOK {
				if err != nil || receipt == nil || receipt.MessageID != "33" || receipt.URL != "https://discord.com/channels/11/22/33" || len(calls) != 3 {
					t.Fatalf("valid queue message rejected: receipt=%+v err=%v calls=%v", receipt, err, calls)
				}
			} else {
				wantErr := tickets.ErrInvalidQueueReceipt
				if tt.failurePath != "" {
					wantErr = errTicketQueueVerificationUnavailable
				}
				if receipt != nil || !errors.Is(err, wantErr) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
					t.Fatalf("invalid or unverifiable queue receipt accepted or misreported: receipt=%+v err=%v", receipt, err)
				}
			}
			if tt.wantNoReads && len(calls) != 0 {
				t.Fatalf("invalid link reached Discord: %v", calls)
			}
		})
	}
}

// TestQueueRecoveryStaleFeedback explains how to refresh expired recovery
// controls without offering a blind retry or claiming a delivery decision.
func TestQueueRecoveryStaleFeedback(t *testing.T) {
	for _, err := range []error{tickets.ErrInvalidTransition, fmt.Errorf("wrapped: %w", tickets.ErrQueueDeliveryUnknown)} {
		message := ticketQueueRecoveryError(err)
		if !strings.Contains(message, "View ticket again") || !strings.Contains(message, "No replacement was sent") {
			t.Fatalf("missing recovery guidance: %s", message)
		}
	}
	if ticketQueueRecoveryError(tickets.ErrPermissionDenied) != ticketErrorMessage(tickets.ErrPermissionDenied) {
		t.Fatal("permission denial was hidden by stale-state guidance")
	}
}

// TestQueueRecoveryIsExceptionalAndManagerOnly keeps ordinary ticket controls
// simple and prevents owners or moderators from receiving recovery authority.
func TestQueueRecoveryIsExceptionalAndManagerOnly(t *testing.T) {
	for _, scenario := range []struct {
		manager, pending, want bool
		status                 tickets.Status
		channel, message       string
	}{
		{true, false, true, tickets.StatusOpen, "queue", ""},
		{false, false, false, tickets.StatusOpen, "queue", ""},
		{true, false, false, tickets.StatusOpen, "queue", "sent"},
		{true, false, false, tickets.StatusOpen, "", ""},
		{true, true, true, tickets.StatusResolved, "queue", ""},
		{true, false, false, tickets.StatusResolved, "queue", ""},
	} {
		ticket := &tickets.Ticket{ID: "ticket", Status: scenario.status, LogChannelDiscordID: scenario.channel, LogMessageDiscordID: scenario.message}
		message := ticketDetailMessage(ticket, nil, tickets.Actor{CanManage: scenario.manager, CanModerate: true}, scenario.pending, nil, 0)
		found := false
		for _, row := range message.Components {
			for _, component := range row.(discordgo.ActionsRow).Components {
				button := component.(discordgo.Button)
				id, err := ui.DecodeCustomID(button.CustomID)
				if err != nil {
					t.Fatal(err)
				}
				if id.Action == "queuefix" {
					found = true
				}
			}
		}
		if found != scenario.want || !message.Ephemeral {
			t.Fatalf("wrong recovery visibility: %+v", scenario)
		}
	}
}

// TestQueueRecoveryConfirmationAndAuthorization binds confirmation/modal state
// to one attempt and verifies final submissions still require fresh authority.
func TestQueueRecoveryConfirmationAndAuthorization(t *testing.T) {
	r := &Runtime{}
	registry := interactions.NewComponentRegistry()
	if err := r.registerTicketQueueRecovery(registry); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("T", 26) + "~" + strings.Repeat("A", 26)
	ctx := ui.Context{Context: context.Background(), Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "guild", Type: discordgo.InteractionMessageComponent, Member: &discordgo.Member{User: &discordgo.User{ID: "former-admin"}}, Data: discordgo.MessageComponentInteractionData{CustomID: queueRecoveryButton("queueretry", payload, "test", discordgo.SecondaryButton).CustomID}}}}
	confirmation := r.ticketQueueRetryComponent(ctx)
	if confirmation.Task != nil || confirmation.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(confirmation.Response.Data.Content, "duplicate") {
		t.Fatal("confirmation was not explicit and private")
	}
	button := confirmation.Response.Data.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
	id, err := ui.DecodeCustomID(button.CustomID)
	if err != nil || id.Payload != payload || id.Action != "queueretryok" {
		t.Fatal("confirmation lost its attempt")
	}
	handler, ok, err := registry.LookupComponent(button.CustomID)
	if err != nil || !ok {
		t.Fatal("confirmation is not registered")
	}
	ctx.Interaction.Data = discordgo.MessageComponentInteractionData{CustomID: button.CustomID}
	result := handler(ctx)
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatal("mutation did not defer for live authorization")
	}
	responder := &progressResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if len(responder.messages) != 1 || !strings.Contains(responder.messages[0], "could not verify") {
		t.Fatal("revoked/unavailable authority reached recovery")
	}
	ctx.Interaction.Data = discordgo.MessageComponentInteractionData{CustomID: queueRecoveryButton("queueadopt", payload, "test", discordgo.SecondaryButton).CustomID}
	modal := r.ticketQueueAdoptComponent(ctx)
	if modal.Response.Type != discordgo.InteractionResponseModal {
		t.Fatal("missing adoption form")
	}
	if _, ok, err := registry.LookupModal(modal.Response.Data.CustomID); err != nil || !ok {
		t.Fatal("adoption submit is not registered")
	}
}
