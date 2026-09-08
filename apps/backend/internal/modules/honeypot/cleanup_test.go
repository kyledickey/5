package honeypot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// cleanupApplier models Discord deletion independently of normal case execution.
type cleanupApplier struct {
	*applierFake
	mu       sync.Mutex
	attempts map[string]int
	fail     bool
	deleted  chan string
	entered  chan struct{}
	release  chan struct{}
}

// ApplyHoneypotCase optionally pauses evidence/case creation to expose burst races.
func (a *cleanupApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	if a.entered != nil {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return honeypot.ApplyResult{}, ctx.Err()
		}
	}
	return a.applierFake.ApplyHoneypotCase(ctx, request)
}

// DeleteHoneypotMessage records retry attempts without creating another case.
func (a *cleanupApplier) DeleteHoneypotMessage(_ context.Context, channelID, messageID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.attempts[messageID]++
	if a.fail {
		return errors.New("temporary Discord delete failure")
	}
	if a.deleted != nil {
		a.deleted <- messageID
	}
	return nil
}

// cleanupFixture attaches a deletion-capable case boundary to the standard store.
func cleanupFixture(t *testing.T) (*fixture, *cleanupApplier) {
	t.Helper()
	f := setup(t)
	// Repeated runs use the same test name in SQLite's shared-memory DSN. Close
	// the last connection after worker defers run so each repetition starts empty.
	sqlDB, err := f.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := &cleanupApplier{applierFake: f.applier, attempts: map[string]int{}}
	f.service = honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	enable(t, f, "guild-a")
	return f, a
}

// TestBurstCleanupRetainsOneCaseAndEveryMessage covers both concurrent burst
// claims and old gateway replays after the moderation debounce has elapsed.
func TestBurstCleanupRetainsOneCaseAndEveryMessage(t *testing.T) {
	f, a := cleanupFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.service.HandleMessage(ctx, message(fmt.Sprintf("burst-%d", i)))
			if err != nil && !errors.Is(err, honeypot.ErrDuplicate) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := f.service.ProcessCleanups(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if a.count() != 1 || len(a.attempts) != 20 {
		t.Fatalf("cases=%d deleted=%d", a.count(), len(a.attempts))
	}
	if err := f.db.Model(&honeypot.Trigger{}).Where("guild_id = ?", "guild-a").Update("created_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if _, err := f.service.HandleMessage(ctx, message(fmt.Sprintf("burst-%d", i))); !errors.Is(err, honeypot.ErrDuplicate) {
			t.Fatalf("replay: %v", err)
		}
	}
	if a.count() != 1 {
		t.Fatal("cleanup replay repeated punishment")
	}
	if err := f.service.ProcessCleanups(ctx, 25); err != nil {
		t.Fatal(err)
	}
	for id, count := range a.attempts {
		if count != 1 {
			t.Fatalf("completed message %s deleted %d times", id, count)
		}
	}
}

// TestCleanupWaitsForEvidenceAndSavedIncident retains all burst messages while
// the first case is pending, and retains sources when case creation fails.
func TestCleanupWaitsForEvidenceAndSavedIncident(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			f, a := cleanupFixture(t)
			a.entered = make(chan struct{})
			a.release = make(chan struct{})
			if failed {
				a.applierFake.err = errors.New("case failed")
			}
			done := make(chan error, 1)
			go func() { _, err := f.service.HandleMessage(context.Background(), message("first")); done <- err }()
			<-a.entered
			if _, err := f.service.HandleMessage(context.Background(), message("second")); !errors.Is(err, honeypot.ErrDuplicate) {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			if len(a.attempts) != 0 {
				t.Fatal("deleted before evidence and case persistence")
			}
			close(a.release)
			if err := <-done; (err != nil) != failed {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			if !failed && len(a.attempts) != 2 || failed && len(a.attempts) != 0 {
				t.Fatal("incorrect cleanup after case outcome", a.attempts)
			}
		})
	}
}

// TestCleanupRecoversAfterFailureAndRestart verifies durable retries and startup
// polling without a new gateway event or another moderation application.
func TestCleanupRecoversAfterFailureAndRestart(t *testing.T) {
	f, a := cleanupFixture(t)
	if _, err := f.service.HandleMessage(context.Background(), message("retry")); err != nil {
		t.Fatal(err)
	}
	a.fail = true
	if err := f.service.ProcessCleanups(context.Background(), 1); err == nil {
		t.Fatal("delete failure hidden")
	}
	var record honeypot.MessageCleanup
	if err := f.db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.CompletedAt != nil || record.AttemptCount != 1 {
		t.Fatal("retry receipt lost", record)
	}
	// Expire the retry/lease without sleeping; a fresh runtime uses the same DB.
	if err := f.db.Model(&record).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	a.fail = false
	a.deleted = make(chan string, 2)
	restarted := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(restarted), 4, 1)
	defer runtime.Close()
	select {
	case <-a.deleted:
	case <-time.After(3 * time.Second):
		t.Fatal("restart did not recover cleanup")
	}
	// Wait for the completion receipt, not merely the transport callback: Close
	// deliberately cancels in-flight cleanup, including its final database write.
	deadline := time.Now().Add(time.Second)
	for {
		if err := f.db.First(&record).Error; err != nil {
			t.Fatal(err)
		}
		if record.CompletedAt != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup completion was not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	runtime.Close()
	if record.CompletedAt == nil || record.AttemptCount != 2 || a.count() != 1 {
		t.Fatal("restart repeated/lost work", record, a.count())
	}
}

// TestCleanupNeverSchedulesExemptMessages protects moderators, bots and webhooks
// even when an ordinary member's incident is simultaneously eligible for cleanup.
func TestCleanupNeverSchedulesExemptMessages(t *testing.T) {
	f, a := cleanupFixture(t)
	for i, exempt := range []func(*honeypot.Message){func(m *honeypot.Message) { m.AuthorCanModerate = true }, func(m *honeypot.Message) { m.IsBot = true }, func(m *honeypot.Message) { m.IsWebhook = true }, func(m *honeypot.Message) { m.IsQuack = true }} {
		event := message(fmt.Sprintf("exempt-%d", i))
		exempt(&event)
		if _, err := f.service.HandleMessage(context.Background(), event); !errors.Is(err, honeypot.ErrExempt) {
			t.Fatal(err)
		}
	}
	if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&honeypot.MessageCleanup{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || len(a.attempts) != 0 || a.count() != 0 {
		t.Fatal("exempt message scheduled", count, a.attempts)
	}
}

// blockedCleanupApplier waits for cancellation to model an unresponsive Discord
// deletion independently of the still-live primary moderation context.
type blockedCleanupApplier struct {
	*cleanupApplier
	started chan struct{}
	once    sync.Once
}

// DeleteHoneypotMessage exposes an in-flight cleanup attempt until shutdown.
func (a *blockedCleanupApplier) DeleteHoneypotMessage(ctx context.Context, _, _ string) error {
	a.once.Do(func() { close(a.started) })
	<-ctx.Done()
	return ctx.Err()
}

// TestCleanupCloseCancelsBlockedDelete ensures cleanup cannot delay shutdown by
// a batch of Discord timeouts, while the interrupted receipt stays recoverable.
func TestCleanupCloseCancelsBlockedDelete(t *testing.T) {
	f, a := cleanupFixture(t)
	blocked := &blockedCleanupApplier{cleanupApplier: a, started: make(chan struct{})}
	service := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, blocked)
	if _, err := service.HandleMessage(context.Background(), message("blocked")); err != nil {
		t.Fatal(err)
	}
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(service), 4, 1)
	defer runtime.Close()
	select {
	case <-blocked.started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	// Accepted primary work must still drain even though deletion is canceled.
	next := message("primary-drain")
	next.AuthorDiscordUserID = "another-member"
	if err := runtime.Submit(next); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { runtime.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close waited for Discord timeout")
	}
	var receipt honeypot.MessageCleanup
	if err := f.db.Where("message_discord_id = ?", "blocked").First(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	if receipt.CompletedAt != nil || receipt.AttemptCount != 1 {
		t.Fatal("interrupted delete lost recovery receipt", receipt)
	}
	if a.count() != 2 {
		t.Fatal("cleanup cancellation aborted primary drain", a.count())
	}
}
