package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// presentationFixture creates durable configuration around a controlled Discord
// transport; reconstructing the counter does not recreate its persisted work.
func presentationFixture(t *testing.T) (*honeypotCounter, *gorm.DB, *modules.Registry) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range []modules.Migration{modules.RegistryMigration(), honeypot.Migration()} {
		if err := migration.Apply(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AutoMigrate(&model.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Original", WarningMessageID: "old"})
	if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(raw)}); err != nil {
		t.Fatal(err)
	}
	session, _ := discordgo.New("Bot test")
	service := honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil)
	return &honeypotCounter{session: session, service: service, resolver: guildResolver{db: db}}, db, registry
}

// TestWarningPresentationRetriesAfterRestart verifies an unavailable edit stays
// durable, refreshes current copy after reconstruction, and then retires work.
func TestWarningPresentationRetriesAfterRestart(t *testing.T) {
	counter, db, registry := presentationFixture(t)
	ctx := context.Background()
	failed := true
	edits := 0
	latest := ""
	counter.session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		code, body := 200, `{"id":"trap","guild_id":"guild"}`
		if r.Method == http.MethodPatch {
			edits++
			data, _ := io.ReadAll(r.Body)
			latest = string(data)
			body = `{"id":"old"}`
			if failed {
				code = 500
				body = `{"code":0,"message":"temporary"}`
			}
		}
		if r.Method == http.MethodPost {
			t.Fatal("edit retry created warning")
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	for range 5 {
		if err := counter.IncidentCreated(ctx, "internal"); err != nil {
			t.Fatal(err)
		}
	}
	if edits != 0 {
		t.Fatal("enforcement observer performed HTTP")
	}
	if err := counter.processPresentation(ctx); err != nil {
		t.Fatal(err)
	}
	if edits != 1 {
		t.Fatal(edits)
	}
	if err := counter.processPresentation(ctx); err != nil || edits != 1 {
		t.Fatal("no retry backoff", edits, err)
	}
	raw, _ := json.Marshal(honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Changed", WarningMessageID: "old"})
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(raw)}); err != nil {
		t.Fatal(err)
	}
	failed = false
	if err := db.Model(&honeypot.WarningRefresh{}).Where("guild_id = ?", "internal").Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	restarted := &honeypotCounter{session: counter.session, service: honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil), resolver: counter.resolver}
	if err := restarted.processPresentation(ctx); err != nil || edits != 2 || !strings.Contains(latest, "Changed") {
		t.Fatal(edits, latest, err)
	}
	rows, err := restarted.service.WarningRefreshes(ctx, time.Now().Add(time.Hour))
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	ids, err := restarted.service.ConfiguredWarningGuilds(ctx, "")
	if err != nil || len(ids) != 1 || ids[0] != "internal" {
		t.Fatal(ids, err)
	}
	ids, err = restarted.service.ConfiguredWarningGuilds(ctx, "internal")
	if err != nil || len(ids) != 0 {
		t.Fatal(ids, err)
	}
}

// TestWarningReplacementUncertaintySurvivesRestart ensures a lost POST response
// never becomes another replacement, even when startup requests a fresh refresh.
func TestWarningReplacementUncertaintySurvivesRestart(t *testing.T) {
	counter, db, registry := presentationFixture(t)
	ctx := context.Background()
	posts := 0
	counter.session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		code, body := 200, `{"id":"trap","guild_id":"guild"}`
		if r.Method == http.MethodPatch {
			code = 404
			body = `{"code":10008,"message":"Unknown Message"}`
		}
		if r.Method == http.MethodPost {
			posts++
			return nil, errors.New("response lost")
		}
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	if err := counter.refresh(ctx, "internal"); err == nil {
		t.Fatal("unknown send succeeded")
	}
	restarted := &honeypotCounter{session: counter.session, service: honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil), resolver: counter.resolver}
	if err := restarted.IncidentCreated(ctx, "internal"); err != nil {
		t.Fatal(err)
	}
	if err := restarted.processPresentation(ctx); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatal("duplicate replacement", posts)
	}
}

// TestWarningStartupAndDisabledRetirement verifies startup reaches a configured
// warning without an incident and a disabled guild retires queued work silently.
func TestWarningStartupAndDisabledRetirement(t *testing.T) {
	counter, _, registry := presentationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	edits := 0
	counter.session.Client = &http.Client{Transport: ticketRoundTripper(func(r *http.Request) (*http.Response, error) {
		body := `{"id":"trap","guild_id":"guild"}`
		if r.Method == http.MethodPatch {
			edits++
			body = `{"id":"old"}`
			cancel()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	counter.RunPresentation(ctx)
	if edits != 1 {
		t.Fatal("startup did not reconcile", edits)
	}
	if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: false, ConfigJSON: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := counter.processPresentation(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := counter.service.WarningRefreshes(context.Background(), time.Now().Add(time.Hour))
	if err != nil || len(rows) != 0 || edits != 1 {
		t.Fatal(rows, edits, err)
	}
}
