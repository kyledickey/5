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
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
					permissions := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages)
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
