package discordbot

import (
	"context"
	"encoding/json"
	"github.com/bwmarrin/discordgo"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// TestAppealDecisionCopyPreservesSnapshots keeps version-one wording and literal
// reviewer-supplied reason text equivalent to the former stored rendered bodies.
func TestAppealDecisionCopyPreservesSnapshots(t *testing.T) {
	for _, item := range []struct {
		status           model.AppealStatus
		icon, lead, next string
	}{
		{model.AppealStatusAccepted, "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."},
		{model.AppealStatusRejected, "decline", "Your appeal was declined.", ""},
		{model.AppealStatusNeedsInformation, "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."},
	} {
		intent := &model.AppealDecisionIntent{Version: 1, Status: item.status, Reason: "**Reason** @everyone"}
		want := discordtext.Conversation(item.icon, item.lead, discordtext.Plain(intent.Reason), item.next, "")
		if item.status == model.AppealStatusAccepted {
			intent.RejoinURL = "https://discord.gg/original"
			want += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + intent.RejoinURL
		}
		if got := appealMemberNotificationBody(quack.AppealMemberNotification{Intent: intent}); got != want {
			t.Fatal(got, want)
		}
	}
	legacy := "legacy preserved copy"
	if got := appealMemberNotificationBody(quack.AppealMemberNotification{LegacyBody: legacy}); got != legacy {
		t.Fatal("legacy body changed", got)
	}
}

// TestAppealRejoinButtonDelivery verifies accepted typed intent alone produces
// the native link button, and the actual REST payload retains existing copy.
func TestAppealRejoinButtonDelivery(t *testing.T) {
	for _, kind := range []string{"accepted", "unconfigured", "rejected", "legacy"} {
		t.Run(kind, func(t *testing.T) {
			notice := quack.AppealMemberNotification{Intent: &model.AppealDecisionIntent{Version: 1, Status: model.AppealStatusAccepted, Reason: "Reviewed reason", RejoinURL: "https://discord.gg/saved-invite"}}
			switch kind {
			case "unconfigured":
				notice.Intent.RejoinURL = ""
			case "rejected":
				notice.Intent.Status = model.AppealStatusRejected
			case "legacy":
				notice.Intent = nil
				notice.LegacyBody = "Exact legacy text https://discord.gg/legacy"
			}
			message := appealMemberNotificationMessage(notice)
			if kind == "accepted" {
				if len(message.Components) != 1 {
					t.Fatal("button missing")
				}
				button := message.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
				if button.Style != discordgo.LinkButton || button.Label != "Rejoin Server" || button.URL != notice.Intent.RejoinURL || button.CustomID != "" {
					t.Fatalf("wrong rejoin button %+v", button)
				}
				if !strings.Contains(message.Content, "once any ban has been removed") {
					t.Fatal("removal uncertainty lost")
				}
			} else if len(message.Components) != 0 {
				t.Fatal("unexpected rejoin control")
			}
			if kind == "legacy" && appealMemberNotificationBody(notice) != notice.LegacyBody {
				t.Fatal("legacy body changed")
			}
			session, err := discordgo.New("Bot test")
			if err != nil {
				t.Fatal(err)
			}
			sends := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				response := `{"id":"dm"}`
				if strings.HasSuffix(request.URL.Path, "/messages") {
					sends++
					var payload struct {
						Content         string
						Components      []json.RawMessage
						AllowedMentions *discordgo.MessageAllowedMentions `json:"allowed_mentions"`
					}
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.Content != message.ForApplication("").Content || len(payload.Components) != len(message.Components) || payload.AllowedMentions == nil || len(payload.AllowedMentions.Parse) != 0 {
						t.Fatalf("REST presentation changed %+v", payload)
					}
					if kind == "accepted" && !strings.Contains(string(payload.Components[0]), "https://discord.gg/saved-invite") {
						t.Fatal("saved invite lost in transport")
					}
					response = `{"id":"sent"}`
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
			})}
			if _, err := (&AppealNotificationAdapter{Session: session}).SendAppealMemberNotification(context.Background(), "member", notice); err != nil {
				t.Fatal(err)
			}
			if sends != 1 {
				t.Fatal("expected one DM", sends)
			}
		})
	}
}
