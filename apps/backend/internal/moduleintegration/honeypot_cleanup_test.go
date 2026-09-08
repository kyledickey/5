package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestHoneypotCleanupFollowsSavedCase keeps case application free of deletion;
// only the durable cleanup worker may remove source messages after persistence.
func TestHoneypotCleanupFollowsSavedCase(t *testing.T) {
	for _, test := range []struct {
		name         string
		caseErr      error
		deleteStatus int
		wantDelete   bool
	}{
		{"saved", nil, 204, false}, {"cleanup denied", nil, 403, false}, {"case failed", errors.New("not saved"), 204, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			creator := &systemCaseCreatorFake{result: &quack.CaseResponse{ID: "saved"}, err: test.caseErr}
			session, _ := discordgo.New("Bot test")
			deleted := false
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				if creator.guildID == "" {
					t.Fatal("cleanup preceded normal case path")
				}
				if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/channels/channel/messages/message") {
					t.Fatalf("unexpected cleanup: %s %s", r.Method, r.URL.Path)
				}
				deleted = true
				return &http.Response{StatusCode: test.deleteStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":50013,"message":"Missing Permissions"}`))}, nil
			})}
			result, err := (honeypotCaseApplier{cases: creator, session: session}).ApplyHoneypotCase(context.Background(), honeypot.ApplyRequest{GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target", ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message", ContextURL: "https://discord.com/channels/guild/channel/message", IdempotencyKey: "incident", Source: honeypot.SourceHoneypot, ActorType: honeypot.ActorTypeSystem})
			if deleted != test.wantDelete || (err != nil) != (test.caseErr != nil) {
				t.Fatalf("deleted=%v error=%v", deleted, err)
			}
			if test.caseErr == nil && result.CaseID != "saved" {
				t.Fatal("cleanup failure lost saved case")
			}
		})
	}
}

// TestHoneypotDeleteReceipts treats already-deleted resources as success while
// retaining permission/transient failures for the durable cleanup worker.
func TestHoneypotDeleteReceipts(t *testing.T) {
	for _, scenario := range []struct {
		status, code int
		failure      bool
	}{{204, 0, false}, {404, 10008, false}, {404, 10003, false}, {403, 50013, true}, {500, 0, true}} {
		session, _ := discordgo.New("Bot test")
		session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/channels/channel/messages/message") {
				t.Fatalf("unexpected deletion %s %s", r.Method, r.URL.Path)
			}
			return &http.Response{StatusCode: scenario.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"code":%d}`, scenario.code)))}, nil
		})}
		err := (honeypotCaseApplier{session: session}).DeleteHoneypotMessage(context.Background(), "channel", "message")
		if (err != nil) != scenario.failure {
			t.Fatalf("status %d: %v", scenario.status, err)
		}
	}
}
