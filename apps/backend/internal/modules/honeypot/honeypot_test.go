package honeypot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type validatorFake struct {
	mu                      sync.Mutex
	channelErr, templateErr error
	channels, templates     []string
}

func (f *validatorFake) ValidateHoneypotChannel(_ context.Context, guildID, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = append(f.channels, guildID+":"+channelID)
	return f.channelErr
}

func (f *validatorFake) ValidateHoneypotTemplate(_ context.Context, guildID, templateID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templates = append(f.templates, guildID+":"+templateID)
	return f.templateErr
}

type applierFake struct {
	mu       sync.Mutex
	err      error
	requests []honeypot.ApplyRequest
}

func (f *applierFake) ApplyHoneypotCase(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.err != nil {
		return honeypot.ApplyResult{}, f.err
	}
	return honeypot.ApplyResult{CaseID: fmt.Sprintf("case-%d", len(f.requests))}, nil
}

func (f *applierFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

type auditRecorder struct {
	mu     sync.Mutex
	events []modules.AuditEvent
}

func (a *auditRecorder) RecordModuleAudit(_ context.Context, event modules.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return nil
}

type fixture struct {
	db        *gorm.DB
	registry  *modules.Registry
	service   *honeypot.Service
	validator *validatorFake
	applier   *applierFake
	audit     *auditRecorder
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := honeypot.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor(),
		modules.Descriptor{ID: modules.Tickets, DisplayName: "Tickets"},
		modules.Descriptor{ID: modules.GeneralLogging, DisplayName: "General logging"})
	if err != nil {
		t.Fatal(err)
	}
	validator := &validatorFake{}
	applier := &applierFake{}
	audit := &auditRecorder{}
	service := honeypot.NewService(registry, honeypot.NewStore(db), audit, validator, validator, applier)
	return &fixture{db: db, registry: registry, service: service, validator: validator, applier: applier, audit: audit}
}

func enable(t *testing.T, fixture *fixture, guildID string) honeypot.Actor {
	t.Helper()
	actor := honeypot.Actor{GuildID: guildID, DiscordUserID: "admin", CanManage: true}
	settings := honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "01J500000000000000TEMPLATE"}
	if _, status, err := fixture.service.UpdateSettings(context.Background(), actor, true, settings); err != nil || !status.Enabled {
		t.Fatalf("enable: status=%+v err=%v", status, err)
	}
	return actor
}

func message(id string) honeypot.Message {
	return honeypot.Message{GuildID: "guild-a", ChannelDiscordID: "trap", MessageDiscordID: id, AuthorDiscordUserID: "member", MessageURL: "https://discord.com/channels/guild-a/trap/" + id}
}

func TestNormalPathContractStatisticsAndAudit(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	result, err := fixture.service.HandleMessage(context.Background(), message("message-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseID == "" || fixture.applier.count() != 1 {
		t.Fatalf("result=%+v calls=%d", result, fixture.applier.count())
	}
	request := fixture.applier.requests[0]
	if request.Source != honeypot.SourceHoneypot || request.ActorType != honeypot.ActorTypeSystem || request.ActorDiscordUserID != "" || request.TargetDiscordUserID != "member" {
		t.Fatalf("system attribution contract=%+v", request)
	}
	if request.IdempotencyKey != "honeypot:guild-a:message-1" || request.ContextMessageDiscordID != "message-1" || request.ContextURL == "" {
		t.Fatalf("normal-path context=%+v", request)
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Statistics.Total != 1 || status.Statistics.Created != 1 || status.Statistics.Failed != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	wantActions := map[string]bool{"honeypot.settings.update": false, "honeypot.trigger.detected": false, "honeypot.case.created": false}
	for _, event := range fixture.audit.events {
		if _, ok := wantActions[event.Action]; ok {
			wantActions[event.Action] = true
		}
	}
	for action, found := range wantActions {
		if !found {
			t.Errorf("missing audit %s: %+v", action, fixture.audit.events)
		}
	}
}

func TestTriggerExemptionsAndLoopPrevention(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	cases := []struct {
		name   string
		mutate func(*honeypot.Message)
	}{
		{"quack", func(message *honeypot.Message) { message.IsQuack = true }},
		{"bot", func(message *honeypot.Message) { message.IsBot = true }},
		{"webhook", func(message *honeypot.Message) { message.IsWebhook = true }},
		{"staff", func(message *honeypot.Message) { message.AuthorCanModerate = true }},
	}
	for index, testCase := range cases {
		event := message(fmt.Sprintf("exempt-%d", index))
		testCase.mutate(&event)
		if _, err := fixture.service.HandleMessage(context.Background(), event); !errors.Is(err, honeypot.ErrExempt) {
			t.Errorf("%s error=%v", testCase.name, err)
		}
	}
	offChannel := message("elsewhere")
	offChannel.ChannelDiscordID = "staff-log"
	if _, err := fixture.service.HandleMessage(context.Background(), offChannel); !errors.Is(err, honeypot.ErrNotTrigger) {
		t.Fatalf("off-channel error=%v", err)
	}
	if fixture.applier.count() != 0 {
		t.Fatalf("exemptions applied %d cases", fixture.applier.count())
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Statistics.Exempt != uint64(len(cases)) || status.Statistics.Total != uint64(len(cases)) {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	duplicate := message("exempt-0")
	duplicate.IsQuack = true
	if _, err := fixture.service.HandleMessage(context.Background(), duplicate); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatalf("duplicate exemption error=%v", err)
	}
}

func TestConcurrentReplayCreatesExactlyOneCase(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := fixture.service.HandleMessage(context.Background(), message("same-message"))
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	succeeded := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, honeypot.ErrDuplicate):
		default:
			t.Errorf("unexpected replay error: %v", err)
		}
	}
	if succeeded != 1 || fixture.applier.count() != 1 {
		t.Fatalf("successes=%d apply calls=%d", succeeded, fixture.applier.count())
	}
}

func TestFailureAndDriftDisableSafelyAndRepair(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	fixture.applier.err = errors.New("action path unavailable")
	if _, err := fixture.service.HandleMessage(context.Background(), message("failure")); err == nil {
		t.Fatal("expected case application failure")
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || !status.Enabled || status.Statistics.Failed != 1 {
		t.Fatalf("ordinary failure changed enablement: status=%+v err=%v", status, err)
	}
	fixture.applier.err = nil
	fixture.validator.templateErr = fmt.Errorf("%w: archived template", honeypot.ErrTemplateUnavailable)
	if _, err := fixture.service.HandleMessage(context.Background(), message("drift")); !errors.Is(err, honeypot.ErrTemplateUnavailable) {
		t.Fatalf("template drift error=%v", err)
	}
	_, status, err = fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Enabled || status.DisabledReason == "" || status.Statistics.Failed != 2 {
		t.Fatalf("drift status=%+v err=%v", status, err)
	}
	fixture.validator.templateErr = nil
	if _, status, err = fixture.service.Repair(context.Background(), actor); err != nil || !status.Enabled || status.DisabledReason != "" {
		t.Fatalf("repair status=%+v err=%v", status, err)
	}
	if err := fixture.service.HandleDeletedChannel(context.Background(), "guild-a", "trap"); err != nil {
		t.Fatal(err)
	}
	_, status, _ = fixture.service.Settings(context.Background(), actor)
	if status.Enabled || status.DisabledReason == "" {
		t.Fatalf("deleted-channel status=%+v", status)
	}
}

func TestGuildAndModuleConfigurationIsolation(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	for _, configuration := range []modules.Configuration{
		{GuildID: "guild-a", ModuleID: modules.Tickets, Enabled: true, ConfigJSON: `{}`},
		{GuildID: "guild-a", ModuleID: modules.GeneralLogging, Enabled: true, ConfigJSON: `{}`},
		{GuildID: "guild-b", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: `{"channel_discord_id":"other","template_id":"other-template"}`},
	} {
		if _, err := fixture.registry.SetConfiguration(context.Background(), configuration); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.service.HandleDeletedChannel(context.Background(), "guild-a", "trap"); err != nil {
		t.Fatal(err)
	}
	for _, moduleID := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		configuration, err := fixture.registry.Configuration(context.Background(), "guild-a", moduleID)
		if err != nil || configuration == nil || !configuration.Enabled {
			t.Fatalf("module %s contaminated: %+v err=%v", moduleID, configuration, err)
		}
	}
	other, err := fixture.registry.Configuration(context.Background(), "guild-b", modules.Honeypots)
	if err != nil || other == nil || !other.Enabled {
		t.Fatalf("other guild contaminated: %+v err=%v", other, err)
	}
	var caseTables int64
	if err := fixture.db.Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('cases','case_actions','appeals')").Scan(&caseTables).Error; err != nil || caseTables != 0 {
		t.Fatalf("module migration contaminated core schema: tables=%d err=%v", caseTables, err)
	}
}

func TestImportDryRunIdempotencyAndValidation(t *testing.T) {
	fixture := setup(t)
	actor := honeypot.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	importer := honeypot.NewImporter(fixture.db, fixture.audit, fixture.validator, fixture.validator)
	row := honeypot.LegacySettings{SourceID: "legacy-honeypot", GuildID: "guild-a", Enabled: true, Settings: honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"}}
	if _, err := fixture.registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "guild-a", ModuleID: modules.Honeypots, ConfigJSON: `{"channel_discord_id":"old-trap","template_id":"old-template"}`}); err != nil {
		t.Fatal(err)
	}
	dry, err := importer.Import(context.Background(), actor, []honeypot.LegacySettings{row}, true)
	if err != nil || len(dry) != 1 || !dry[0].WouldCreate {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	var before int64
	fixture.db.Model(&modules.ImportRecord{}).Count(&before)
	if before != 0 {
		t.Fatalf("dry run wrote %d ledger rows", before)
	}
	first, err := importer.Import(context.Background(), actor, []honeypot.LegacySettings{row}, false)
	if err != nil || !first[0].Created {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := importer.Import(context.Background(), actor, []honeypot.LegacySettings{row}, false)
	if err != nil || second[0].Created {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	var count int64
	fixture.db.Model(&modules.ImportRecord{}).Where("module_id = ?", modules.Honeypots).Count(&count)
	if count != 1 {
		t.Fatalf("ledger rows=%d", count)
	}
	configuration, err := fixture.registry.Configuration(context.Background(), "guild-a", modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled || configuration.ID == "" {
		t.Fatalf("upserted configuration=%+v err=%v", configuration, err)
	}
	fixture.validator.channelErr = errors.New("missing permissions")
	row.SourceID = "unsafe"
	if _, err := importer.Import(context.Background(), actor, []honeypot.LegacySettings{row}, true); !errors.Is(err, honeypot.ErrChannelUnavailable) {
		t.Fatalf("unsafe import error=%v", err)
	}
}

func TestRuntimeIntentsQueueAndIndependentShutdown(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	if got := honeypot.RequiredIntents(false); got != (honeypot.IntentRequirements{}) {
		t.Fatalf("disabled intents=%+v", got)
	}
	if got := honeypot.RequiredIntents(true); !got.Guilds || !got.GuildMessages || got.MessageContent {
		t.Fatalf("enabled intents=%+v", got)
	}
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(fixture.service), 128, 4)
	for index := range 100 {
		event := message(fmt.Sprintf("queued-%d", index))
		event.AuthorDiscordUserID = fmt.Sprintf("member-%d", index)
		if err := runtime.Submit(event); err != nil {
			t.Fatal(err)
		}
	}
	runtime.Close()
	runtime.Close()
	if err := runtime.Submit(message("after-close")); err == nil {
		t.Fatal("submit after close succeeded")
	}
	if fixture.applier.count() != 100 {
		t.Fatalf("drain applied %d cases", fixture.applier.count())
	}
}

func TestMigrationDescriptorAndManagerPermissions(t *testing.T) {
	if migration := honeypot.Migration(); migration.Version != 300 || migration.Name != "honeypot_triggers" {
		t.Fatalf("migration=%+v", migration)
	}
	fixture := setup(t)
	if _, _, err := fixture.service.Settings(context.Background(), honeypot.Actor{GuildID: "guild-a"}); !errors.Is(err, honeypot.ErrPermissionDenied) {
		t.Fatalf("read permission error=%v", err)
	}
	if _, _, err := fixture.service.UpdateSettings(context.Background(), honeypot.Actor{GuildID: "guild-a"}, true, honeypot.Settings{}); !errors.Is(err, honeypot.ErrPermissionDenied) {
		t.Fatalf("write permission error=%v", err)
	}
	fixture.validator.channelErr = errors.New("cannot observe channel")
	actor := honeypot.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	_, _, err := fixture.service.UpdateSettings(context.Background(), actor, true, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"})
	if !errors.Is(err, honeypot.ErrChannelUnavailable) {
		t.Fatalf("channel permission error=%v", err)
	}
}

func TestPendingClaimCannotBeCompletedTwice(t *testing.T) {
	fixture := setup(t)
	store := honeypot.NewStore(fixture.db)
	trigger, claimed, err := store.Claim(context.Background(), message("manual"), "template", honeypot.OutcomePending)
	if err != nil || !claimed {
		t.Fatalf("claim=%v err=%v", claimed, err)
	}
	if err := store.Complete(context.Background(), trigger.ID, honeypot.OutcomeCreated, "case", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(context.Background(), trigger.ID, honeypot.OutcomeFailed, "", "late"); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatalf("second completion error=%v", err)
	}
	stats, err := store.Statistics(context.Background(), "guild-a")
	if err != nil || stats.Created != 1 || stats.Failed != 0 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
}

func TestContextCancellationDoesNotInventRetry(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.service.HandleMessage(ctx, message("cancelled"))
	if err == nil {
		t.Fatal("cancelled request unexpectedly succeeded")
	}
	time.Sleep(time.Millisecond)
	if fixture.applier.count() != 0 {
		t.Fatalf("cancelled application calls=%d", fixture.applier.count())
	}
}

// TestMemberBurstCreatesOneIncident covers distinct messages arriving concurrently
// and allows a new incident after the debounce window has elapsed.
func TestMemberBurstCreatesOneIncident(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := fixture.service.HandleMessage(context.Background(), message(fmt.Sprintf("burst-%d", i)))
			if err != nil && !errors.Is(err, honeypot.ErrDuplicate) {
				t.Errorf("burst: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if fixture.applier.count() != 1 {
		t.Fatalf("burst created %d cases", fixture.applier.count())
	}
	if err := fixture.db.Model(&honeypot.Trigger{}).Where("guild_id = ?", "guild-a").Update("created_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.HandleMessage(context.Background(), message("later")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 2 {
		t.Fatal("later incident suppressed")
	}
}

func TestFailedHoneypotIncidentDoesNotSuppressNextMessage(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	fixture.applier.err = errors.New("case unavailable")
	if _, err := fixture.service.HandleMessage(context.Background(), message("failed")); err == nil {
		t.Fatal("expected case failure")
	}
	fixture.applier.err = nil
	if _, err := fixture.service.HandleMessage(context.Background(), message("retry")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 2 {
		t.Fatal("failed incident prevented recovery")
	}
}

// TestIncidentClaimRechecksChangedConfiguration models a gateway job whose
// initial settings read happened before the administrator changed the trap.
func TestIncidentClaimRechecksChangedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		enabled  bool
		settings honeypot.Settings
		want     error
	}{
		{"disabled", false, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"}, honeypot.ErrDisabled},
		{"new template", true, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "replacement"}, honeypot.ErrNotTrigger},
		{"new channel", true, honeypot.Settings{ChannelDiscordID: "replacement", TemplateID: "template"}, honeypot.ErrNotTrigger},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := setup(t)
			enable(t, fixture, "guild-a")
			actor := honeypot.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
			if _, _, err := fixture.service.UpdateSettings(context.Background(), actor, test.enabled, test.settings); err != nil {
				t.Fatal(err)
			}
			event := message("stale-job")
			_, claimed, err := honeypot.NewStore(fixture.db).ClaimIncident(context.Background(), event, "template")
			if claimed || !errors.Is(err, test.want) {
				t.Fatalf("stale claim=%v err=%v", claimed, err)
			}
			var count int64
			if err := fixture.db.Model(&honeypot.Trigger{}).Count(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("stale policy persisted an incident")
			}
		})
	}
}

// TestTemporaryTemplateFailureKeepsHoneypotEnabled prevents a database outage
// from becoming a permanent configuration change that needs administrator repair.
func TestTemporaryTemplateFailureKeepsHoneypotEnabled(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	fixture.validator.templateErr = errors.New("temporary database failure")
	if _, err := fixture.service.HandleMessage(context.Background(), message("outage")); err == nil {
		t.Fatal("lookup failure ignored")
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || !status.Enabled || status.Statistics.Failed != 1 {
		t.Fatalf("temporary failure disabled trap: %+v %v", status, err)
	}
	fixture.validator.templateErr = nil
	if _, err := fixture.service.HandleMessage(context.Background(), message("recovered")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 1 {
		t.Fatal("trap did not recover automatically")
	}
}

// TestObsoleteRoleExemptionsDoNotBypassTrap covers stored pre-rewrite settings;
// old role lists no longer exempt ordinary members from the selected template.
func TestObsoleteRoleExemptionsDoNotBypassTrap(t *testing.T) {
	fixture := setup(t)
	if _, err := fixture.registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: "guild-a", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: `{"channel_discord_id":"trap","template_id":"template","exempt_role_discord_ids":["trusted"]}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.HandleMessage(context.Background(), message("ordinary-member")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 1 {
		t.Fatal("obsolete configuration bypassed moderation")
	}
}

// failingCounter models unavailable Discord presentation after successful moderation.
type failingCounter struct {
	mu    sync.Mutex
	calls int
}

func (c *failingCounter) IncidentCreated(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return errors.New("warning message unavailable")
}

// TestCounterFailureDoesNotRepeatCase refreshes only successful incidents; a
// duplicate message and a failed counter send cannot create another case.
func TestCounterFailureDoesNotRepeatCase(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	counter := &failingCounter{}
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(fixture.service), 8, 1, counter)
	for range 2 {
		if err := runtime.Submit(message("same")); err != nil {
			t.Fatal(err)
		}
	}
	runtime.Close()
	if fixture.applier.count() != 1 || counter.calls != 1 {
		t.Fatalf("case calls=%d counter calls=%d", fixture.applier.count(), counter.calls)
	}
}
