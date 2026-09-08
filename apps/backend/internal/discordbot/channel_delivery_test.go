package discordbot

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

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
