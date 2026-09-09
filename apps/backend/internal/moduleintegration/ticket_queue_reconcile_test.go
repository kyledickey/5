package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
