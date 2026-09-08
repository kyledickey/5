package generallogging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	logmodule "github.com/quackdiscord/bot/internal/modules/generallogging"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type auditRecorder struct{ events []modules.AuditEvent }

func (a *auditRecorder) RecordModuleAudit(_ context.Context, event modules.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

type deliveryFake struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	payloads  []string
	channels  []string
}

func (f *deliveryFake) ValidateStaffOnlyChannel(_ context.Context, _ string, channelID string) error {
	if strings.HasPrefix(channelID, "public") {
		return errors.New("destination is not staff-only")
	}
	return nil
}
func (f *deliveryFake) SendStaffLog(_ context.Context, _, channelID, payload string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failUntil {
		return errors.New("temporary Discord failure")
	}
	f.payloads = append(f.payloads, payload)
	f.channels = append(f.channels, channelID)
	return nil
}

func setup(t *testing.T) (*gorm.DB, *logmodule.Service, *deliveryFake, *auditRecorder) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), logmodule.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	client := &deliveryFake{}
	audit := &auditRecorder{}
	service := logmodule.NewService(registry, audit, client, logmodule.NewMessageCache(2))
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{logmodule.MessageDelete: "staff-log", logmodule.MessageBulkDelete: "staff-log", logmodule.MemberJoin: "staff-log"}
	settings.IncludeMessageContent = true
	settings.IncludeAttachmentMetadata = true
	settings.CacheEntriesPerGuild = 2
	if _, err := service.UpdateSettings(context.Background(), logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings); err != nil {
		t.Fatal(err)
	}
	return db, service, client, audit
}

func TestPrivacyRedactionRetryAndAuditIsolation(t *testing.T) {
	_, service, client, audit := setup(t)
	client.failUntil = 2
	if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", ChannelDiscordID: "source", MessageDiscordID: "message", Content: "token=supersecretvalue", Attachments: []logmodule.AttachmentMetadata{{Filename: "proof.png", Size: 5}}}); err != nil {
		t.Fatal(err)
	}
	err := service.Handle(context.Background(), logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageDelete, MessageDiscordID: "message", Metadata: map[string]string{"webhook": "https://discord.com/api/webhooks/123/secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.attempts != 3 {
		t.Fatalf("attempts=%d", client.attempts)
	}
	payload := client.payloads[0]
	if strings.Contains(payload, "supersecretvalue") || strings.Contains(payload, "/123/secret") || !strings.Contains(payload, "REDACTED") {
		t.Fatalf("unredacted payload: %s", payload)
	}
	status := service.Status("guild-a")
	if status.Delivered != 1 || status.Failed != 0 {
		t.Fatalf("status=%+v", status)
	}
	for _, event := range audit.events {
		if strings.Contains(event.Action, "delivery") || event.ResourceType == "audit_log" {
			t.Fatalf("general events contaminated audit: %+v", event)
		}
	}
}

func TestFailedDeleteRetainsCachedContextForGatewayReplay(t *testing.T) {
	_, service, client, _ := setup(t)
	client.failUntil = 10
	if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "replay", Content: "retained context"}); err != nil {
		t.Fatal(err)
	}
	event := logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageDelete, MessageDiscordID: "replay"}
	if err := service.Handle(context.Background(), event); err == nil {
		t.Fatal("expected bounded failure")
	}
	client.failUntil = client.attempts
	if err := service.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.payloads[len(client.payloads)-1], "retained context") {
		t.Fatalf("replay lost cache: %s", client.payloads[len(client.payloads)-1])
	}
}

func TestBulkDeleteUsesAndThenEvictsCachedContext(t *testing.T) {
	_, service, client, _ := setup(t)
	for _, id := range []string{"one", "two"} {
		if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: id, Content: "body-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.HandleBulkDelete(context.Background(), "guild-a", "source", []string{"one", "two", "missing"}); err != nil {
		t.Fatal(err)
	}
	payload := client.payloads[len(client.payloads)-1]
	if !strings.Contains(payload, "body-one") || !strings.Contains(payload, "body-two") {
		t.Fatalf("bulk payload=%s", payload)
	}
	if err := service.HandleBulkDelete(context.Background(), "guild-a", "source", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	payload = client.payloads[len(client.payloads)-1]
	if strings.Contains(payload, "body-one") {
		t.Fatalf("bulk cache not evicted: %s", payload)
	}
}

func TestCacheMessageLoadsPersistedLimit(t *testing.T) {
	_, service, _, _ := setup(t)
	for i := 0; i < 3; i++ {
		if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if status := service.Status("guild-a"); status.CachedMessages != 2 {
		t.Fatalf("cached=%d", status.CachedMessages)
	}
}

func TestRepairAndGuildModuleIsolation(t *testing.T) {
	_, service, _, _ := setup(t)
	actor := logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings, enabled, err := service.RepairDeletedChannel(context.Background(), actor, "staff-log")
	if err != nil {
		t.Fatal(err)
	}
	if enabled || len(settings.Channels) != 0 {
		t.Fatalf("repair enabled=%v settings=%+v", enabled, settings)
	}
	if err := service.Handle(context.Background(), logmodule.Event{GuildID: "guild-b", Type: logmodule.MemberJoin}); !errors.Is(err, logmodule.ErrDisabled) {
		t.Fatalf("guild leak error=%v", err)
	}
}

func TestCacheConcurrentBoundedAndGuildScoped(t *testing.T) {
	cache := logmodule.NewMessageCache(10)
	cache.SetGuildLimit("a", 5)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Put(logmodule.CachedMessage{GuildID: "a", MessageDiscordID: fmt.Sprint(i), Content: "a"})
			cache.Put(logmodule.CachedMessage{GuildID: "b", MessageDiscordID: fmt.Sprint(i), Content: "b"})
			cache.Get("a", fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	if cache.Len("a") != 5 || cache.Len("b") != 10 {
		t.Fatalf("lengths a=%d b=%d", cache.Len("a"), cache.Len("b"))
	}
}

func TestDeliveryQueueConcurrentBoundedLifecycle(t *testing.T) {
	_, service, _, _ := setup(t)
	queue := logmodule.NewDeliveryQueue(context.Background(), service, 8, 3)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := queue.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberJoin, ActorDiscordUserID: fmt.Sprint(i)})
			if err != nil && !errors.Is(err, logmodule.ErrQueueFull) {
				t.Errorf("submit: %v", err)
			}
		}(i)
	}
	wg.Wait()
	queue.Close()
	if err := queue.Submit(logmodule.Event{}); err == nil {
		t.Fatal("submit after close succeeded")
	}
}

func TestSettingsImportDryRunAndIdempotency(t *testing.T) {
	db, service, _, audit := setup(t)
	importer := logmodule.NewImporter(db, audit)
	actor := logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{logmodule.MemberJoin: "staff"}
	settings.CacheEntriesPerGuild = 1
	row := logmodule.LegacySettings{SourceID: "legacy-settings", GuildID: "guild-a", Enabled: true, Settings: settings}
	dry, err := importer.Import(context.Background(), actor, []logmodule.LegacySettings{row}, true)
	if err != nil || !dry[0].WouldCreate {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	first, err := importer.Import(context.Background(), actor, []logmodule.LegacySettings{row}, false)
	if err != nil || !first[0].Created {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := importer.Import(context.Background(), actor, []logmodule.LegacySettings{row}, false)
	if err != nil || second[0].Created {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	for _, id := range []string{"after-import-1", "after-import-2"} {
		if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: id}); err != nil {
			t.Fatal(err)
		}
	}
	if status := service.Status("guild-a"); status.CachedMessages != 1 {
		t.Fatalf("imported cache limit not applied: %+v", status)
	}
}

// TestQueuedEditsKeepTheirOriginalSnapshots covers two edits before worker
// delivery, including a legitimately empty original message and unchanged updates.
func TestQueuedEditsKeepTheirOriginalSnapshots(t *testing.T) {
	_, service, client, _ := setup(t)
	ctx := context.Background()
	actor := logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings, _, _, err := service.Settings(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	settings.Channels[logmodule.MessageEdit] = "staff-log"
	if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
		t.Fatal(err)
	}
	current := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message", AuthorDiscordUserID: "author", Content: ""}
	if err := service.CacheMessage(ctx, current); err != nil {
		t.Fatal(err)
	}
	current.Content = "first"
	first, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || first == nil {
		t.Fatalf("first edit: %v", err)
	}
	current.Content = "second"
	second, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || second == nil {
		t.Fatalf("second edit: %v", err)
	}
	unchanged, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || unchanged != nil {
		t.Fatalf("unchanged update generated log: %+v %v", unchanged, err)
	}
	if err := service.Handle(ctx, *first); err != nil {
		t.Fatal(err)
	}
	if err := service.Handle(ctx, *second); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.payloads[0], `"before":""`) || !strings.Contains(client.payloads[0], `"after":"first"`) || !strings.Contains(client.payloads[1], `"before":"first"`) || !strings.Contains(client.payloads[1], `"after":"second"`) {
		t.Fatalf("queued edits read newer cache state: %v", client.payloads)
	}
}

func TestAttachmentOnlyEditsKeepPreviousFiles(t *testing.T) {
	_, service, _, _ := setup(t)
	ctx := context.Background()
	current := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "files", Content: "same", Attachments: []logmodule.AttachmentMetadata{{DiscordID: "old", Filename: "proof.png"}}}
	if err := service.CacheMessage(ctx, current); err != nil {
		t.Fatal(err)
	}
	current.Attachments = []logmodule.AttachmentMetadata{{DiscordID: "replacement", Filename: "proof.png"}}
	replaced, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || replaced == nil || replaced.BeforeAttachments[0].DiscordID != "old" {
		t.Fatalf("same-name replacement missed: %+v %v", replaced, err)
	}
	current.Attachments = nil
	removed, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || removed == nil || len(removed.BeforeAttachments) != 1 || len(removed.Attachments) != 0 {
		t.Fatalf("file removal missed: %+v %v", removed, err)
	}
}

// TestDeliveryQueueSkipsUnconfiguredEventsButReportsFailures distinguishes
// ordinary disabled/unrouted gateway traffic from a real delivery failure.
func TestDeliveryQueueSkipsUnconfiguredEventsButReportsFailures(t *testing.T) {
	for _, scenario := range []struct {
		name, guild string
		event       logmodule.EventType
		fail        bool
	}{
		{"disabled", "guild-b", logmodule.MemberJoin, false},
		{"unrouted", "guild-a", logmodule.GuildChange, false},
		{"delivery failure", "guild-a", logmodule.MemberJoin, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, service, client, audit := setup(t)
			if scenario.fail {
				client.failUntil = 100
			}
			auditCount := len(audit.events)
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			defer slog.SetDefault(previous)
			queue := logmodule.NewDeliveryQueue(context.Background(), service, 1, 1)
			if err := queue.Submit(logmodule.Event{GuildID: scenario.guild, Type: scenario.event}); err != nil {
				t.Fatal(err)
			}
			queue.Close()
			reported := strings.Contains(output.String(), "General logging delivery failed")
			if reported != scenario.fail {
				t.Fatalf("wrong failure classification: %s", output.String())
			}
			if !scenario.fail && client.attempts != 0 {
				t.Fatal("unconfigured event attempted delivery")
			}
			if len(audit.events) != auditCount {
				t.Fatal("delivery bookkeeping polluted moderation audit")
			}
			if scenario.fail && service.Status(scenario.guild).Failed != 1 {
				t.Fatal("real failure disappeared from module status")
			}
		})
	}
}

// TestNativeSetupPersistsOneChannelAndDeliversMessageDetails exercises the same
// settings operation as /setup logging through persistence and event formatting.
// Delivery bookkeeping must remain absent from the administration audit stream.
func TestNativeSetupPersistsOneChannelAndDeliversMessageDetails(t *testing.T) {
	_, service, client, audit := setup(t)
	ctx := context.Background()
	actor := logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings, _, _, err := service.Settings(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(ctx, actor, true, settings.RouteAllTo("new-staff-log")); err != nil {
		t.Fatal(err)
	}
	saved, enabled, _, err := service.Settings(ctx, actor)
	if err != nil || !enabled || len(saved.Channels) != 9 {
		t.Fatalf("setup not persisted: %+v, %v", saved, err)
	}
	auditCount := len(audit.events)
	for _, kind := range []logmodule.EventType{logmodule.MessageDelete, logmodule.MessageBulkDelete} {
		cached := logmodule.CachedMessage{GuildID: actor.GuildID, ChannelDiscordID: "source", MessageDiscordID: "cached", Content: "original content", Attachments: []logmodule.AttachmentMetadata{{Filename: "proof.png", ContentType: "image/png", Size: 42}}, EmbedTypes: []string{"image"}}
		if err := service.CacheMessage(ctx, cached); err != nil {
			t.Fatal(err)
		}
		if kind == logmodule.MessageBulkDelete {
			err = service.HandleBulkDelete(ctx, actor.GuildID, "source", []string{"cached", "uncached"})
		} else {
			err = service.Handle(ctx, logmodule.Event{GuildID: actor.GuildID, Type: kind, MessageDiscordID: "cached"})
		}
		if err != nil {
			t.Fatal(err)
		}
		var payload struct {
			Before      string                         `json:"before"`
			Attachments []logmodule.AttachmentMetadata `json:"attachments"`
			EmbedTypes  []string                       `json:"embed_types"`
		}
		if err := json.Unmarshal([]byte(client.payloads[len(client.payloads)-1]), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Before != cached.Content || len(payload.Attachments) != 1 || payload.Attachments[0].Filename != "proof.png" || len(payload.EmbedTypes) != 1 || payload.EmbedTypes[0] != "image" {
			t.Fatalf("%s lost configured message detail: %+v", kind, payload)
		}
	}
	for _, channel := range client.channels {
		if channel != "new-staff-log" {
			t.Fatalf("old destination used: %s", channel)
		}
	}
	if len(audit.events) != auditCount {
		t.Fatal("message delivery added audit bookkeeping")
	}
}
