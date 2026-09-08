package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// journalSetup closes the fixture pool so repeated race runs cannot reuse rows.
func journalSetup(t *testing.T) (*gorm.DB, *tickets.Service, *auditRecorder) {
	db, service, audit := setup(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db, service, audit
}

// journalDiscordFake exposes structured surviving history while retaining real
// adapter lifecycle assertions and counting queue publication/deletion attempts.
type journalDiscordFake struct {
	*discordFake
	messages []tickets.TranscriptMessage
}

// CaptureTicketMessages simulates close-time Discord history after deletions.
func (f *journalDiscordFake) CaptureTicketMessages(context.Context, string) ([]tickets.TranscriptMessage, error) {
	if !f.frozen {
		return nil, errors.New("capture preceded thread freeze")
	}
	return f.messages, nil
}

// TestJournalPreservesDeletedOriginalTextAcrossRestart covers create/delete/close
// parity, duplicate gateway creates, edited survivors, order and thread isolation.
func TestJournalPreservesDeletedOriginalTextAcrossRestart(t *testing.T) {
	db, service, _ := journalSetup(t)
	ctx := context.Background()
	client := &journalDiscordFake{discordFake: &discordFake{}}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	first := tickets.TranscriptMessage{MessageID: "100", AuthorID: "member", AuthorName: "Member", Body: "original deleted text", SentAt: time.Now().UTC()}
	second := first
	second.MessageID = "101"
	second.Body = "original surviving text"
	for _, message := range []tickets.TranscriptMessage{first, second, first} {
		if err := service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, message); err != nil {
			t.Fatal(err)
		}
	}
	changed := second
	changed.Body = "edited text"
	if err := service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, changed); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMessage(ctx, "other-guild", ticket.ThreadDiscordChannelID, first); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMessage(ctx, actor.GuildID, "unrelated-channel", first); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&tickets.MessageJournal{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatal("journal identity isolation", count, err)
	}
	// Rebuild module objects around persisted data; deleted message is absent
	// from final Discord history while the surviving message has since changed.
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	restarted := tickets.NewService(registry, tickets.NewStore(db), nil)
	client.messages = []tickets.TranscriptMessage{changed, changed}
	if _, err := tickets.NewDiscordAdapter(restarted, client).Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	transcript, err := restarted.Transcript(ctx, actor, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(transcript.Content, first.Body) != 1 || strings.Count(transcript.Content, second.Body) != 1 || strings.Contains(transcript.Content, "edited text") || strings.Index(transcript.Content, first.Body) > strings.Index(transcript.Content, second.Body) {
		t.Fatal("original transcript changed", transcript.Content)
	}
	if client.archiveAttempts != 1 || client.transcriptPublishes != 1 {
		t.Fatal("closure did not complete")
	}
}

// TestJournalFailureBlocksDeletionAndRetryFlushesOriginal verifies failed SQL
// ingestion cannot be silently bypassed by a later close-time history fetch.
func TestJournalFailureBlocksDeletionAndRetryFlushesOriginal(t *testing.T) {
	db, service, _ := journalSetup(t)
	client := &journalDiscordFake{discordFake: &discordFake{}}
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	db.Config.Logger = logger.Default.LogMode(logger.Silent)
	fail := true
	if err := db.Callback().Create().Before("gorm:create").Register("fail_journal", func(tx *gorm.DB) {
		if fail && tx.Statement.Table == "ticket_message_journal" {
			tx.AddError(errors.New("journal storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	original := tickets.TranscriptMessage{MessageID: "deleted", AuthorID: "member", Body: "buffered original", SentAt: time.Now().UTC()}
	if err := service.RecordMessage(context.Background(), actor.GuildID, ticket.ThreadDiscordChannelID, original); err == nil {
		t.Fatal("write failure hidden")
	}
	if _, err := adapter.Close(context.Background(), actor, ticket.ID); err == nil || client.archiveAttempts != 0 || client.transcriptPublishes != 0 {
		t.Fatal("close discarded failed ingestion", err)
	}
	fail = false
	if _, err := adapter.Close(context.Background(), actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	transcript, err := service.Transcript(context.Background(), actor, ticket.ID)
	if err != nil || !strings.Contains(transcript.Content, original.Body) {
		t.Fatal("retry lost original", transcript, err)
	}
}

// TestJournalOverflowCannotSilentlyClose makes bounded buffer exhaustion explicit
// instead of producing a deceptively complete transcript and deleting the thread.
func TestJournalOverflowCannotSilentlyClose(t *testing.T) {
	db, service, _ := journalSetup(t)
	client := &journalDiscordFake{discordFake: &discordFake{}}
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	db.Config.Logger = logger.Default.LogMode(logger.Silent)
	if err := db.Callback().Create().Before("gorm:create").Register("full_journal", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_message_journal" {
			tx.AddError(errors.New("unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 1001 {
		err = service.RecordMessage(context.Background(), actor.GuildID, ticket.ThreadDiscordChannelID, tickets.TranscriptMessage{MessageID: fmt.Sprint(i), AuthorID: "member", Body: "original", SentAt: time.Now()})
	}
	if !errors.Is(err, tickets.ErrJournalIncomplete) {
		t.Fatal("overflow hidden", err)
	}
	if err := db.Callback().Create().Remove("full_journal"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Close(context.Background(), actor, ticket.ID); !errors.Is(err, tickets.ErrJournalIncomplete) || client.archiveAttempts != 0 {
		t.Fatal("overflow allowed deletion", err)
	}
}

// TestJournalCloseWaitsForReceivedWrite exercises the shared ingestion/merge gate
// with a write already in flight when another goroutine requests closure.
func TestJournalCloseWaitsForReceivedWrite(t *testing.T) {
	db, service, _ := journalSetup(t)
	client := &journalDiscordFake{discordFake: &discordFake{}}
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	if err := db.Callback().Create().Before("gorm:create").Register("blocked_journal", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_message_journal" {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		written <- service.RecordMessage(context.Background(), actor.GuildID, ticket.ThreadDiscordChannelID, tickets.TranscriptMessage{MessageID: "received", AuthorID: "member", Body: "received before close", SentAt: time.Now().UTC()})
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { _, err := adapter.Close(context.Background(), actor, ticket.ID); closed <- err }()
	select {
	case err := <-closed:
		t.Fatalf("close passed in-flight write: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	transcript, err := service.Transcript(context.Background(), actor, ticket.ID)
	if err != nil || !strings.Contains(transcript.Content, "received before close") {
		t.Fatal("write/close race lost original", transcript, err)
	}
}

// TestJournalSharesTranscriptRetention ensures original text does not outlive the
// existing closed-ticket transcript retention policy.
func TestJournalSharesTranscriptRetention(t *testing.T) {
	db, service, _ := journalSetup(t)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := service.Open(context.Background(), actor, "thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMessage(context.Background(), actor.GuildID, "thread", tickets.TranscriptMessage{MessageID: "original", AuthorID: "member", Body: "text", SentAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveNativeTranscript(context.Background(), actor, ticket.ID, nil); err != nil {
		t.Fatal(err)
	}
	var journal tickets.MessageJournal
	if err := db.First(&journal).Error; err != nil || journal.ExpiresAt == nil {
		t.Fatal("journal has no retention deadline", err)
	}
	if err := db.Model(&journal).Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.PurgeExpiredTranscripts(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&tickets.MessageJournal{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("expired journal retained", count, err)
	}
}
