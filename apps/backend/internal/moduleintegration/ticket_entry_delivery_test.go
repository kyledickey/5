package moduleintegration

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// TestTicketEntryReusesPanel limits recreation to an explicit missing message;
// permission failures must not turn a setup retry into duplicate panels.
func TestTicketEntryReusesPanel(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		sends  int
		fail   bool
	}{
		{"existing", 200, `{"id":"panel"}`, 0, false},
		{"deleted", 404, `{"code":10008,"message":"Unknown Message"}`, 1, false},
		{"forbidden", 403, `{"code":50013,"message":"Missing Permissions"}`, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			edits, sends := 0, 0
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				status, body := test.status, test.body
				switch r.Method {
				case http.MethodPatch:
					edits++
				case http.MethodPost:
					sends++
					status = 200
					body = `{"id":"replacement"}`
				default:
					t.Fatalf("unexpected method %s", r.Method)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			_, err = (ticketDiscordClient{session: session}).publishTicketEntry(context.Background(), tickets.Settings{EntryChannelDiscordID: "entry", EntryPanelChannelID: "entry", EntryPanelMessageID: "panel"})
			if (err != nil) != test.fail || edits != 1 || sends != test.sends {
				t.Fatalf("edits=%d sends=%d error=%v", edits, sends, err)
			}
		})
	}
}
