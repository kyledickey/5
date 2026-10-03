package discordbot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// TestStaffDestinationRespectsChosenViewers verifies delivery accepts administrator
// choices while still rejecting destinations belonging to a different server.
func TestStaffDestinationRespectsChosenViewers(t *testing.T) {
	for _, scenario := range []string{"private", "cross-guild", "public", "non-staff-role", "demoted-member"} {
		t.Run(scenario, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}, {ID: "staff", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel}}}
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "staff", Permissions: discordgo.PermissionModerateMembers}, {ID: "bot-role", Permissions: discordgo.PermissionAdministrator}}}
			switch scenario {
			case "cross-guild":
				channel.GuildID = "other"
			case "public":
				channel.PermissionOverwrites = nil
			case "non-staff-role":
				guild.Roles[1].Permissions = 0
			case "demoted-member":
				channel.PermissionOverwrites[1] = &discordgo.PermissionOverwrite{ID: "former-staff", Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel}
			}
			sends := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				var body any
				switch {
				case request.Method == http.MethodPost:
					sends++
					body = map[string]string{"id": "message"}
				case strings.HasSuffix(request.URL.Path, "/channels/channel"):
					body = channel
				case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
					body = guild
				case strings.HasSuffix(request.URL.Path, "/members/bot"):
					body = &discordgo.Member{User: &discordgo.User{ID: "bot"}, Roles: []string{"bot-role"}}
				case strings.Contains(request.URL.Path, "/members/"):
					body = &discordgo.Member{User: &discordgo.User{ID: "former-staff"}}
				default:
					t.Fatalf("unexpected request %s", request.URL.Path)
				}
				return securityJSONResponse(request, body), nil
			})}
			err := (&Bot{Session: session}).SendAuditMirror(context.Background(), quack.AuditMirrorMessage{DiscordGuildID: "guild", ChannelDiscordID: "channel"})
			if scenario != "cross-guild" {
				if err != nil || sends != 1 {
					t.Fatalf("chosen destination denied: %v", err)
				}
			} else if err == nil || sends != 0 {
				t.Fatalf("cross-guild destination delivered: sends=%d err=%v", sends, err)
			}
		})
	}
}

func TestEvidenceChecksFreshActorChannelAccessBeforeBotRead(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	allowed, reads := false, 0
	guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild", Permissions: discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory}, {ID: "staff", Permissions: discordgo.PermissionModerateMembers}}}
	// Deliberately seed permissive gateway data; authorization must ignore it.
	_ = session.State.GuildAdd(&discordgo.Guild{ID: "guild", OwnerID: "moderator"})
	session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
		var body any
		switch {
		case strings.HasSuffix(request.URL.Path, "/channels/channel"):
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
			if !allowed {
				channel.PermissionOverwrites = []*discordgo.PermissionOverwrite{{ID: "moderator", Type: discordgo.PermissionOverwriteTypeMember, Deny: discordgo.PermissionViewChannel}}
			}
			body = channel
		case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
			body = guild
		case strings.HasSuffix(request.URL.Path, "/members/moderator"):
			body = &discordgo.Member{User: &discordgo.User{ID: "moderator"}, Roles: []string{"staff"}}
		case strings.HasSuffix(request.URL.Path, "/messages/message"):
			reads++
			body = &discordgo.Message{ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: "target"}}
		default:
			t.Fatalf("unexpected request %s", request.URL.Path)
		}
		return securityJSONResponse(request, body), nil
	})}
	bot := &Bot{Session: session}
	ref := quack.DiscordMessageReference{GuildID: "guild", ChannelID: "channel", MessageID: "message", ActorDiscordUserID: "moderator"}
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err == nil || reads != 0 {
		t.Fatal("bot read inaccessible evidence")
	}
	allowed = true
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err != nil || reads != 1 {
		t.Fatalf("readable evidence failed: %v", err)
	}
}

// securityJSONResponse supplies Discord REST fixtures without a network listener.
func securityJSONResponse(request *http.Request, body any) *http.Response {
	encoded, _ := json.Marshal(body)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}
}

// TestStaffDeliveryIgnoresCachedAdministratorPermissions checks each permission
// needed by text and attached records using fresh REST membership and overwrites.
func TestStaffDeliveryIgnoresCachedAdministratorPermissions(t *testing.T) {
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles)
	for _, missing := range []int64{0, discordgo.PermissionViewChannel, discordgo.PermissionSendMessages, discordgo.PermissionReadMessageHistory, discordgo.PermissionAttachFiles} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			_ = session.State.GuildAdd(&discordgo.Guild{ID: "guild", OwnerID: "bot"})
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
				{ID: "bot", Type: discordgo.PermissionOverwriteTypeMember, Allow: required &^ missing, Deny: missing},
			}}
			reads := 0
			session.Client = &http.Client{Transport: requestTransport(func(request *http.Request) (*http.Response, error) {
				var body any
				switch {
				case strings.HasSuffix(request.URL.Path, "/channels/channel"):
					body = channel
				case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
					body = &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}}}
				case strings.HasSuffix(request.URL.Path, "/members/bot"):
					reads++
					body = &discordgo.Member{User: &discordgo.User{ID: "bot"}}
				default:
					t.Fatalf("unexpected request: %s", request.URL.Path)
				}
				return securityJSONResponse(request, body), nil
			})}
			err := (&Bot{Session: session}).ValidateStaffChannel(context.Background(), "guild", "channel")
			if (err == nil) != (missing == 0) || reads != 1 {
				t.Fatalf("missing=%d reads=%d err=%v", missing, reads, err)
			}
		})
	}
}
