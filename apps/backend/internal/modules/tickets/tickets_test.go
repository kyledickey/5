package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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

func setup(t *testing.T) (*gorm.DB, *tickets.Service, *auditRecorder) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := tickets.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	audit := &auditRecorder{}
	service := tickets.NewService(registry, tickets.NewStore(db), audit)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.QueueChannelDiscordID = "queue"
	if _, err := service.UpdateSettings(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings); err != nil {
		t.Fatal(err)
	}
	return db, service, audit
}

func TestLifecyclePrivacyDuplicateRateAndIsolation(t *testing.T) {
	_, service, audit := setup(t)
	ctx := context.Background()
	member := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	ticket, err := service.Open(ctx, member, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(ctx, member, "thread-2"); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, _, err := service.Detail(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("privacy error=%v", err)
	}
	if _, _, err := service.Detail(ctx, tickets.Actor{GuildID: "guild-b", DiscordUserID: "staff", CanModerate: true}, ticket.ID); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("guild isolation error=%v", err)
	}
	if err := service.Reply(ctx, member, ticket.ID, "private reply"); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(ctx, staff, ticket.ID, "private transcript")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != tickets.StatusResolved {
		t.Fatalf("status=%s", resolved.Status)
	}
	transcript, err := service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("transcript=%+v err=%v", transcript, err)
	}
	if _, err := service.Resolve(ctx, member, ticket.ID, "replacement"); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("closed ticket changed: %v", err)
	}
	transcript, err = service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("closed transcript changed: %+v %v", transcript, err)
	}
	if _, err := service.Open(ctx, member, "premature-thread"); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("opened before transcript delivery and cleanup: %v", err)
	}
	if _, err := tickets.NewDiscordAdapter(service, &discordFake{}).Close(ctx, member, ticket.ID); err != nil {
		t.Fatal(err)
	}
	for index := 2; index <= 3; index++ {
		opened, openErr := service.Open(ctx, member, fmt.Sprintf("thread-%d", index))
		if openErr != nil {
			t.Fatalf("open %d: %v", index, openErr)
		}
		if _, cancelErr := service.Cancel(ctx, member, opened.ID); cancelErr != nil {
			t.Fatalf("cancel %d: %v", index, cancelErr)
		}
	}
	if _, err := service.Open(ctx, member, "thread-4"); err != nil {
		t.Fatalf("unexpected daily limit: %v", err)
	}
	if len(audit.events) < 5 {
		t.Fatalf("audit events=%d", len(audit.events))
	}
}

func TestImportDryRunAndIdempotency(t *testing.T) {
	db, _, audit := setup(t)
	importer := tickets.NewImporter(tickets.NewStore(db), audit)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	row := tickets.LegacyTicket{SourceID: "legacy-1", GuildID: "guild-a", OwnerDiscordUserID: "member", Status: tickets.StatusResolved, CreatedAt: time.Now().Add(-time.Hour)}
	dry, err := importer.Import(context.Background(), actor, []tickets.LegacyTicket{row}, true)
	if err != nil || !dry[0].WouldCreate {
		t.Fatalf("dry=%+v err=%v", dry, err)
	}
	first, err := importer.Import(context.Background(), actor, []tickets.LegacyTicket{row}, false)
	if err != nil || !first[0].Created {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := importer.Import(context.Background(), actor, []tickets.LegacyTicket{row}, false)
	if err != nil || second[0].Created || second[0].TargetID != first[0].TargetID {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

type discordFake struct {
	joined              []string
	frozen              bool
	failPublish         bool
	transcriptPublishes int
	archived            []string
	replies             []string
	permissionCalls     int
	channelCalls        int
	archiveAttempts     int
	failArchive         int
	permissionError     error
}

func (f *discordFake) CreatePrivateTicketChannel(context.Context, string, string, tickets.Settings) (string, error) {
	f.channelCalls++
	return fmt.Sprintf("private-thread-%d", f.channelCalls), nil
}
func (f *discordFake) EnsureTicketPermissions(context.Context, string, string, string) error {
	f.permissionCalls++
	return f.permissionError
}

// TestDiscordOpeningLimitsPrecedeProvisioning covers repeated button clicks
// and failed private ACL setup without creating unbounded orphan channels.
func TestDiscordOpeningLimitsPrecedeProvisioning(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	if _, err := adapter.Open(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := adapter.Open(context.Background(), actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
			t.Fatalf("duplicate opening: %v", err)
		}
	}
	if client.channelCalls != 1 {
		t.Fatalf("duplicate created %d channels", client.channelCalls)
	}
	actor.DiscordUserID = "failed-member"
	client.permissionError = errors.New("private ACL unavailable")
	for range 3 {
		if _, err := adapter.Open(context.Background(), actor); err == nil {
			t.Fatal("expected ACL rejection")
		}
	}
	client.permissionError = nil
	if _, err := adapter.Open(context.Background(), actor); err != nil {
		t.Fatalf("setup repair did not allow retry: %v", err)
	}
	if client.channelCalls != 5 || len(client.archived) != 3 {
		t.Fatalf("unexpected provisional cleanup: %+v", client)
	}
}
func (f *discordFake) SendTicketReply(_ context.Context, _ string, body string) error {
	f.replies = append(f.replies, body)
	return nil
}
func (f *discordFake) CaptureTicketTranscript(context.Context, string) (string, error) {
	if !f.frozen {
		return "", errors.New("thread was not frozen")
	}
	return "captured", nil
}
func (f *discordFake) DeleteProvisionalTicketChannel(_ context.Context, id string) error {
	f.archived = append(f.archived, id)
	return nil
}
func (f *discordFake) DeleteTicketChannel(_ context.Context, id string) error {
	f.archiveAttempts++
	if f.archiveAttempts <= f.failArchive {
		return errors.New("temporary archive failure")
	}
	f.archived = append(f.archived, id)
	return nil
}

func TestDiscordAdapterPrivateFlowAndRepair(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	member := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}
	if _, err := adapter.Close(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("unrelated member closed ticket: %v", err)
	}
	if err := adapter.Reply(context.Background(), staff, ticket.ID, "staff reply"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.RepairPermissions(context.Background(), staff, ticket.ID); err != nil {
		t.Fatal(err)
	}
	client.failArchive = 1
	if _, err := adapter.Close(context.Background(), member, ticket.ID); err == nil {
		t.Fatal("expected first archive failure")
	}
	if _, err := adapter.Close(context.Background(), member, ticket.ID); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	second, err := adapter.Open(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Cancel(context.Background(), member, second.ID); err != nil {
		t.Fatal(err)
	}
	if client.permissionCalls != 3 || len(client.replies) != 1 || len(client.archived) != 2 || client.archiveAttempts != 3 {
		t.Fatalf("client=%+v", client)
	}
	if err := adapter.HandleDeletedChannel(context.Background(), "guild-a", ticket.ID, "private-thread"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.HandleDeletedEntryChannel(context.Background(), "guild-a", "entry"); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background(), staff)
	if err != nil || status.Enabled || status.EntryConfigured {
		t.Fatalf("entry repair status=%+v err=%v", status, err)
	}
}

func TestEnabledTicketsNeedChannelsWithoutCustomStaffRoles(t *testing.T) {
	_, service, _ := setup(t)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.QueueChannelDiscordID = "queue"
	_, err := service.UpdateSettings(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings)
	if err != nil {
		t.Fatalf("tickets required custom staff roles: %v", err)
	}
}

func TestComponentRegistrarAndControls(t *testing.T) {
	registry := interactions.NewComponentRegistry()
	handler := func(ui.Context) ui.HandlerResult { return ui.Immediate(ui.Error("ok")) }
	if err := tickets.RegisterComponents(registry, tickets.ComponentHandlers{Open: handler, Queue: handler, View: handler, Close: handler}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"open", "queue", "view", "close"} {
		customID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: action, Version: "v1", Payload: "ticket-id"})
		if _, ok, err := registry.LookupComponent(customID); err != nil || !ok {
			t.Fatalf("action %s ok=%v err=%v", action, ok, err)
		}
	}
	if len(tickets.EntryComponents()) != 1 || len(tickets.TicketComponents("ticket-id")) != 1 {
		t.Fatal("missing ticket controls")
	}
	if _, ok, err := registry.LookupComponent(ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "reply", Version: "v1", Payload: "ticket-id"})); err != nil || ok {
		t.Fatalf("reply modal control remains: %v %v", ok, err)
	}

}

// TestOwnerCanCloseExistingTicketAfterModuleDisabled keeps closure available
// during setup changes without allowing further tickets to be opened.
func TestOwnerCanCloseExistingTicketAfterModuleDisabled(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	member := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	adapter := tickets.NewDiscordAdapter(service, &discordFake{})
	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, false, tickets.Settings{QueueChannelDiscordID: "queue", TranscriptRetentionDays: 90}); err != nil {
		t.Fatal(err)
	}
	closed, err := adapter.Close(ctx, member, ticket.ID)
	if err != nil || closed.Status != tickets.StatusResolved {
		t.Fatalf("owner close: %+v %v", closed, err)
	}
	transcript, err := service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "captured" {
		t.Fatalf("owner closure lost transcript: %+v %v", transcript, err)
	}
	if _, err := adapter.Open(ctx, member); !errors.Is(err, tickets.ErrDisabled) {
		t.Fatalf("disabled module opened ticket: %v", err)
	}
}

func (f *discordFake) PublishTicketQueue(_ context.Context, ticket *tickets.Ticket, _ tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if transcript != nil {
		f.transcriptPublishes++
		if f.failPublish {
			return nil, errors.New("transcript upload failed")
		}
	}
	return &tickets.QueueReceipt{MessageID: "queue-" + ticket.ID, URL: "https://discord.com/channels/guild/queue/message"}, nil
}

func (f *discordFake) FreezeTicketChannel(context.Context, string) error { f.frozen = true; return nil }

// TestTicketDeletionWaitsForTranscriptPublication exercises a failed upload,
// recovery, and a repeated close without uploading or deleting before its receipt.
func TestTicketDeletionWaitsForTranscriptPublication(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	client.failPublish = true
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("failed transcript upload reported success")
	}
	if client.archiveAttempts != 0 {
		t.Fatal("thread deleted before transcript publication")
	}
	saved, err := service.Transcript(ctx, actor, ticket.ID)
	if err != nil || saved.Content != "captured" {
		t.Fatalf("failed upload lost captured transcript: %+v %v", saved, err)
	}
	client.failPublish = false
	client.failArchive = 1
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected first delete to fail")
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.transcriptPublishes != 2 || client.archiveAttempts != 2 {
		t.Fatalf("retry did not reuse transcript receipt: %+v", client)
	}
	detail, _, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil || detail.TranscriptURL == "" || detail.LogMessageDiscordID == "" {
		t.Fatalf("missing durable transcript receipt: %+v %v", detail, err)
	}
}

// SendTicketWelcome models the thread greeting independently from staff replies.
func (f *discordFake) SendTicketWelcome(context.Context, *tickets.Ticket) error { return nil }

// JoinTicketThread records only membership requests that passed service authorization.
func (f *discordFake) JoinTicketThread(_ context.Context, channelID, userID string) error {
	f.joined = append(f.joined, channelID+":"+userID)
	return nil
}

func TestTicketJoinRequiresCurrentModeratorAndOpenTicket(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "mod"}
	if err := adapter.Join(ctx, actor, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("nonmoderator joined: %v", err)
	}
	actor.CanModerate = true
	if err := adapter.Join(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Join(ctx, actor, ticket.ID); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("closed ticket joined: %v", err)
	}
	if len(client.joined) != 1 {
		t.Fatalf("unexpected invitations: %v", client.joined)
	}
}

// TestEntryPanelReceiptPreservesCurrentSettings prevents a delayed publication
// from overwriting a newer administrator channel selection.
func TestEntryPanelReceiptPreservesCurrentSettings(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	if err := service.RecordEntryPanel(ctx, actor, "entry", "panel"); err != nil {
		t.Fatal(err)
	}
	settings, _, err := service.Settings(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if settings.EntryPanelMessageID != "panel" || settings.QueueChannelDiscordID != "queue" {
		t.Fatalf("receipt changed settings: %+v", settings)
	}
	settings.EntryChannelDiscordID = "new-entry"
	if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntryPanel(ctx, actor, "entry", "late-panel"); err == nil {
		t.Fatal("stale panel receipt accepted")
	}
	settings, _, err = service.Settings(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if settings.EntryChannelDiscordID != "new-entry" || settings.EntryPanelMessageID != "panel" {
		t.Fatalf("stale receipt overwrote settings: %+v", settings)
	}
}

// TestConcurrentCloseReusesCapturedTranscript verifies simultaneous Discord/API
// callers execute one capture/publication pipeline before idempotent cleanup.
func TestConcurrentCloseReusesCapturedTranscript(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 12)
	for range 12 {
		go func() { <-start; _, err := adapter.Close(context.Background(), actor, ticket.ID); results <- err }()
	}
	close(start)
	for range 12 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if client.transcriptPublishes != 1 {
		t.Fatalf("published transcript %d times", client.transcriptPublishes)
	}
}

// TestFailedTicketClosureKeepsMemberReservation prevents a second thread while
// the first still awaits transcript upload or deletion, and fences old retries.
func TestFailedTicketClosureKeepsMemberReservation(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	client.failPublish = true
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected upload failure")
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("opened during failed upload: %v", err)
	}
	client.failPublish = false
	client.failArchive = 1
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected deletion failure")
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("opened before deletion: %v", err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, actor); err != nil {
		t.Fatalf("cleanup did not release member: %v", err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("old close released newer ticket: %v", err)
	}
}
