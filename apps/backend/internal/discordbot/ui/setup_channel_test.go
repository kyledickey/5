package ui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// setupTransport exercises setup's Discord boundary without creating real channels.
type setupTransport func(*http.Request) (*http.Response, error)

// RoundTrip delegates to the scenario's REST fixture.
func (f setupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestSetupChannelSelection prevents accidental edits or duplicate creation when
// an administrator supplies a destination, reruns setup, or Discord is unavailable.
func TestSetupChannelSelection(t *testing.T) {
	for _, scenario := range []struct {
		name, specified, configured string
		status, wantCreates         int
	}{
		{"explicit", "chosen", "old", 200, 0},
		{"reuse", "", "old", 200, 0},
		{"first setup", "", "", 200, 1},
		{"deleted", "", "old", 404, 1},
		{"forbidden", "", "old", 403, 0},
		{"unavailable", "", "old", 503, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			reads, creates := 0, 0
			session.Client = &http.Client{Transport: setupTransport(func(r *http.Request) (*http.Response, error) {
				reads++
				status := 200
				var body any
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/old"):
					status = scenario.status
					body = &discordgo.Channel{ID: "old", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
					if status != 200 {
						body = map[string]any{"code": 10003, "message": "missing or unavailable"}
					}
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					body = &discordgo.Guild{ID: "guild"}
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/guilds/guild/channels"):
					creates++
					var data discordgo.GuildChannelCreateData
					if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
						t.Fatal(err)
					}
					if data.Name != "appeals" || data.Topic == "" || len(data.PermissionOverwrites) != 2 {
						t.Fatalf("invalid creation: %+v", data)
					}
					body = &discordgo.Channel{ID: "new", GuildID: "guild"}
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/new/messages"):
					var message discordgo.MessageSend
					if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(message.Content, "# Appeals\n") || message.AllowedMentions == nil {
						t.Fatalf("invalid intro: %+v", message)
					}
					body = &discordgo.Message{ID: "intro"}
				default:
					t.Fatalf("unexpected mutation or request: %s %s", r.Method, r.URL.Path)
				}
				encoded, _ := json.Marshal(body)
				return &http.Response{
					StatusCode: status,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(string(encoded))),
					Request:    r,
				}, nil
			})}
			id, err := SetupChannel(context.Background(), session, "guild", scenario.specified, scenario.configured, "appeals", SetupStaffChannel)
			wantErr := scenario.status == 403 || scenario.status == 503
			if (err != nil) != wantErr || creates != scenario.wantCreates {
				t.Fatalf("id=%s err=%v creates=%d", id, err, creates)
			}
			if scenario.specified != "" && (reads != 0 || id != scenario.specified) {
				t.Fatal("explicit choice was inspected or replaced")
			}
			if scenario.name == "reuse" && id != "old" {
				t.Fatal("configured channel replaced")
			}
		})
	}
}

// TestCreatedChannelEffectivePermissions checks the resulting Discord permissions
// for members, moderators and the bot, including private-ticket thread capabilities.
func TestCreatedChannelEffectivePermissions(t *testing.T) {
	read := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	guild := &discordgo.Guild{
		ID:    "guild",
		Roles: []*discordgo.Role{{ID: "guild"}, {ID: "mod", Permissions: discordgo.PermissionModerateMembers}},
		Members: []*discordgo.Member{
			{User: &discordgo.User{ID: "member"}},
			{User: &discordgo.User{ID: "moderator"}, Roles: []string{"mod"}},
			{User: &discordgo.User{ID: "bot"}},
		},
	}
	for _, kind := range []SetupChannelKind{SetupStaffChannel, SetupTicketEntry, SetupHoneypotChannel} {
		channel := &discordgo.Channel{
			ID:                   "channel",
			GuildID:              guild.ID,
			Type:                 discordgo.ChannelTypeGuildText,
			PermissionOverwrites: setupChannelPermissions(guild, "bot", kind),
		}
		guild.Channels = []*discordgo.Channel{channel}
		state := discordgo.NewState()
		if err := state.GuildAdd(guild); err != nil {
			t.Fatal(err)
		}
		permissions := func(id string) int64 {
			p, err := state.UserChannelPermissions(id, channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		member, mod, bot := permissions("member"), permissions("moderator"), permissions("bot")
		if bot&read != read || bot&discordgo.PermissionSendMessages == 0 {
			t.Fatal("bot cannot deliver")
		}
		switch kind {
		case SetupStaffChannel:
			if member&discordgo.PermissionViewChannel != 0 || mod&read != read {
				t.Fatal("staff destination permissions incorrect")
			}
		case SetupTicketEntry:
			required := int64(discordgo.PermissionCreatePrivateThreads | discordgo.PermissionManageThreads | discordgo.PermissionSendMessagesInThreads)
			if member&read != read || member&discordgo.PermissionSendMessages != 0 ||
				member&discordgo.PermissionSendMessagesInThreads == 0 || bot&required != required {
				t.Fatal("ticket entry cannot support private conversations")
			}
		case SetupHoneypotChannel:
			if member&discordgo.PermissionSendMessages == 0 || bot&discordgo.PermissionManageMessages == 0 {
				t.Fatal("trap cannot receive and remove messages")
			}
		}
	}
}

// TestSetupChannelIntroFailureRetainsCreatedDestination ensures a failed welcome
// cannot turn a successful channel creation into another creation on setup retry.
func TestSetupChannelIntroFailureRetainsCreatedDestination(t *testing.T) {
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	creates, intros := 0, 0
	session.Client = &http.Client{Transport: setupTransport(func(r *http.Request) (*http.Response, error) {
		code, body := 200, `{"id":"guild"}`
		switch {
		case strings.HasSuffix(r.URL.Path, "/guilds/guild/channels"):
			creates++
			body = `{"id":"created","guild_id":"guild","type":0}`
		case strings.HasSuffix(r.URL.Path, "/channels/created/messages"):
			intros++
			code, body = 503, `{"code":0,"message":"Unavailable"}`
		case strings.HasSuffix(r.URL.Path, "/channels/created"):
			body = `{"id":"created","guild_id":"guild","type":0}`
		case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
		default:
			t.Fatalf("unexpected request: %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: code,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}
	id, err := SetupChannel(context.Background(), session, "guild", "", "", "appeals", SetupStaffChannel)
	if err != nil || id != "created" {
		t.Fatalf("lost creation receipt: %s, %v", id, err)
	}
	if _, err := SetupChannel(context.Background(), session, "guild", "", id, "appeals", SetupStaffChannel); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || intros != 1 {
		t.Fatalf("repeated creation or intro: %d, %d", creates, intros)
	}
}

// TestChannelPanelsSupplyTheirOwnWelcome keeps entry controls and warnings as the
// only welcome while giving every newly-created channel a useful description.
func TestChannelPanelsSupplyTheirOwnWelcome(t *testing.T) {
	for _, kind := range []SetupChannelKind{SetupTicketEntry, SetupHoneypotChannel} {
		topic, intro := setupChannelPresentation("channel", kind)
		if topic == "" || intro != "" {
			t.Fatalf("entry presentation: %q %q", topic, intro)
		}
	}
}
