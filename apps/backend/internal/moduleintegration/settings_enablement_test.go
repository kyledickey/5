package moduleintegration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/testutil"
)

// TestCoreSettingsReflectNativeModuleSetup verifies both interfaces use the same
// configuration row and rejected enablement cannot partially update core fields.
func TestCoreSettingsReflectNativeModuleSetup(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := repository.BootstrapGuild(ctx, model.BootstrapGuildParams{DiscordGuildID: "guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(repository.DB()), tickets.Descriptor(), generallogging.Descriptor(), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	service := tickets.NewService(registry, tickets.NewStore(repository.DB()), nil)
	if _, err := service.UpdateSettings(ctx, tickets.Actor{GuildID: bootstrap.Guild.ID, DiscordUserID: "owner", CanManage: true}, true, tickets.Settings{EntryChannelDiscordID: "entry", QueueChannelDiscordID: "queue", TranscriptRetentionDays: 30}); err != nil {
		t.Fatal(err)
	}
	settings, err := repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if err != nil || !settings.TicketsEnabled {
		t.Fatalf("native setup invisible: %+v %v", settings, err)
	}
	runtime := &Runtime{registry: registry, honeypotTemplates: honeypotTemplateValidator{templates: quack.NewTemplateService(repository)}}
	guild := &quack.GuildStaffContext{Guild: &bootstrap.Guild, Staff: &model.StaffMember{DiscordUserID: "owner"}, ActorDiscordUserID: "owner", PermissionBits: uint64(discordgo.PermissionManageGuild), Permissions: map[model.PermissionAction]bool{model.PermissionActionGuildSettingsWrite: true, model.PermissionActionGuildSettingsRead: true}}
	core := quack.NewGuildSettingsService(repository).WithModuleEnablementValidator(runtime)
	enable := true
	footer := "must not save"
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{HoneypotEnabled: &enable, NotificationFooter: &footer}); err == nil || !strings.Contains(err.Error(), "/setup") {
		t.Fatalf("missing actionable setup rejection: %v", err)
	}
	settings, _ = repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if settings.NotificationFooter != "" {
		t.Fatal("rejected enablement changed core settings")
	}
	// A configured envelope can still be invalid for enabled=true even when the
	// registry accepts it for disabled storage.
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: modules.Honeypots, ConfigJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{HoneypotEnabled: &enable}); err == nil || !strings.Contains(err.Error(), "channel") {
		t.Fatalf("invalid stored configuration enabled: %v", err)
	}
	disable := false
	result, err := core.Update(ctx, guild, quack.GuildSettingsInput{TicketsEnabled: &disable, HoneypotEnabled: &disable})
	if err != nil || result.TicketsEnabled {
		t.Fatalf("configured disable failed: %+v %v", result, err)
	}
	config, _ := registry.Configuration(ctx, bootstrap.Guild.ID, modules.Tickets)
	if config.Enabled || !strings.Contains(config.ConfigJSON, "entry") {
		t.Fatal("core settings did not preserve canonical module config")
	}
	// Re-enabling a retained logging setup checks current Discord state and
	// leaves the exact module configuration unchanged.
	raw, _ := json.Marshal(generallogging.Defaults().RouteAllTo("log"))
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: modules.GeneralLogging, ConfigJSON: string(raw)}); err != nil {
		t.Fatal(err)
	}
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "bot"}
	reads := 0
	channelGuild := "guild"
	botPermissions := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory | discordgo.PermissionAttachFiles | discordgo.PermissionViewAuditLogs)
	session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
		reads++
		body := ""
		switch {
		case strings.HasSuffix(request.URL.Path, "/channels/log"):
			body = `{"id":"log","guild_id":"` + channelGuild + `","type":0}`
		case strings.HasSuffix(request.URL.Path, "/guilds/guild"):
			body = fmt.Sprintf(`{"id":"guild","roles":[{"id":"guild","permissions":"0"},{"id":"bot-role","permissions":"%d"}]}`, botPermissions)
		case strings.HasSuffix(request.URL.Path, "/members/bot"):
			body = `{"user":{"id":"bot"},"roles":["bot-role"]}`
		default:
			t.Fatalf("unexpected validation request: %s", request.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	runtime.session, runtime.resolver = session, guildResolver{db: repository.DB()}
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{GeneralLoggingEnabled: &enable}); err != nil {
		t.Fatal(err)
	}
	config, _ = registry.Configuration(ctx, bootstrap.Guild.ID, modules.GeneralLogging)
	if !config.Enabled || config.ConfigJSON != string(raw) || reads < 3 {
		t.Fatalf("enable skipped live validation or changed config: %+v reads=%d", config, reads)
	}
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{GeneralLoggingEnabled: &disable}); err != nil {
		t.Fatal(err)
	}
	// A bot with valid destination delivery permissions still needs live guild
	// audit access. Failed enablement must leave both config and core fields intact.
	botPermissions &^= discordgo.PermissionViewAuditLogs
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{GeneralLoggingEnabled: &enable, NotificationFooter: &footer}); err == nil || !strings.Contains(err.Error(), "View Audit Log") {
		t.Fatalf("missing audit access was accepted: %v", err)
	}
	config, _ = registry.Configuration(ctx, bootstrap.Guild.ID, modules.GeneralLogging)
	settings, _ = repository.GetGuildSettings(ctx, bootstrap.Guild.ID)
	if config.Enabled || settings.NotificationFooter != "" || config.ConfigJSON != string(raw) {
		t.Fatal("rejected audit permission changed module or core settings")
	}
	botPermissions |= discordgo.PermissionViewAuditLogs
	channelGuild = "other-guild"
	if _, err := core.Update(ctx, guild, quack.GuildSettingsInput{GeneralLoggingEnabled: &enable, NotificationFooter: &footer}); err == nil {
		t.Fatal("cross-guild destination enabled")
	}
	config, _ = registry.Configuration(ctx, bootstrap.Guild.ID, modules.GeneralLogging)
	if config.Enabled {
		t.Fatal("failed live validation enabled module")
	}

}
