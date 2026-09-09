package moduleintegration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestTicketCloseNoticeReconcilesOnlyBotTranscript ensures an ambiguous retry
// cannot send again or trust a member-authored lookalike as a delivery receipt.
func TestTicketCloseNoticeReconcilesOnlyBotTranscript(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/@me/channels"):
			body = `{"id":"dm"}`
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/dm/messages"):
			messages := []*discordgo.Message{
				{ID: "spoof", Content: "Ticket ticket", Author: &discordgo.User{ID: "member"}, Attachments: []*discordgo.MessageAttachment{{Filename: "ticket-ticket.txt"}}},
				{ID: "receipt", Content: "Ticket ticket", Author: &discordgo.User{ID: "bot"}, Attachments: []*discordgo.MessageAttachment{{Filename: "ticket-ticket.txt"}}},
			}
			encoded, _ := json.Marshal(messages)
			body = string(encoded)
		default:
			t.Fatalf("unexpected DM send during reconciliation: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	receipt, err := (ticketDiscordClient{session: session}).DeliverTicketCloseNotice(context.Background(), &tickets.Ticket{ID: "ticket", OwnerDiscordUserID: "member"}, &tickets.Transcript{Content: "private"}, true)
	if err != nil || receipt != "receipt" {
		t.Fatalf("wrong reconciliation: %q %v", receipt, err)
	}
}

// TestTicketCloseNoticeIncludesMemberTranscript checks the actual multipart DM:
// the server and ticket identify the conversation, and staff links never leak.
func TestTicketCloseNoticeIncludesMemberTranscript(t *testing.T) {
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
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Name: "Duck Club"}); err != nil {
		t.Fatal(err)
	}
	sent := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/users/@me/channels"):
			body = `{"id":"dm"}`
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/dm/messages"):
			sent++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
			var payload discordgo.MessageSend
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Content, "Duck Club") || !strings.Contains(payload.Content, "Ticket ticket") || strings.Contains(payload.Content, "staff-secret") {
				t.Fatalf("bad close notice: %s", payload.Content)
			}
			if payload.Flags&discordgo.MessageFlagsEphemeral != 0 {
				t.Fatal("DM has ephemeral flag")
			}
			files := r.MultipartForm.File["files[0]"]
			if len(files) != 1 || files[0].Filename != "ticket-ticket.txt" {
				t.Fatal("missing transcript")
			}
			file, err := files[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			content, err := io.ReadAll(file)
			if err != nil || string(content) != "member conversation" {
				t.Fatal("wrong transcript", err)
			}
			body = `{"id":"notice","attachments":[{"filename":"ticket-ticket.txt"}]}`
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	receipt, err := (ticketDiscordClient{session: session, resolver: guildResolver{db: db}}).DeliverTicketCloseNotice(context.Background(), &tickets.Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "member", TranscriptURL: "staff-secret"}, &tickets.Transcript{Content: "member conversation"}, false)
	if err != nil || receipt != "notice" || sent != 1 {
		t.Fatalf("DM failed: %q %v", receipt, err)
	}
}
