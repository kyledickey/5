package moduleintegration

import (
	"context"
	"fmt"
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

// TestTicketTranscriptRecreatesDeletedQueueMessage verifies that a consumed
// multipart reader is replaced before retrying a deleted queue message.
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
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/queue"):
			body = fmt.Sprintf(`{"id":"queue","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"%d","allow":"0"}]}`, discordgo.PermissionViewChannel)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			body = `{"id":"guild","roles":[]}`
		case r.Method == http.MethodPatch || r.Method == http.MethodPost:
			writes++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer r.MultipartForm.RemoveAll()
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
			if string(data) != "preserved conversation" || files[0].Filename != "ticket-ticket.txt" {
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
				body = `{"id":"replacement","attachments":[{"id":"file","filename":"ticket-ticket.txt"}]}`
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client := ticketDiscordClient{session: session, resolver: guildResolver{db: db}}
	receipt, err := client.PublishTicketQueue(context.Background(), &tickets.Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "owner", LogChannelDiscordID: "queue", LogMessageDiscordID: "original"}, tickets.Settings{QueueChannelDiscordID: "queue"}, &tickets.Transcript{Content: "preserved conversation"})
	if err != nil {
		t.Fatal(err)
	}
	if writes != 2 || receipt.URL != "https://discord.com/channels/guild/queue/replacement" {
		t.Fatalf("incorrect receipt: %+v writes=%d", receipt, writes)
	}
}
