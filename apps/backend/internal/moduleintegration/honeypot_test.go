package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestHoneypotChannelRequiresEvidenceAndCleanupPermissions prevents enabling a
// trap that can punish members but cannot read or remove the triggering message.
func TestHoneypotChannelRequiresEvidenceAndCleanupPermissions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	all := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages)
	for _, test := range []struct {
		name        string
		deny        int64
		channelType int
		want        string
	}{
		{name: "ready"},
		{name: "missing history", deny: discordgo.PermissionReadMessageHistory, want: "Read Message History"},
		{name: "missing cleanup", deny: discordgo.PermissionManageMessages, want: "Manage Messages"},
		{name: "voice channel", channelType: 2, want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "bot"}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch {
				case strings.HasSuffix(r.URL.Path, "/channels/trap"):
					body = fmt.Sprintf(`{"id":"trap","guild_id":"guild","type":%d,"permission_overwrites":[{"id":"bot","type":1,"deny":"%d","allow":"0"}]}`, test.channelType, test.deny)
				case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					body = fmt.Sprintf(`{"id":"guild","roles":[{"id":"guild","permissions":"%d"}]}`, all)
				case strings.HasSuffix(r.URL.Path, "/members/bot"):
					body = `{"user":{"id":"bot"},"roles":[]}`
				default:
					t.Fatalf("unexpected request: %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			err := (honeypotChannelValidator{session: session, resolver: guildResolver{db: db}}).ValidateHoneypotChannel(context.Background(), "internal", "trap")
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, honeypot.ErrChannelUnavailable) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("incorrect failure: %v", err)
			}
		})
	}
}

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

// TestHoneypotFiltersBeforeDiscordLookup verifies unrelated traffic stays cheap
// while a channel configuration change is honored by the very next message.
func TestHoneypotFiltersBeforeDiscordLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.Guild{}, &modules.Configuration{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	configure := func(raw string) {
		t.Helper()
		if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: raw}); err != nil {
			t.Fatal(err)
		}
	}
	configure(`{"channel_discord_id":"trap","template_id":"template"}`)
	guildReads := 0
	if err := db.Callback().Query().Before("gorm:query").Register("test_gateway_resolution", func(tx *gorm.DB) {
		if tx.Statement.Table == "guilds" {
			guildReads++
			if _, bounded := tx.Statement.Context.Deadline(); !bounded {
				t.Error("gateway guild lookup has no deadline")
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	session, _ := discordgo.New("Bot test")
	reads := 0
	session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
		reads++
		if _, bounded := request.Context().Deadline(); !bounded {
			t.Error("honeypot Discord lookup has no deadline")
		}
		return nil, errors.New("stop after first lookup")
	})}
	runtime := &Runtime{db: db, registry: registry, session: session, HoneypotRuntime: &honeypot.Runtime{}}
	event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "ordinary", Author: &discordgo.User{ID: "member"}}}
	runtime.onMessageCreate(session, event)
	event.ChannelID = "trap"
	event.Author.Bot = true
	runtime.onMessageCreate(session, event)
	if reads != 1 {
		t.Fatalf("ordinary bot must reach live permission lookup: %d reads", reads)
	}
	session.State.User = &discordgo.User{ID: "quack", Bot: true}
	event.Author.ID = "quack"
	runtime.onMessageCreate(session, event)
	if reads != 1 {
		t.Fatal("Quack message reached trigger lookup")
	}
	event.Author.ID = "member"
	event.Author.Bot = false
	event.ChannelID = "ordinary"
	configure(`{"channel_discord_id":"ordinary","template_id":"template"}`)
	runtime.onMessageCreate(session, event)
	if reads != 2 {
		t.Fatalf("new trap configuration not observed: %d reads", reads)
	}
	if guildReads != 4 {
		t.Fatalf("want one guild lookup per message, got %d for four messages", guildReads)
	}
}

// TestHoneypotExemptionUsesGuildModerationAuthority keeps the baseline identical
// to cases and appeals, regardless of trap-channel permission overwrites.
func TestHoneypotExemptionUsesGuildModerationAuthority(t *testing.T) {
	for _, test := range []struct {
		name                     string
		permissions, allow, deny int64
		owner, want              bool
	}{
		{name: "moderator", permissions: discordgo.PermissionModerateMembers, want: true},
		{name: "administrator", permissions: discordgo.PermissionAdministrator, want: true},
		{name: "owner", owner: true, want: true},
		{name: "manage server only", permissions: discordgo.PermissionManageServer},
		{name: "ban only", permissions: discordgo.PermissionBanMembers},
		{name: "channel grant", allow: discordgo.PermissionModerateMembers},
		{name: "channel denial", permissions: discordgo.PermissionModerateMembers, deny: discordgo.PermissionModerateMembers, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: test.permissions}}}
			if test.owner {
				guild.OwnerID = "member"
			}
			channel := &discordgo.Channel{ID: "trap", GuildID: "guild", PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: "member", Type: discordgo.PermissionOverwriteTypeMember, Allow: test.allow, Deny: test.deny}}}
			member := &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"role"}}
			event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "trap", Author: member.User}}
			projection, err := projectHoneypotMessage("internal", event, guild, channel, member, "bot")
			if err != nil || projection.AuthorCanModerate != test.want {
				t.Fatalf("exemption=%v want=%v err=%v", projection.AuthorCanModerate, test.want, err)
			}
		})
	}
}

// TestHoneypotRecoveryRefreshesAuthor checks live staff and self exemptions while
// allowing ordinary third-party bots to use the same normal case preflight path.
func TestHoneypotRecoveryRefreshesAuthor(t *testing.T) {
	for _, scenario := range []struct {
		name, id      string
		bot           bool
		permissions   int64
		messageStatus int
		want          error
	}{
		{name: "ordinary bot", id: "member", bot: true},
		{name: "moderator bot", id: "member", bot: true, permissions: discordgo.PermissionModerateMembers, want: honeypot.ErrExempt},
		{name: "human administrator", id: "member", permissions: discordgo.PermissionAdministrator, want: honeypot.ErrExempt},
		{name: "Quack", id: "quack", bot: true, want: honeypot.ErrExempt},
		{name: "missing source", id: "member", messageStatus: 404, want: honeypot.ErrNotTrigger},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			session, _ := discordgo.New("Bot test")
			session.State.User = &discordgo.User{ID: "quack", Bot: true}
			session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
				if request.Method != http.MethodGet {
					t.Fatal("recovery preparation mutated Discord")
				}
				var body any
				status := http.StatusOK
				switch {
				case strings.HasSuffix(request.URL.Path, "/channels/trap/messages/message"):
					if scenario.messageStatus != 0 {
						status = scenario.messageStatus
						body = map[string]any{"code": 10008}
					} else {
						body = &discordgo.Message{ID: "message", ChannelID: "trap", Author: &discordgo.User{ID: scenario.id, Bot: scenario.bot}}
					}
				case strings.HasSuffix(request.URL.Path, "/channels/trap"):
					body = &discordgo.Channel{ID: "trap", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
				case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
					body = &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: scenario.permissions}}}
				case strings.Contains(request.URL.Path, "/guilds/guild/members/"):
					body = &discordgo.Member{User: &discordgo.User{ID: scenario.id, Bot: scenario.bot}, Roles: []string{"role"}}
				default:
					t.Fatalf("unexpected lookup %s", request.URL.Path)
				}
				raw, _ := json.Marshal(body)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			request := honeypot.ApplyRequest{GuildID: "internal", TemplateID: "template", TargetDiscordUserID: scenario.id, ContextChannelDiscordID: "trap", ContextMessageDiscordID: "message"}
			prepared, err := (honeypotCaseApplier{session: session}).PrepareHoneypotRecovery(context.Background(), request)
			if !errors.Is(err, scenario.want) {
				t.Fatalf("got %v want %v", err, scenario.want)
			}
			if scenario.want == nil && prepared.ContextURL != "https://discord.com/channels/guild/trap/message" {
				t.Fatal("missing canonical evidence URL", prepared)
			}
		})
	}
}

// unattendedTemplateCheck lets the adapter tests supply only the narrow use case.
type unattendedTemplateCheck func(context.Context, string, string) error

// ValidateUnattendedTemplate delegates to the test's scoped policy response.
func (f unattendedTemplateCheck) ValidateUnattendedTemplate(ctx context.Context, guildID, templateID string) error {
	return f(ctx, guildID, templateID)
}

// TestHoneypotTemplateValidatorErrorMapping ensures transient failures do not
// masquerade as policy drift and trigger unavailable-template recovery.
func TestHoneypotTemplateValidatorErrorMapping(t *testing.T) {
	storageErr := errors.New("database unavailable")
	for _, tt := range []struct {
		name         string
		result, want error
	}{
		{name: "compatible"},
		{name: "policy drift", result: fmt.Errorf("%w: required context", quack.ErrUnattendedTemplateUnavailable), want: honeypot.ErrTemplateUnavailable},
		{name: "storage failure", result: storageErr, want: storageErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			validator := honeypotTemplateValidator{templates: unattendedTemplateCheck(func(_ context.Context, guildID, templateID string) error {
				if guildID != "guild" || templateID != "template" {
					t.Fatalf("scope = %q/%q", guildID, templateID)
				}
				return tt.result
			})}
			err := validator.ValidateHoneypotTemplate(context.Background(), "guild", "template")
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.result == storageErr && errors.Is(err, honeypot.ErrTemplateUnavailable) {
				t.Fatal("storage failure classified as policy drift")
			}
		})
	}
	if err := (honeypotTemplateValidator{}).ValidateHoneypotTemplate(context.Background(), "guild", "template"); err == nil || errors.Is(err, honeypot.ErrTemplateUnavailable) {
		t.Fatalf("missing dependency error = %v", err)
	}
}
