package honeypot_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// recoveryApplier models the core's immutable idempotency lookup independently
// of fresh live preparation and the normal case application transaction.
type recoveryApplier struct {
	*cleanupApplier
	recoveryMu sync.Mutex
	saved      map[string]string
	prepareErr error
	lookupErr  error
	prepares   int
}

// FindHoneypotCase reads a committed case without another application attempt.
func (a *recoveryApplier) FindHoneypotCase(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	return honeypot.ApplyResult{CaseID: a.saved[request.IdempotencyKey]}, a.lookupErr
}

// PrepareHoneypotRecovery models live permissions and restores canonical context.
func (a *recoveryApplier) PrepareHoneypotRecovery(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyRequest, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	a.prepares++
	request.ContextURL = "https://discord.com/channels/guild/trap/" + request.ContextMessageDiscordID
	return request, a.prepareErr
}

// ApplyHoneypotCase mirrors the core's atomic idempotent case insertion.
func (a *recoveryApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	if id := a.saved[request.IdempotencyKey]; id != "" {
		return honeypot.ApplyResult{CaseID: id}, nil
	}
	result, err := a.cleanupApplier.ApplyHoneypotCase(ctx, request)
	if err == nil {
		a.saved[request.IdempotencyKey] = result.CaseID
	}
	return result, err
}

// recoveryFixture creates the real module store with an idempotent core boundary.
func recoveryFixture(t *testing.T) (*fixture, *recoveryApplier) {
	t.Helper()
	f, cleanup := cleanupFixture(t)
	a := &recoveryApplier{cleanupApplier: cleanup, saved: map[string]string{}}
	f.service = honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	return f, a
}

// interruptedIncident models a process stopping immediately after durable claim.
func interruptedIncident(t *testing.T, f *fixture) (*honeypot.Trigger, honeypot.ApplyRequest) {
	t.Helper()
	settings, _, err := f.service.Settings(context.Background(), honeypot.Actor{GuildID: "guild-a", CanManage: true})
	if err != nil {
		t.Fatal(err)
	}
	event := message("interrupted")
	trigger, created, err := honeypot.NewStore(f.db).ClaimIncident(context.Background(), event, settings.TemplateID)
	if err != nil || !created {
		t.Fatal("claim", created, err)
	}
	request := honeypot.ApplyRequest{GuildID: trigger.GuildID, TemplateID: trigger.TemplateID, TargetDiscordUserID: trigger.TargetDiscordUserID, ContextChannelDiscordID: trigger.ChannelDiscordID, ContextMessageDiscordID: trigger.MessageDiscordID, ContextURL: event.MessageURL, IdempotencyKey: "honeypot:" + trigger.GuildID + ":" + trigger.MessageDiscordID, Source: honeypot.SourceHoneypot, ActorType: honeypot.ActorTypeSystem}
	return trigger, request
}

// expireIncident advances a persisted lease without making timing tests sleep.
func expireIncident(t *testing.T, f *fixture, trigger *honeypot.Trigger) {
	t.Helper()
	if err := f.db.Model(&honeypot.Trigger{}).Where("id = ?", trigger.ID).Update("updated_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
}

// TestIncidentRecoveryBeforeAndAfterCaseCommit covers both crash windows and
// ensures only a confirmed saved case unlocks durable burst-message cleanup.
func TestIncidentRecoveryBeforeAndAfterCaseCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[committed], func(t *testing.T) {
			f, a := recoveryFixture(t)
			trigger, request := interruptedIncident(t, f)
			if committed {
				if _, err := a.ApplyHoneypotCase(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				// Reconciliation must work even after the original action removed the
				// member or staff archived the template: the case already exists.
				a.prepareErr = errors.New("member no longer present")
				f.validator.templateErr = honeypot.ErrTemplateUnavailable
			}
			if _, err := f.service.HandleMessage(context.Background(), message("burst")); !errors.Is(err, honeypot.ErrDuplicate) {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || len(a.attempts) != 0 {
				t.Fatal("cleanup before incident receipt", err)
			}
			if guilds, err := f.service.RecoverPending(context.Background(), 1); err != nil || len(guilds) != 0 {
				t.Fatal("active primary lease was recovered", guilds, err)
			}
			expireIncident(t, f, trigger)
			guilds, err := f.service.RecoverPending(context.Background(), 1)
			if err != nil || len(guilds) != 1 {
				t.Fatal("recovery failed", guilds, err)
			}
			if a.count() != 1 || committed && a.prepares != 0 || !committed && a.prepares != 1 {
				t.Fatal("recovery repeated/omitted case work", a.count(), a.prepares)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || len(a.attempts) != 2 {
				t.Fatal("cleanup did not resume", a.attempts, err)
			}
		})
	}
}

// TestOverlappingIncidentRecoveryWorkers proves the persisted lease admits only
// one recovery application and counter refresh across independent service objects.
func TestOverlappingIncidentRecoveryWorkers(t *testing.T) {
	f, a := recoveryFixture(t)
	trigger, _ := interruptedIncident(t, f)
	expireIncident(t, f, trigger)
	other := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			service := f.service
			if i%2 == 0 {
				service = other
			}
			if _, err := service.RecoverPending(context.Background(), 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if a.count() != 1 || a.prepares != 1 {
		t.Fatal("overlapping recovery repeated work", a.count(), a.prepares)
	}
}

// TestRecoveryRechecksPolicyBeforeNewCase keeps lost permissions, archived
// templates and newly exempt authors from creating a delayed punishment.
func TestRecoveryRechecksPolicyBeforeNewCase(t *testing.T) {
	for _, scenario := range []string{"permission", "template", "staff", "disabled", "lookup unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f, a := recoveryFixture(t)
			trigger, _ := interruptedIncident(t, f)
			expireIncident(t, f, trigger)
			switch scenario {
			case "permission":
				a.prepareErr = errors.New("bot permission lost")
			case "template":
				f.validator.templateErr = honeypot.ErrTemplateUnavailable
			case "staff":
				a.prepareErr = honeypot.ErrExempt
			case "lookup unavailable":
				a.lookupErr = errors.New("database unavailable")
			case "disabled":
				settings, _, _ := f.service.Settings(context.Background(), honeypot.Actor{GuildID: "guild-a", CanManage: true})
				if _, _, err := f.service.UpdateSettings(context.Background(), honeypot.Actor{GuildID: "guild-a", CanManage: true}, false, settings); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = f.service.RecoverPending(context.Background(), 1)
			if a.count() != 0 {
				t.Fatal("unsafe recovery applied moderation")
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || len(a.attempts) != 0 {
				t.Fatal("unsafe recovery deleted evidence", err)
			}
		})
	}
}

// TestRuntimeRecoversExpiredIncidentOnStartup exercises the actual durable
// recovery poller, including a late burst and downstream cleanup/counter refresh.
func TestRuntimeRecoversExpiredIncidentOnStartup(t *testing.T) {
	f, a := recoveryFixture(t)
	trigger, _ := interruptedIncident(t, f)
	if err := f.db.Model(&honeypot.Trigger{}).Where("id = ?", trigger.ID).Update("created_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.HandleMessage(context.Background(), message("late-burst")); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatal("pending incident lost late burst", err)
	}
	expireIncident(t, f, trigger)
	a.deleted = make(chan string, 2)
	counter := &failingCounter{}
	runtime := honeypot.NewRuntime(context.Background(), honeypot.NewDiscordAdapter(f.service), 4, 1, counter)
	defer runtime.Close()
	select {
	case <-a.deleted:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not recover incident and clean message")
	}
	runtime.Close()
	var recovered honeypot.Trigger
	if err := f.db.First(&recovered).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.Outcome != honeypot.OutcomeCreated || a.count() != 1 || counter.calls != 1 {
		t.Fatal("startup recovery repeated/lost incident", recovered, a.count(), counter.calls)
	}
}
