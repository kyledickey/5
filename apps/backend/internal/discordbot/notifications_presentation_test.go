package discordbot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
)

// TestNotificationAdaptersSendApplicationText checks real REST serialization
// for both uploaded catalogs without delivering a DM or invoking enforcement.
func TestNotificationAdaptersSendApplicationText(t *testing.T) {
	for _, appID := range []string{"968198214450831370", "819019613371236432"} {
		t.Run(appID, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.State.User = &discordgo.User{ID: appID}
			body := discordtext.Conversation("timeout", "You’ve been timed out in **The Pond**.", "A reason.", "", "Case #12")
			count := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				response := `{"id":"dm-channel"}`
				if strings.HasSuffix(request.URL.Path, "/messages") {
					count++
					var payload struct {
						Content         string
						Embeds          []json.RawMessage
						Components      []json.RawMessage
						AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.Content != discordtext.Resolve(body, appID) || len(payload.Embeds) != 0 || payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
						t.Errorf("invalid text DM: %+v", payload)
					}
					if count == 3 && len(payload.Components) != 1 {
						t.Error("case DM lost its appeal button")
					}
					response = `{"id":"sent-message"}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
			})}
			bot := &Bot{Session: session}
			if _, err := bot.SendDM(context.Background(), "member", body); err != nil {
				t.Fatal(err)
			}
			if _, err := bot.SendPreparedDM(context.Background(), "dm-channel", body); err != nil {
				t.Fatal(err)
			}
			if _, err := bot.SendCaseNotification(context.Background(), "member", "dm-channel", body, "https://dashboard.example", "guild", "case"); err != nil {
				t.Fatal(err)
			}
			adapter := &AppealNotificationAdapter{Session: session}
			if _, err := adapter.SendAppealMemberNotification(context.Background(), "member", body); err != nil {
				t.Fatal(err)
			}
			if count != 4 {
				t.Fatalf("expected one message per delivery, got %d", count)
			}
		})
	}
}
