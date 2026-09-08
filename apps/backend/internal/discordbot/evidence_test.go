package discordbot

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// TestEvidenceChannelPreservesAdministratorChanges ensures routine startup and
// channel events cannot undo a server's chosen storage name or permissions.
func TestEvidenceChannelPreservesAdministratorChanges(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	calls := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Method != http.MethodGet || !strings.HasSuffix(request.URL.Path, "/channels/storage") {
			t.Fatalf("existing channel was modified: %s %s", request.Method, request.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"storage","guild_id":"guild","type":0,"name":"our-evidence","parent_id":"staff-category","permission_overwrites":[]}`)), Request: request}, nil
	})}
	id, err := (&Bot{Session: session}).EnsureEvidenceChannel(context.Background(), "guild", "storage")
	if err != nil || id != "storage" || calls != 1 {
		t.Fatalf("existing storage not reused: id=%s calls=%d err=%v", id, calls, err)
	}
}
