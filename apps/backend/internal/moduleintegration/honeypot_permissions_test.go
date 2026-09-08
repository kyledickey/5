package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestHoneypotChannelRequiresEvidenceAndCleanupPermissions prevents enabling a
// trap that can punish members but cannot read or remove the triggering message.
func TestHoneypotChannelRequiresEvidenceAndCleanupPermissions(t *testing.T) {
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
	all := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages)
	for _, test := range []struct {
		name        string
		deny        int64
		channelType int
		want        string
	}{
		{name: "ready"},
		{name: "missing history", deny: discordgo.PermissionReadMessageHistory, want: "Read Message History"},
		{name: "missing cleanup", deny: discordgo.PermissionManageMessages, want: "Manage Messages"},
		{name: "voice channel", channelType: 2, want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch {
				case strings.HasSuffix(r.URL.Path, "/channels/trap"):
					body = fmt.Sprintf(`{"id":"trap","guild_id":"guild","type":%d,"permission_overwrites":[{"id":"bot","type":1,"deny":"%d","allow":"0"}]}`, test.channelType, test.deny)
				case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					body = fmt.Sprintf(`{"id":"guild","roles":[{"id":"guild","permissions":"%d"}]}`, all)
				case strings.HasSuffix(r.URL.Path, "/members/bot"):
					body = `{"user":{"id":"bot"},"roles":[]}`
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			err := (honeypotChannelValidator{session: session, resolver: guildResolver{db: db}}).ValidateHoneypotChannel(context.Background(), "internal", "trap")
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, honeypot.ErrChannelUnavailable) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incorrect failure: %v", err)
			}
		})
	}
}
