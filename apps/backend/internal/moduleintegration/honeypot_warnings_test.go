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
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestHoneypotCounterRepairsOnlyMissingWarnings verifies real edit/send transport
// preserves configured copy and stores the replacement receipt for later updates.
func TestHoneypotCounterRepairsOnlyMissingWarnings(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			ctx := context.Background()
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&model.Guild{}); err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(modules.SchemaTypes()...); err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(honeypot.SchemaTypes()...); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.Guild{ULIDModel: model.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
				t.Fatal(err)
			}
			registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
			if err != nil {
				t.Fatal(err)
			}
			settings := honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Custom warning", WarningMessageID: "old"}
			raw, _ := json.Marshal(settings)
			if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "internal", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(raw)}); err != nil {
				t.Fatal(err)
			}
			service := honeypot.NewService(registry, honeypot.NewStore(db), nil, nil, nil, nil)
			session, _ := discordgo.New("Bot test")
			posts, requests := 0, 0
			session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
				requests++
				code, body := 200, `{"id":"trap","guild_id":"guild","type":0}`
				if request.Method == http.MethodPatch {
					code = status
					body = `{"id":"old"}`
					if status != 200 {
						body = `{"code":10008,"message":"Unknown Message"}`
					}
					if status == 500 {
						body = `{"code":0,"message":"Server error"}`
					}
				}
				if request.Method == http.MethodPost {
					posts++
					payload, _ := io.ReadAll(request.Body)
					if !strings.Contains(string(payload), "Custom warning") || !strings.Contains(string(payload), "0 incidents caught") {
						t.Fatalf("wrong warning: %s", payload)
					}
					body = `{"id":"replacement"}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			counter := honeypotCounter{session: session, service: service, resolver: guildResolver{db: db}}
			if err := counter.WarningDeleted(ctx, "internal", "trap", []string{"unrelated"}); err != nil || requests != 0 {
				t.Fatalf("unrelated deletion triggered delivery: %v", err)
			}
			err = counter.WarningDeleted(ctx, "internal", "trap", []string{"another", "old"})
			if err != nil {
				t.Fatal(err)
			}
			err = counter.refresh(ctx, "internal")
			if (err == nil) != (status != 500) || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("unexpected repair: posts=%d err=%v", posts, err)
			}
			saved, _, err := service.Settings(ctx, honeypot.Actor{GuildID: "internal", CanManage: true})
			want := "old"
			if status == 404 {
				want = "replacement"
			}
			if status == 404 {
				before := requests
				if err := counter.WarningDeleted(ctx, "internal", "trap", []string{"old"}); err != nil || requests != before {
					t.Fatalf("stale deletion repeated repair: %v", err)
				}
			}
			if err != nil || saved.WarningMessageID != want || saved.WarningText != "Custom warning" {
				t.Fatalf("receipt: %+v %v", saved, err)
			}
		})
	}
}

// presentationFixture creates durable configuration around a controlled Discord
// transport; reconstructing the counter does not recreate its persisted work.
func presentationFixture(t *testing.T) (*honeypotCounter, *gorm.DB, *modules.Registry) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(append(modules.SchemaTypes(), honeypot.SchemaTypes()...)...); err != nil {
		t.Fatal(err)
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

// warningPolicyStub supplies current outcomes without Discord or database calls.
type warningPolicyStub struct {
	actions []model.ActionType
	err     error
}

// UnattendedTemplateActions returns the configured test policy.
func (s warningPolicyStub) UnattendedTemplateActions(context.Context, string, string) ([]model.ActionType, error) {
	return s.actions, s.err
}

// TestGeneratedHoneypotWarningMatchesPolicy prevents claiming a ban for a timeout
// or for a level that merely records a case, including escalating policies.
func TestGeneratedHoneypotWarningMatchesPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []model.ActionType
		want    string
	}{
		{"ban", []model.ActionType{model.ActionBanUser}, "will ban you from this server"},
		{"timeout", []model.ActionType{model.ActionTimeoutUser}, "will time you out"},
		{"kick", []model.ActionType{model.ActionKickUser}, "will kick you from this server"},
		{"warning", []model.ActionType{model.ActionSendDM}, "will send you a warning by DM"},
		{"case only", []model.ActionType{""}, "will record a moderation case"},
		{"escalation", []model.ActionType{model.ActionTimeoutUser, model.ActionBanUser}, "can time you out or ban you from this server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, saved := range []string{"", legacyHoneypotWarning} {
				got, err := resolveHoneypotWarning(context.Background(), warningPolicyStub{actions: tc.actions}, "guild", honeypot.Settings{WarningText: saved})
				if err != nil || !strings.Contains(got, tc.want) {
					t.Fatalf("got %q, %v", got, err)
				}
			}
		})
	}
}

// TestCustomHoneypotWarningSurvivesPolicyReadFailure preserves explicit admin copy
// and refuses to invent a default punishment when current policy cannot be read.
func TestCustomHoneypotWarningSurvivesPolicyReadFailure(t *testing.T) {
	policy := warningPolicyStub{err: errors.New("unavailable")}
	custom := "# Custom warning\nPlease stay out."
	got, err := resolveHoneypotWarning(context.Background(), policy, "guild", honeypot.Settings{WarningText: custom})
	if err != nil || got != custom {
		t.Fatalf("custom copy changed: %q, %v", got, err)
	}
	if _, err := resolveHoneypotWarning(context.Background(), policy, "guild", honeypot.Settings{}); err == nil {
		t.Fatal("invented a punishment without current policy")
	}
}
