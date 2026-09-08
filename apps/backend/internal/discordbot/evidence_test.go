package discordbot

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
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

// TestEvidenceCopyReturnsReopenableMessageLink verifies that administrator-chosen
// channel visibility does not prevent storage and that receipts link to saved messages.
func TestEvidenceCopyReturnsReopenableMessageLink(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	uploads := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		body := `{"id":"storage","guild_id":"guild","type":0,"permission_overwrites":[]}`
		if request.Method == http.MethodPost {
			uploads++
			body = `{"id":"copy","channel_id":"storage","attachments":[{"id":"file","url":"https://cdn.discordapp.com/attachments/storage/file/proof.png?expires=soon"}]}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	})}
	client := &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("abc")), Request: request}, nil
	})}
	saved, err := (&Bot{Session: session, HTTPClient: client}).PreserveEvidenceAttachment(context.Background(), "guild", "storage", quack.DiscordAttachmentSnapshot{Filename: "proof.png", SizeBytes: 3, URL: "https://cdn.discordapp.com/attachments/source/file/proof.png"})
	if err != nil || uploads != 1 || saved.URL != "https://discord.com/channels/guild/storage/copy" {
		t.Fatalf("copy receipt was not a stable message link: saved=%+v uploads=%d err=%v", saved, uploads, err)
	}
}
