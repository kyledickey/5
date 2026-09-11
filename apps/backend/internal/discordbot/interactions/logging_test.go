package interactions_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// failingResponseClient reproduces net/http's credential-bearing URL error.
type failingResponseClient struct {
	fakeClient
	err error
}

// InteractionRespond fails every initial response with the configured error.
func (c *failingResponseClient) InteractionRespond(*discordgo.Interaction, *discordgo.InteractionResponse) error {
	return c.err
}

// TestDispatcherLogsNeverExposeWebhookCredentials proves response, task and
// panic logs omit the interaction token even when the error text contains it.
func TestDispatcherLogsNeverExposeWebhookCredentials(t *testing.T) {
	const secret = "private-interaction-token"
	transportErr := &url.Error{
		Op:  "Post",
		URL: "https://discord.com/api/v10/webhooks/application/" + secret,
		Err: errors.New("connection reset"),
	}
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	for _, mode := range []string{"response", "task", "panic"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
			client := &fakeClient{done: make(chan struct{}, 1)}
			dispatcher := &interactions.Dispatcher{Client: client}
			dispatcher.Commands = fakeCommands{"test": func(ui.Context) ui.HandlerResult {
				if mode == "response" {
					return ui.Immediate(ui.Public(ui.Content("done", false)))
				}
				return ui.Async(ui.DeferEphemeral(), func(context.Context, ui.Responder) error {
					if mode == "panic" {
						panic(secret)
					}
					return transportErr
				})
			}}
			if mode == "response" {
				dispatcher.Client = &failingResponseClient{err: transportErr}
			}
			dispatcher.Handle(nil, commandInteraction("test", discordgo.InteractionApplicationCommand))
			if mode != "response" {
				client.wait(t)
			}
			if strings.Contains(output.String(), secret) || output.Len() == 0 {
				t.Fatalf("unsafe or missing operational log: %s", output.String())
			}
		})
	}
}

// TestDiscordRejectionLogsIncludeSafeDiagnostics makes initial-response failures
// actionable while protecting the error body's potentially sensitive content.
func TestDiscordRejectionLogsIncludeSafeDiagnostics(t *testing.T) {
	const secret = "private-interaction-token"
	var output bytes.Buffer
	prior := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prior) })
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	client := &failingResponseClient{err: &discordgo.RESTError{
		Response:     &http.Response{StatusCode: 404},
		Message:      &discordgo.APIErrorMessage{Code: 10062, Message: secret},
		ResponseBody: []byte(secret),
	}}
	dispatcher := &interactions.Dispatcher{Client: client, Commands: fakeCommands{"test": func(ui.Context) ui.HandlerResult {
		return ui.Immediate(ui.DeferEphemeral())
	}}}
	dispatcher.Handle(nil, commandInteraction("test", discordgo.InteractionApplicationCommand))
	logged := output.String()
	for _, want := range []string{`"http_status":404`, `"discord_code":10062`, `"interaction_type":2`, `"response_type":5`, `"elapsed_ms":`} {
		if !strings.Contains(logged, want) {
			t.Fatalf("missing %s in %s", want, logged)
		}
	}
	if strings.Contains(logged, secret) {
		t.Fatalf("logged response secrets: %s", logged)
	}
}
