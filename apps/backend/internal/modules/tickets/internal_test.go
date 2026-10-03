package tickets

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCloseLockCancellationAndIndependentTickets verifies a waiting request can
// expire without blocking unrelated tickets or leaking lock bookkeeping.
func TestCloseLockCancellationAndIndependentTickets(t *testing.T) {
	var locks ticketCloseLocks
	release, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := locks.acquire(ctx, "first"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not cancel: %v", err)
	}
	other, err := locks.acquire(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(locks.entries) != 0 {
		t.Fatal("idle close lock retained")
	}
	again, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	again()
}

// TestOpeningReservationFencesExpiredWorkers proves a lost provisional worker
// cannot create a ticket after another request has reclaimed the member slot.
func TestOpeningReservationFencesExpiredWorkers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	actor := Actor{GuildID: "guild", DiscordUserID: "member"}
	now := time.Now().UTC()
	first, err := s.reserveOpening(context.Background(), actor, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveOpening(context.Background(), actor, now); !errors.Is(err, ErrDuplicateOpen) {
		t.Fatalf("concurrent opening was not denied: %v", err)
	}
	later := now.Add(ticketOpeningTTL + time.Second)
	second, err := s.reserveOpening(context.Background(), actor, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(context.Background(), actor, first, "old-channel", later); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale worker completed: %v", err)
	}
	if err := s.releaseOpening(context.Background(), actor, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(context.Background(), actor, second, "new-channel", later); err != nil {
		t.Fatal(err)
	}
}

// journalGateService isolates admission tests from Discord and settings workflows.
func journalGateService(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "journal.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := NewService(nil, NewStore(db), nil)
	for _, thread := range []string{"one", "two"} {
		ticket, err := s.store.create(context.Background(), "guild", thread, thread, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		s.rememberJournalThread(ticket)
	}
	return s
}

// TestJournalAdmissionPrecedesGate verifies queued text is available to a close
// snapshot and an unrelated thread does not wait for that thread's processing.
func TestJournalAdmissionPrecedesGate(t *testing.T) {
	s := journalGateService(t)
	release, err := s.journal.locks.acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.RecordMessage(context.Background(), "guild", "one", TranscriptMessage{MessageID: "1", AuthorID: "member", Body: "queued original"})
	}()
	deadline := time.Now().Add(time.Second)
	for {
		s.journal.mu.Lock()
		n := len(s.journal.pending)
		s.journal.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			release()
			t.Fatal("write did not register before waiting")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.RecordMessage(context.Background(), "guild", "two", TranscriptMessage{MessageID: "2", AuthorID: "member", Body: "independent"}); err != nil {
		release()
		t.Fatal(err)
	}
	var ticket ticketRecord
	if err := s.store.db.Where("thread_discord_channel_id = ?", "one").First(&ticket).Error; err != nil {
		release()
		t.Fatal(err)
	}
	// A close owns this gate and can flush the admitted waiter before it runs.
	rows, err := s.flushJournal(context.Background(), &Ticket{ID: ticket.ID, GuildID: "guild", ThreadDiscordChannelID: "one"})
	release()
	if err != nil || len(rows) != 1 || rows[0].Body != "queued original" {
		t.Fatal(rows, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.journal.threads) != 0 || len(s.journal.locks.entries) != 0 {
		t.Fatal("idle processing gates leaked")
	}
}

// TestJournalIgnoresUnknownTrafficDuringOutage proves unrelated message content
// cannot occupy retry memory even when every SQL operation would fail.
func TestJournalIgnoresUnknownTrafficDuringOutage(t *testing.T) {
	s := journalGateService(t)
	sqlDB, _ := s.store.db.DB()
	_ = sqlDB.Close()
	for i := 0; i < 1100; i++ {
		if err := s.RecordMessage(context.Background(), "guild", "unrelated", TranscriptMessage{MessageID: "message", AuthorID: "member", Body: "private unrelated text"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.journal.pending) != 0 || len(s.journal.blocked) != 0 || s.journal.allBlocked {
		t.Fatal("unrelated traffic entered ticket retry state")
	}
}
