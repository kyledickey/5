package moduleintegration

import (
	"context"
	"encoding/json"
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
