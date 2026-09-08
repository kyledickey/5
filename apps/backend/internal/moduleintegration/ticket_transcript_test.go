package moduleintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TestTicketTranscriptKeepsStableOrderAndAttachmentContext covers overlapping
// pages and equal timestamps, while retaining readable author attribution.
func TestTicketTranscriptKeepsStableOrderAndAttachmentContext(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	calls := 0
	stamp := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		var page []*discordgo.Message
		if calls == 1 {
			for id := 200; id > 100; id-- {
				page = append(page, &discordgo.Message{ID: fmt.Sprint(id), Timestamp: stamp, Content: fmt.Sprintf("message-%d", id), Author: &discordgo.User{ID: "member", Username: "Member"}})
			}
		} else {
			if r.URL.Query().Get("before") != "101" {
				t.Fatalf("incorrect history cursor: %s", r.URL.RawQuery)
			}
			page = []*discordgo.Message{{ID: "101", Timestamp: stamp, Content: "message-101", Author: &discordgo.User{ID: "member", Username: "Member"}}, {ID: "100", Timestamp: stamp, Content: "first", Attachments: []*discordgo.MessageAttachment{nil, {Filename: "proof.png", Size: 42, URL: "https://cdn.discordapp.com/attachments/file"}}}}
		}
		body, _ := json.Marshal(page)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	transcript, err := (ticketDiscordClient{session: session}).CaptureTicketTranscript(context.Background(), "thread")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || strings.Count(transcript, "message-101") != 1 || strings.Index(transcript, "first") > strings.Index(transcript, "message-101") || !strings.Contains(transcript, "Member (member)") || !strings.Contains(transcript, "original attachment URL (may expire)") {
		t.Fatalf("incorrect transcript: %s", transcript)
	}
}

// TestTicketTranscriptRejectsRepeatedPage ensures closure cannot silently loop
// forever when Discord fails to advance its history cursor.
func TestTicketTranscriptRejectsRepeatedPage(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	calls := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("pagination did not terminate")
		}
		page := make([]*discordgo.Message, 100)
		for i := range page {
			page[i] = &discordgo.Message{ID: fmt.Sprint(200 - i)}
		}
		body, _ := json.Marshal(page)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}
	if _, err := (ticketDiscordClient{session: session}).CaptureTicketTranscript(context.Background(), "thread"); err == nil {
		t.Fatal("repeated history page accepted")
	}
}
