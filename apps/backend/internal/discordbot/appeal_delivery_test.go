package discordbot

import (
	"context"
	"errors"
	"fmt"
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestAppealSendErrorRetriesOnlyKnownRejections keeps ambiguous transport failures
// out of automatic retry while allowing channel/permission/rate-limit repair.
func TestAppealSendErrorRetriesOnlyKnownRejections(t *testing.T) {
	for _, status := range []int{403, 404, 429, 500} {
		err := appealSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, quack.ErrAppealDeliveryDeferred) != (status != 500) {
			t.Fatalf("incorrect retry classification for %d: %v", status, err)
		}
	}
	if err := appealSendError(errors.New("connection reset after writing request")); errors.Is(err, quack.ErrAppealDeliveryDeferred) {
		t.Fatal("uncertain send was retried")
	}
}

// appealQueueResolverStub supplies a validated destination for transport tests.
type appealQueueResolverStub struct{}

func (appealQueueResolverStub) AppealStaffChannel(context.Context, string) (string, error) {
	return "queue", nil
}

// TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages prevents duplicate
// posts on transient edits and preserves the final decision without stale controls.
func TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			edits, posts := 0, 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				code, body := 200, `{"id":"replacement"}`
				if request.Method == http.MethodPatch {
					edits++
					raw, _ := io.ReadAll(request.Body)
					if strings.Contains(string(raw), "appeal:accept") || strings.Contains(string(raw), "appeal:reject") {
						t.Fatal("decided message still has decision controls")
					}
					code = status
					if code != 200 {
						body = `{"code":10008,"message":"Unknown Message"}`
						if code == 500 {
							body = `{"code":0,"message":"Server error"}`
						}
					}
				} else if request.Method == http.MethodPost {
					posts++
				} else {
					t.Fatalf("unexpected request %s", request.Method)
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			adapter := &AppealNotificationAdapter{Session: session, Resolver: appealQueueResolverStub{}}
			receipt, err := adapter.SendAppealStaffNotification(context.Background(), "guild", &quack.AppealResponse{ID: "appeal", Status: model.AppealStatusRejected}, quack.AppealQueueReceipt{ChannelID: "queue", MessageID: "original"})
			if edits != 1 || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("unexpected requests: %d edits, %d posts", edits, posts)
			}
			if status == 500 {
				if !errors.Is(err, quack.ErrAppealDeliveryDeferred) {
					t.Fatalf("edit not retryable: %v", err)
				}
				return
			}
			if err != nil || receipt.ChannelID != "queue" || receipt.MessageID != map[int]string{200: "original", 404: "replacement"}[status] {
				t.Fatalf("receipt: %+v %v", receipt, err)
			}
		})
	}
}
