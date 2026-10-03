package discordbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestCaseNotificationCopyPreservesRecordedOutcomes protects the existing member
// wording and forbids fabricated expiry or successful-action claims on failure.
func TestCaseNotificationCopyPreservesRecordedOutcomes(t *testing.T) {
	until := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	request := quack.CaseNotificationRequest{GuildName: "The Pond", RuleName: "Repeated spam", Reason: "Please stop repeating messages.", CaseNumber: 12, IncludeAppealInstructions: true, Outcomes: []quack.CaseNotificationOutcome{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded, TimeoutUntil: &until}}}
	want := discordtext.Conversation("timeout", "You’ve been timed out in **The Pond** for **Repeated spam**.", request.Reason, fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.\n\nUse the Appeal decision button below to ask the moderators to review this case.", until.Unix(), until.Unix()), "Case #12")
	if body := renderCaseNotification(request); body != want {
		t.Fatalf("copy changed\n%s\n%s", body, want)
	}
	request.Outcomes[0].Status = model.ActionExecutionFailed
	if body := renderCaseNotification(request); strings.Contains(body, "You’ve been timed out") || strings.Contains(body, "You can chat again") {
		t.Fatal("false success", body)
	}
	request.Outcomes[0].Status = model.ActionExecutionSucceeded
	request.Outcomes[0].TimeoutUntil = nil
	if body := renderCaseNotification(request); strings.Contains(body, "You can chat again") {
		t.Fatal("invented expiry", body)
	}
	request.Reason = "@everyone **quoted**"
	request.Introduction = "intro **literal**"
	request.Footer = "footer @here"
	body := renderCaseNotification(request)
	for _, literal := range []string{request.Reason, request.Introduction, request.Footer} {
		if !strings.Contains(body, discordtext.Plain(literal)) {
			t.Fatal("escaping changed", body)
		}
	}
}

// TestCaseNotificationReturnsRenderedFailureReceipt keeps the exact attempted
// body on errors and classifies every uncertain send as unsafe to repeat.
func TestCaseNotificationReturnsRenderedFailureReceipt(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			sends, opens := 0, 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				if strings.HasSuffix(request.URL.Path, "/messages") {
					sends++
					return nil, errors.New("connection interrupted")
				}
				opens++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"dm"}`)), Request: request}, nil
			})}
			request := quack.CaseNotificationRequest{TargetDiscordUserID: "member", Reason: "saved reason", CaseNumber: 3}
			if prepared {
				request.PreparedChannelDiscordID = "dm"
			}
			receipt, err := (&Bot{Session: session}).SendCaseNotification(context.Background(), request)
			var classified actionmods.DiscordError
			if !errors.As(err, &classified) || !classified.OutcomeUncertain || classified.Retryable {
				t.Fatal("uncertain send classified as safely repeatable", err)
			}
			if receipt.RenderedMessage != renderCaseNotification(request) || receipt.ChannelID != "dm" || receipt.MessageID != "" || sends != 1 {
				t.Fatal("lost failure receipt", receipt, sends)
			}
			if (opens == 0) != prepared {
				t.Fatal("prepared channel routing changed", opens)
			}
		})
	}
}

// TestCaseNotificationBlockedDMReturnsUndeliveredReceipt exercises Discord's
// explicit 403/50007 rejection through the real adapter, retaining the attempted
// message without reporting delivery or treating rejection as an uncertain send.
func TestCaseNotificationBlockedDMReturnsUndeliveredReceipt(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	sends := 0
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/channels/dm/messages") {
			t.Fatalf("unexpected blocked-DM request: %s %s", request.Method, request.URL.Path)
		}
		sends++
		return &http.Response{
			StatusCode: http.StatusForbidden,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"code":50007,"message":"Cannot send messages to this user"}`)),
			Request:    request,
		}, nil
	})}
	request := quack.CaseNotificationRequest{TargetDiscordUserID: "member", PreparedChannelDiscordID: "dm", Reason: "saved reason", CaseNumber: 3}
	receipt, err := (&Bot{Session: session}).SendCaseNotification(context.Background(), request)
	var classified actionmods.DiscordError
	if !errors.As(err, &classified) || classified.Code != "dm_send_permission_or_hierarchy_denied" || classified.Retryable || classified.OutcomeUncertain {
		t.Fatalf("blocked DM was not a definitive terminal rejection: %v", err)
	}
	if strings.Contains(err.Error(), "50007") || strings.Contains(err.Error(), "Cannot send messages to this user") {
		t.Fatalf("raw Discord rejection escaped the adapter: %v", err)
	}
	if receipt.RenderedMessage != renderCaseNotification(request) || receipt.ChannelID != "dm" || receipt.MessageID != "" || sends != 1 {
		t.Fatalf("blocked DM lost its undelivered receipt or was retried: receipt=%+v sends=%d", receipt, sends)
	}
}

// TestCaseNotificationPreparationFailureHasNoSendAmbiguity distinguishes opening
// a channel from creating a message; only the latter risks duplicate delivery.
func TestCaseNotificationPreparationFailureHasNoSendAmbiguity(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/messages") {
			t.Fatal("sent after preparation failure")
		}
		return nil, errors.New("connection interrupted")
	})}
	receipt, err := (&Bot{Session: session}).SendCaseNotification(context.Background(), quack.CaseNotificationRequest{TargetDiscordUserID: "member", Reason: "saved reason"})
	var classified actionmods.DiscordError
	if !errors.As(err, &classified) || classified.OutcomeUncertain || !classified.Retryable || receipt.RenderedMessage == "" {
		t.Fatal("preparation classification", receipt, err)
	}
}

// TestCaseNotificationRemovalDescribesResultingState stays accurate both after
// removal and when the recorded successful action found no punishment remaining.
func TestCaseNotificationRemovalDescribesResultingState(t *testing.T) {
	for _, scenario := range []struct {
		action     model.ActionType
		icon, lead string
	}{
		{model.ActionRemoveTimeout, "untimeout", "Your timeout in **The Pond** has ended."},
		{model.ActionUnbanUser, "unban", "You’re no longer banned from **The Pond**."},
	} {
		request := quack.CaseNotificationRequest{GuildName: "The Pond", RuleName: "Repeated spam", Reason: "Appeal accepted", CaseNumber: 12, Outcomes: []quack.CaseNotificationOutcome{{ActionType: scenario.action, Status: model.ActionExecutionSucceeded}}}
		want := discordtext.Conversation(scenario.icon, scenario.lead, request.Reason, "This updates your case for **Repeated spam**.", "Case #12")
		if got := renderCaseNotification(request); got != want {
			t.Fatalf("inaccurate removal copy: %s; want %s", got, want)
		}
	}
}

// TestNotificationAdaptersSendApplicationText checks real REST serialization
// for both uploaded catalogs without delivering a DM or invoking enforcement.
func TestNotificationAdaptersSendApplicationText(t *testing.T) {
	for _, appID := range []string{"968198214450831370", "819019613371236432"} {
		t.Run(appID, func(t *testing.T) {
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			session.State.User = &discordgo.User{ID: appID}
			body := discordtext.Conversation("timeout", "You’ve been timed out in **The Pond**.", "A reason.", "", "Case #12")
			count := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				response := `{"id":"dm-channel"}`
				if strings.HasSuffix(request.URL.Path, "/messages") {
					count++
					var payload struct {
						Content         string
						Embeds          []json.RawMessage
						Components      []json.RawMessage
						AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					expectedBody := body
					if count == 3 {
						expectedBody = renderCaseNotification(quack.CaseNotificationRequest{GuildName: "The Pond", Reason: "A reason.", CaseNumber: 12, IncludeAppealInstructions: true, AppealControl: true, Outcomes: []quack.CaseNotificationOutcome{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}}})
					}
					if payload.Content != discordtext.Resolve(expectedBody, appID) || len(payload.Embeds) != 0 || payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
						t.Errorf("invalid text DM: %+v", payload)
					}
					if count == 3 && len(payload.Components) != 1 {
						t.Error("case DM lost its appeal button")
					}
					response = `{"id":"sent-message"}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
			})}
			bot := &Bot{Session: session}
			if _, err := bot.SendDM(context.Background(), "member", body); err != nil {
				t.Fatal(err)
			}
			if _, err := bot.SendPreparedDM(context.Background(), "dm-channel", body); err != nil {
				t.Fatal(err)
			}
			if _, err := bot.SendCaseNotification(context.Background(), quack.CaseNotificationRequest{TargetDiscordUserID: "member", PreparedChannelDiscordID: "dm-channel", DashboardBaseURL: "https://dashboard.example", GuildID: "guild", CaseID: "case", GuildName: "The Pond", Reason: "A reason.", CaseNumber: 12, IncludeAppealInstructions: true, AppealControl: true, Outcomes: []quack.CaseNotificationOutcome{{ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionSucceeded}}}); err != nil {
				t.Fatal(err)
			}
			adapter := &AppealNotificationAdapter{Session: session}
			if _, err := adapter.SendAppealMemberNotification(context.Background(), "member", quack.AppealMemberNotification{LegacyBody: body}); err != nil {
				t.Fatal(err)
			}
			if count != 4 {
				t.Fatalf("expected one message per delivery, got %d", count)
			}
		})
	}
}
