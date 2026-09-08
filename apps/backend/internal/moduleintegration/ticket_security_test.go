package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ticketAuthorityStore keeps the regression independent of gateway state and
// supplies only the durable attribution writes performed by live resolution.
type ticketAuthorityStore struct{ quack.GuildRepository }

func (ticketAuthorityStore) UpsertGuild(context.Context, model.UpsertGuildParams) (*model.Guild, error) {
	return &model.Guild{ULIDModel: model.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild"}, nil
}
func (ticketAuthorityStore) UpsertStaffMember(context.Context, model.UpsertStaffMemberParams) (*model.StaffMember, error) {
	return &model.StaffMember{DiscordUserID: "member"}, nil
}

// ticketAuthorityDiscord exposes a demotion which has not reached gateway cache.
type ticketAuthorityDiscord struct {
	quack.DiscordClient
	calls int
}

func (d *ticketAuthorityDiscord) GuildAuthorization(context.Context, string, string, string) (*quack.DiscordGuildAuthorization, error) {
	d.calls++
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: "guild", OwnerID: "owner"},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: "member", Present: true},
		Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "bot", Present: true},
	}, nil
}

func TestTicketActorUsesLiveGuildAuthority(t *testing.T) {
	discord := &ticketAuthorityDiscord{}
	r := &Runtime{services: &quack.Services{Guilds: quack.NewGuildService(ticketAuthorityStore{}, discord)}}
	actor, err := r.ticketActor(ui.Context{Context: context.Background(), Interaction: &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{
			User: &discordgo.User{ID: "member"}, Permissions: discordgo.PermissionAdministrator,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if discord.calls != 1 || actor.CanManage || actor.CanModerate {
		t.Fatalf("stale interaction granted authority: %+v, live calls=%d", actor, discord.calls)
	}
}

// ticketRoundTripper exercises the Discord transport without binding a socket.
type ticketRoundTripper func(*http.Request) (*http.Response, error)

func (f ticketRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTicketThreadRepairPreservesCurrentStaffAndRemovesFormerStaff(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	var removed, added []string
	session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := ""
		switch request.Method {
		case http.MethodGet:
			switch {
			case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
				body = `{"id":"guild","roles":[{"id":"staff-role","permissions":"1099511627776"}]}`
			case strings.HasSuffix(request.URL.Path, "/members/current-staff"):
				body = `{"user":{"id":"current-staff"},"roles":["staff-role"]}`
			case strings.HasSuffix(request.URL.Path, "/members/former-staff"):
				body = `{"user":{"id":"former-staff"},"roles":[]}`
			case strings.Contains(request.URL.Path, "/channels/thread/thread-members"):
				body = `[{"user_id":"owner"},{"user_id":"bot"},{"user_id":"former-staff"},{"user_id":"current-staff"}]`
			default:
				t.Fatalf("unexpected read: %s", request.URL.Path)
			}
		case http.MethodPut:
			added = append(added, request.URL.Path)
		case http.MethodDelete:
			removed = append(removed, request.URL.Path)
		default:
			t.Fatalf("unexpected request %s", request.Method)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client := ticketDiscordClient{session: session}
	if err := client.syncTicketThreadMembers(context.Background(), "guild", "thread", "owner"); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.HasSuffix(removed[0], "/former-staff") || len(added) != 0 {
		t.Fatalf("unexpected invitation removals: %v", removed)
	}
}

type appealDestinationStore struct{ quack.Repository }

func (appealDestinationStore) GetGuildSettings(context.Context, string) (*model.GuildSettings, error) {
	return &model.GuildSettings{AuditMirrorChannelDiscordID: "audit-channel", AppealQueueChannelDiscordID: "channel"}, nil
}
func (appealDestinationStore) GetGuildByID(context.Context, string) (*model.Guild, error) {
	return &model.Guild{DiscordGuildID: "discord-guild"}, nil
}

type rejectingAppealDestination struct{ guildID, channelID string }

func (v *rejectingAppealDestination) ValidateStaffChannel(_ context.Context, guildID, channelID string) error {
	v.guildID, v.channelID = guildID, channelID
	return errors.New("destination is public")
}

func TestAppealStaffDestinationRevalidatesPrivacy(t *testing.T) {
	validator := &rejectingAppealDestination{}
	resolver := appealStaffChannelResolver{repository: appealDestinationStore{}, validator: validator}
	if channel, err := resolver.AppealStaffChannel(context.Background(), "internal-guild"); err == nil || channel != "" {
		t.Fatalf("unsafe appeal destination accepted: %q, %v", channel, err)
	}
	if validator.guildID != "discord-guild" || validator.channelID != "channel" {
		t.Fatalf("incorrect destination identity: %+v", validator)
	}
}

func TestTicketCreationAlwaysUsesPrivateThread(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ticket-thread-setting?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	{
		created := false
		session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
			body := `{"id":"entry","guild_id":"guild","parent_id":"category"}`
			if request.Method == http.MethodPost {
				created = true
				var payload struct {
					Type      discordgo.ChannelType `json:"type"`
					Invitable bool                  `json:"invitable"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(request.URL.Path, "/channels/entry/threads") || payload.Type != discordgo.ChannelTypeGuildPrivateThread || payload.Invitable {
					t.Fatalf("unexpected thread creation: %s %+v", request.URL.Path, payload)
				}
				body = `{"id":"ticket"}`
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		client := ticketDiscordClient{session: session, resolver: guildResolver{db: db}}
		id, err := client.CreatePrivateTicketChannel(context.Background(), "internal-guild", "owner", tickets.Settings{EntryChannelDiscordID: "entry"})
		if err != nil || id != "ticket" || !created {
			t.Fatalf("ticket creation: %s %v", id, err)
		}
	}
}

// changingAppealDestination models configuration edits between worker deliveries.
type changingAppealDestination struct {
	quack.Repository
	queue string
}

func (r *changingAppealDestination) GetGuildSettings(context.Context, string) (*model.GuildSettings, error) {
	return &model.GuildSettings{AppealQueueChannelDiscordID: r.queue, AuditMirrorChannelDiscordID: "audit"}, nil
}
func (r *changingAppealDestination) GetGuildByID(context.Context, string) (*model.Guild, error) {
	return &model.Guild{DiscordGuildID: "guild"}, nil
}

type acceptingAppealDestination struct{}

func (acceptingAppealDestination) ValidateStaffChannel(context.Context, string, string) error {
	return nil
}

// TestAppealQueueConfigurationTakesEffectWithoutRestart checks the live resolver
// never falls back to the audit channel or caches a former queue destination.
func TestAppealQueueConfigurationTakesEffectWithoutRestart(t *testing.T) {
	repository := &changingAppealDestination{queue: "first"}
	resolver := appealStaffChannelResolver{repository: repository, validator: acceptingAppealDestination{}}
	for _, want := range []string{"first", "second", ""} {
		repository.queue = want
		got, err := resolver.AppealStaffChannel(context.Background(), "internal-guild")
		if err != nil || got != want {
			t.Fatalf("queue %q: got %q %v", want, got, err)
		}
	}
}

// TestTicketWelcomeMentionsOnlyOwner keeps the initial conversation immediately
// usable while preventing role or everyone mentions from the bot's greeting.
func TestTicketWelcomeMentionsOnlyOwner(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	sent := false
	session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		sent = true
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/channels/thread/messages") {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Content         string                           `json:"content"`
			AllowedMentions discordgo.MessageAllowedMentions `json:"allowed_mentions"`
			Components      []json.RawMessage                `json:"components"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Content, "<@owner>") || !strings.Contains(payload.Content, "Feel free to tell us what's up") || len(payload.Components) != 1 {
			t.Fatalf("missing greeting or controls: %+v", payload)
		}
		if len(payload.AllowedMentions.Parse) != 0 || len(payload.AllowedMentions.Users) != 1 || payload.AllowedMentions.Users[0] != "owner" {
			t.Fatalf("unsafe greeting mentions: %+v", payload.AllowedMentions)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"welcome"}`))}, nil
	})}
	if err := (ticketDiscordClient{session: session}).SendTicketWelcome(context.Background(), &tickets.Ticket{ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "thread"}); err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("no welcome message")
	}
}
