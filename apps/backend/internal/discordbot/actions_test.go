package discordbot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TestOwnedTimeoutChecksLiveExpiry verifies matching, changed, absent and legacy
// second-precision ownership without making a real Discord request.
func TestOwnedTimeoutChecksLiveExpiry(t *testing.T) {
	until := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
	for _, scenario := range []string{"match", "changed", "absent", "expired", "legacy", "new_exact_second", "missing", "unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			expected := until.Format(time.RFC3339Nano)
			live := until
			if scenario == "changed" {
				live = live.Add(time.Minute)
			}
			if scenario == "legacy" {
				expected = until.Format(time.RFC3339)
			}
			if scenario == "new_exact_second" {
				expected = until.Truncate(time.Second).Format("2006-01-02T15:04:05.000Z07:00")
				live = until.Truncate(time.Second).Add(500 * time.Millisecond)
			}
			if scenario == "missing" {
				expected = ""
			}
			if scenario == "expired" {
				live = time.Now().Add(-time.Minute)
			}
			patches := 0
			session, _ := discordgo.New("Bot test")
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				body := "{}"
				status := 200
				if request.Method == http.MethodPatch {
					patches++
				} else {
					payload := map[string]any{"user": map[string]string{"id": "member"}, "communication_disabled_until": live.Format(time.RFC3339Nano)}
					if scenario == "absent" {
						payload["communication_disabled_until"] = nil
					}
					encoded, _ := json.Marshal(payload)
					body = string(encoded)
					if scenario == "unavailable" {
						status = 403
						body = `{"code":50013,"message":"denied"}`
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}
			response, err := (&Bot{Session: session}).RemoveOwnedTimeout(context.Background(), "guild", "member", expected, "reason")
			shouldPatch := scenario == "match" || scenario == "legacy"
			shouldError := scenario == "changed" || scenario == "new_exact_second" || scenario == "missing" || scenario == "unavailable"
			if (err != nil) != shouldError || (patches == 1) != shouldPatch {
				t.Fatalf("response=%v err=%v patches=%d", response, err, patches)
			}
			if scenario == "absent" || scenario == "expired" {
				if response["result"] != "timeout_already_absent" || response["reversal_noop"] != true {
					t.Fatal(response)
				}
			}
		})
	}
}

// TestOwnedBanChecksReasonAndMissingOutcome refuses unrelated/manual ban reasons
// and distinguishes an explicitly missing ban from missing access or guilds.
func TestOwnedBanChecksReasonAndMissingOutcome(t *testing.T) {
	for _, scenario := range []string{"match", "changed", "missing_reason", "absent", "unknown_guild", "permission"} {
		t.Run(scenario, func(t *testing.T) {
			deletes := 0
			session, _ := discordgo.New("Bot test")
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				status := 200
				body := `{"user":{"id":"member"},"reason":"Quack case #42: reason"}`
				if request.Method == http.MethodDelete {
					deletes++
					status = 204
					body = ""
				} else {
					switch scenario {
					case "changed":
						body = `{"reason":"Other moderator punishment"}`
					case "missing_reason":
						body = `{"reason":null}`
					case "absent":
						status = 404
						body = `{"code":10026,"message":"Unknown Ban"}`
					case "unknown_guild":
						status = 404
						body = `{"code":10004,"message":"Unknown Guild"}`
					case "permission":
						status = 403
						body = `{"code":50013,"message":"Missing Permissions"}`
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})}
			response, err := (&Bot{Session: session}).RemoveOwnedBan(context.Background(), "guild", "member", "Quack case #42: reason", "undo")
			if (err == nil) != (scenario == "match" || scenario == "absent") || (deletes == 1) != (scenario == "match") {
				t.Fatalf("response=%v err=%v deletes=%d", response, err, deletes)
			}
			if scenario == "absent" && (response["result"] != "ban_already_absent" || response["reversal_noop"] != true) {
				t.Fatal(response)
			}
		})
	}
}
