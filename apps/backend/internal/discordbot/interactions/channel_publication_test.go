package interactions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// publicationTransport inspects actual Discord HTTP requests without network I/O.
type publicationTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates to the test's deterministic response fixture.
func (f publicationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestStandalonePublicationTransport verifies no reply reference or unexpected
// mentions, channel-token edits, and one POST even on uncertain server failure.
func TestStandalonePublicationTransport(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "uncertain"}[fail], func(t *testing.T) {
			session, err := discordgo.New("Bot test-token")
			if err != nil {
				t.Fatal(err)
			}
			posts, patches := 0, 0
			session.Client = &http.Client{Transport: publicationTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bot test-token" {
					t.Fatal("request did not use bot credentials")
				}
				if !strings.HasPrefix(request.URL.Path, "/api/v9/channels/channel/messages") {
					t.Fatal("wrong channel transport", request.URL.Path)
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if _, ok := body["message_reference"]; ok {
					t.Fatal("hidden acknowledgement referenced")
				}
				mentions, ok := body["allowed_mentions"].(map[string]any)
				if !ok {
					t.Fatal("mention policy missing")
				}
				if allowed, ok := mentions["parse"].([]any); ok && len(allowed) > 0 {
					t.Fatal("unexpected automatic mention")
				}
				switch request.Method {
				case http.MethodPost:
					posts++
				case http.MethodPatch:
					patches++
				default:
					t.Fatal("unexpected method", request.Method)
				}
				status, payload := 200, `{"id":"notice","channel_id":"channel"}`
				if fail {
					status = 500
					payload = `{"message":"uncertain"}`
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload)), Request: request}, nil
			})}
			responder := responder{client: sessionClient{session: session}, interaction: &discordgo.Interaction{AppID: "application", Token: "PRIVATE INTERACTION TOKEN", ChannelID: "channel"}}
			if _, err := responder.PublishChannel(context.Background(), ui.Content("private", true)); err == nil || posts != 0 {
				t.Fatal("ephemeral content accepted")
			}
			message, err := responder.PublishChannel(context.Background(), ui.Content("Case #1 for <@member>", false))
			if posts != 1 {
				t.Fatal("non-idempotent send retried", posts)
			}
			if fail {
				if err == nil {
					t.Fatal("uncertain send reported success")
				}
				return
			}
			if err != nil || message.ID != "notice" || message.ChannelID != "channel" {
				t.Fatal("coordinate lost", message, err)
			}
			if _, err := responder.EditChannel(context.Background(), message.ID, ui.EditMessage(ui.Content("Completed", false))); err != nil {
				t.Fatal(err)
			}
			if patches != 1 {
				t.Fatal("channel edit missing")
			}
		})
	}
}
