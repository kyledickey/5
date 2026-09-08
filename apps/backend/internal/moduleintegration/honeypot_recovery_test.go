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
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// TestHoneypotRecoveryRefreshesAuthor checks live staff and self exemptions while
// allowing ordinary third-party bots to use the same normal case preflight path.
func TestHoneypotRecoveryRefreshesAuthor(t *testing.T) {
	for _, scenario := range []struct {
		name, id      string
		bot           bool
		permissions   int64
		messageStatus int
		want          error
	}{
		{name: "ordinary bot", id: "member", bot: true},
		{name: "moderator bot", id: "member", bot: true, permissions: discordgo.PermissionModerateMembers, want: honeypot.ErrExempt},
		{name: "human administrator", id: "member", permissions: discordgo.PermissionAdministrator, want: honeypot.ErrExempt},
		{name: "Quack", id: "quack", bot: true, want: honeypot.ErrExempt},
		{name: "missing source", id: "member", messageStatus: 404, want: honeypot.ErrNotTrigger},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "quack", Bot: true}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet {
					t.Fatal("recovery preparation mutated Discord")
				}
				var body any
				status := http.StatusOK
				switch {
				case strings.HasSuffix(request.URL.Path, "/channels/trap/messages/message"):
					if scenario.messageStatus != 0 {
						status = scenario.messageStatus
						body = map[string]any{"code": 10008}
					} else {
						body = &discordgo.Message{ID: "message", ChannelID: "trap", Author: &discordgo.User{ID: scenario.id, Bot: scenario.bot}}
					}
				case strings.HasSuffix(request.URL.Path, "/channels/trap"):
					body = &discordgo.Channel{ID: "trap", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
				case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
					body = &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: scenario.permissions}}}
				case strings.Contains(request.URL.Path, "/guilds/guild/members/"):
					body = &discordgo.Member{User: &discordgo.User{ID: scenario.id, Bot: scenario.bot}, Roles: []string{"role"}}
				default:
					t.Fatalf("unexpected lookup %s", request.URL.Path)
				}
				raw, _ := json.Marshal(body)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			request := honeypot.ApplyRequest{GuildID: "internal", TemplateID: "template", TargetDiscordUserID: scenario.id, ContextChannelDiscordID: "trap", ContextMessageDiscordID: "message"}
			prepared, err := (honeypotCaseApplier{session: session}).PrepareHoneypotRecovery(context.Background(), request)
			if !errors.Is(err, scenario.want) {
				t.Fatalf("got %v want %v", err, scenario.want)
			}
			if scenario.want == nil && prepared.ContextURL != "https://discord.com/channels/guild/trap/message" {
				t.Fatal("missing canonical evidence URL", prepared)
			}
		})
	}
}
