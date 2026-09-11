package interactions_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
)

// acceptingTransport answers every interaction callback with 204 and counts
// them atomically, standing in for Discord during concurrent dispatch.
type acceptingTransport struct{ responses atomic.Int64 }

// RoundTrip records one accepted callback.
func (t *acceptingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.responses.Add(1)
	return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}

// TestDispatcherHandlesConcurrentInteractionsWithLazyClient proves that a
// dispatcher whose Client and Deduper are left nil, as commands.Register leaves
// them, can be driven from many gateway goroutines at once: the session-backed
// client and the deduper are created once, every distinct interaction is
// answered, and the race detector stays quiet.
func TestDispatcherHandlesConcurrentInteractionsWithLazyClient(t *testing.T) {
	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatal(err)
	}
	transport := &acceptingTransport{}
	session.Client = &http.Client{Transport: transport}
	var handled atomic.Int64
	dispatcher := &interactions.Dispatcher{Commands: fakeCommands{
		"ping": func(ui.Context) ui.HandlerResult {
			handled.Add(1)
			return ui.Immediate(ui.Public(ui.Content("pong", false)))
		},
	}}

	const workers = 64
	var wait sync.WaitGroup
	for i := range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			interaction := commandInteraction("ping", discordgo.InteractionApplicationCommand)
			interaction.ID = "interaction-" + string(rune('A'+i%26)) + string(rune('a'+i/26))
			dispatcher.Handle(session, interaction)
		}()
	}
	wait.Wait()

	if handled.Load() != workers || transport.responses.Load() != workers {
		t.Fatalf("handled=%d responses=%d, want %d each", handled.Load(), transport.responses.Load(), workers)
	}
	if dispatcher.Client == nil || dispatcher.Deduper == nil {
		t.Fatal("lazy client or deduper was not installed")
	}
}
