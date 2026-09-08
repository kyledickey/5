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
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestHoneypotCounterRepairsOnlyMissingWarnings verifies real edit/send transport
// preserves configured copy and stores the replacement receipt for later updates.
func TestHoneypotCounterRepairsOnlyMissingWarnings(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx := context.Background()
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.Guild{}); err != nil {
				t.Fatal(err)
			}
			if err := modules.RegistryMigration().Apply(db); err != nil {
				t.Fatal(err)
			}
			if err := honeypot.Migration().Apply(db); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
				t.Fatal(err)
			}
			registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
			if err != nil {
				t.Fatal(err)
			}
			settings := honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Custom warning", WarningMessageID: "old"}
			raw, _ := json.Marshal(settings)
			if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(raw)}); err != nil {
				t.Fatal(err)
			}
			service := honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil)
			session, _ := discordgo.New("Bot test")
			posts := 0
			session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
				code, body := 200, `{"id":"trap","guild_id":"guild","type":0}`
				if request.Method == http.MethodPatch {
					code = status
					body = `{"id":"old"}`
					if status != 200 {
						body = `{"code":10008,"message":"Unknown Message"}`
					}
					if status == 500 {
						body = `{"code":0,"message":"Server error"}`
					}
				}
				if request.Method == http.MethodPost {
					posts++
					payload, _ := io.ReadAll(request.Body)
					if !strings.Contains(string(payload), "Custom warning") || !strings.Contains(string(payload), "0 incidents caught") {
						t.Fatalf("wrong warning: %s", payload)
					}
					body = `{"id":"replacement"}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			counter := honeypotCounter{session: session, service: service, resolver: guildResolver{db: db}}
			err = counter.IncidentCreated(ctx, "internal")
			if (err == nil) != (status != 500) || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("unexpected repair: posts=%d err=%v", posts, err)
			}
			saved, _, err := service.Settings(ctx, honeypot.Actor{GuildID: "internal", CanManage: true})
			want := "old"
			if status == 404 {
				want = "replacement"
			}
			if err != nil || saved.WarningMessageID != want || saved.WarningText != "Custom warning" {
				t.Fatalf("receipt: %+v %v", saved, err)
			}
		})
	}
}
