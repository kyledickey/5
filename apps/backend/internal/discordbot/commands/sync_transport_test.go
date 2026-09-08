package commands

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// commandRateLimitTransport returns one short Discord cooldown, then the
// requested command. It verifies transport retry behavior without network I/O.
type commandRateLimitTransport struct{ calls int }

// RoundTrip simulates a definite 429 rejection, which is safe to retry.
func (r *commandRateLimitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	r.calls++
	code, body := 200, `{"id":"command","name":"template","type":1}`
	if r.calls == 1 {
		code, body = 429, `{"message":"rate limited","retry_after":0.001,"global":false}`
	}
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
}

// TestCommandRegistrationHonorsDiscordCooldown prevents short registration
// bursts from aborting the entire bot startup after a definite 429 response.
func TestCommandRegistrationHonorsDiscordCooldown(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	transport := &commandRateLimitTransport{}
	session.Client = &http.Client{Transport: transport}
	client := sessionCommandClient{session: session}
	command, err := client.EditCommand(context.Background(), "app", "guild", "command", &discordgo.ApplicationCommand{Name: "template", Description: "Manage rules"})
	if err != nil || command == nil || command.ID != "command" || transport.calls != 2 {
		t.Fatalf("cooldown recovery: command=%+v calls=%d err=%v", command, transport.calls, err)
	}
}

// TestGuildCommandFingerprintMatchesDiscordOmission covers the actual returned
// field difference while preserving meaningful global DM-permission changes.
func TestGuildCommandFingerprintMatchesDiscordOmission(t *testing.T) {
	local := UserCaseCommandSpec().Definition
	remote := *local
	remote.DMPermission = nil
	localHash, _, err := commandFingerprintForScope(local, "guild")
	if err != nil {
		t.Fatal(err)
	}
	remoteHash, _, err := commandFingerprintForScope(&remote, "guild")
	if err != nil || localHash != remoteHash {
		t.Fatalf("guild definitions differ: err=%v", err)
	}
	if local.DMPermission == nil {
		t.Fatal("normalization mutated the registered command")
	}
	localHash, _, _ = commandFingerprintForScope(local, "")
	remoteHash, _, _ = commandFingerprintForScope(&remote, "")
	if localHash == remoteHash {
		t.Fatal("global DM change was ignored")
	}
}
