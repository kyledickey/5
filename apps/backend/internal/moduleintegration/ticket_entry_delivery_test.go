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

// TestTicketEntryMoveRetiresOldPanelBeforePublishing preserves a retryable old
// reference on retirement failure and verifies a moved panel has no active controls.
func TestTicketEntryMoveRetiresOldPanelBeforePublishing(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		wantSend bool
	}{
		{"retired", 200, `{"id":"old-panel"}`, true},
		{"message deleted", 404, `{"code":10008,"message":"Unknown Message"}`, true},
		{"channel deleted", 404, `{"code":10003,"message":"Unknown Channel"}`, true},
		{"permission lost", 403, `{"code":50013,"message":"Missing Permissions"}`, false},
		{"unavailable", 503, `{"message":"Unavailable"}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			var requests []string
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				status, body := scenario.status, scenario.body
				switch r.Method {
				case http.MethodPatch:
					if !strings.HasSuffix(r.URL.Path, "/channels/old-entry/messages/old-panel") {
						t.Fatal("edited the wrong panel")
					}
					var payload struct {
						Content    string            `json:"content"`
						Components []json.RawMessage `json:"components"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(payload.Content, "<#new-entry>") || payload.Components == nil || len(payload.Components) != 0 {
						t.Fatal("retired panel must redirect and clear controls")
					}
				case http.MethodPost:
					if len(requests) != 2 || !strings.HasSuffix(r.URL.Path, "/channels/new-entry/messages") {
						t.Fatal("published before retiring old panel")
					}
					status, body = 200, `{"id":"new-panel"}`
				default:
					t.Fatalf("unexpected request: %s", r.Method)
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			_, err := (ticketDiscordClient{session: session}).publishTicketEntry(context.Background(), tickets.Settings{EntryChannelDiscordID: "new-entry", EntryPanelChannelID: "old-entry", EntryPanelMessageID: "old-panel"})
			if (err == nil) != scenario.wantSend || (len(requests) == 2) != scenario.wantSend {
				t.Fatalf("requests=%v err=%v", requests, err)
			}
		})
	}
}
