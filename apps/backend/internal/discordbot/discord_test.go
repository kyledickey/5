package discordbot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

func TestBotStatusTracksGatewayDisconnectAndResume(t *testing.T) {
	bot, err := New("token")
	if err != nil {
		t.Fatalf("new bot: %v", err)
	}
	bot.Session.State.User = &discordgo.User{ID: "bot-1", Username: "quack"}
	bot.gatewayReady(bot.Session, &discordgo.Ready{})
	if connected, _, _ := bot.Status(); !connected {
		t.Fatal("expected ready gateway to be connected")
	}
	bot.gatewayDisconnected(bot.Session, &discordgo.Disconnect{})
	if connected, _, _ := bot.Status(); connected {
		t.Fatal("expected disconnected gateway to fail readiness")
	}
	bot.gatewayResumed(bot.Session, &discordgo.Resumed{})
	if connected, _, _ := bot.Status(); !connected {
		t.Fatal("expected resumed gateway to restore readiness")
	}
}

func TestDiscordActionClassificationRedactsAndProtectsIrreversibleOutcomes(t *testing.T) {
	tests := []struct {
		name                 string
		status               int
		irreversible         bool
		code                 string
		retryable, uncertain bool
	}{{"validation", 400, false, "validation_failed", false, false}, {"permission", 403, false, "permission_or_hierarchy_denied", false, false}, {"unknown", 404, false, "unknown_member_or_resource", false, false}, {"rate", 429, true, "rate_limited", true, false}, {"safe server", 500, false, "discord_server_error", true, false}, {"uncertain ban", 500, true, "discord_server_error", false, true}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &discordgo.RESTError{Response: &http.Response{StatusCode: test.status}}
			err := classifyDiscordOperation("ban", source, test.irreversible)
			var classified actionmods.DiscordError
			if !errors.As(err, &classified) {
				t.Fatalf("not classified: %v", err)
			}
			if classified.Retryable != test.retryable || classified.OutcomeUncertain != test.uncertain || classified.Message == source.Error() || !strings.Contains(classified.Code, test.code) {
				t.Fatalf("unexpected classification: %+v", classified)
			}
		})
	}
}

// requestTransport isolates REST behavior from both network access and Discord.
type requestTransport func(*http.Request) (*http.Response, error)

func (f requestTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestEnforcementCarriesContextAndDoesNotRetryBehindWorker(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "trace")
	calls := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context().Value(key{}) != "trace" {
			t.Error("request lost its context")
		}
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"upstream unavailable"}`)), Request: request}, nil
	})}
	_, err = (&Bot{Session: session}).BanMember(ctx, "guild", "member", 0, "case")
	var classified actionmods.DiscordError
	if calls != 1 || !errors.As(err, &classified) || !classified.OutcomeUncertain || classified.Retryable {
		t.Fatalf("expected one uncertain attempt: calls=%d error=%+v", calls, err)
	}
}

// TestEvidenceDownloadRejectsUnsafeURLsBeforeRequest verifies rejected sources
// never reach either the CDN transport or Discord upload client.
func TestEvidenceDownloadRejectsUnsafeURLsBeforeRequest(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected download request")
	})}
	// No Discord session is supplied: an upload attempt would panic.
	bot := &Bot{HTTPClient: client}
	for _, raw := range []string{"http://cdn.discordapp.com/attachments/1/2/x", "https://localhost/attachments/1/2/x", "https://cdn.discordapp.com.evil.test/attachments/x", "https://user:secret@cdn.discordapp.com/attachments/x"} {
		_, err := bot.PreserveEvidenceAttachment(context.Background(), "guild", "channel", quack.DiscordAttachmentSnapshot{URL: raw, SizeBytes: 3})
		if err == nil || calls != 0 {
			t.Fatalf("unsafe download: %s", raw)
		}
	}
}
