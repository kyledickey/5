package moduleintegration

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

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
