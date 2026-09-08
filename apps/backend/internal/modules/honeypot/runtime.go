package honeypot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"
)

// ErrQueueFull reports deliberate gateway shedding before any trigger is claimed.
var ErrQueueFull = errors.New("honeypot trigger queue is full")

// IntentRequirements describes the only Discord gateway capabilities needed by an enabled honeypot runtime.
type IntentRequirements struct {
	Guilds, GuildMessages bool
	MessageContent        bool
}

// RequiredIntents returns no optional intents when no guild enables honeypots.
// Enabled honeypots need guild/channel deletion and message-create metadata, but never privileged message content.
func RequiredIntents(anyGuildEnabled bool) IntentRequirements {
	if !anyGuildEnabled {
		return IntentRequirements{}
	}
	return IntentRequirements{Guilds: true, GuildMessages: true}
}

// DiscordAdapter translates dependency-neutral gateway projections into module operations.
type DiscordAdapter struct{ service *Service }

// NewDiscordAdapter constructs the honeypot gateway adapter without central registration.
func NewDiscordAdapter(service *Service) *DiscordAdapter { return &DiscordAdapter{service: service} }

// HandleMessage processes one projected Discord message.
func (a *DiscordAdapter) HandleMessage(ctx context.Context, message Message) (ApplyResult, error) {
	if a == nil || a.service == nil {
		return ApplyResult{}, errors.New("honeypot Discord adapter is not configured")
	}
	return a.service.HandleMessage(ctx, message)
}

// HandleDeletedChannel disables an affected guild without touching any other module.
func (a *DiscordAdapter) HandleDeletedChannel(ctx context.Context, guildID, channelID string) error {
	if a == nil || a.service == nil {
		return errors.New("honeypot Discord adapter is not configured")
	}
	return a.service.HandleDeletedChannel(ctx, guildID, channelID)
}

// HandleTemplateUnavailable disables an affected configuration after template archive or compatibility drift.
func (a *DiscordAdapter) HandleTemplateUnavailable(ctx context.Context, guildID, templateID string) error {
	if a == nil || a.service == nil {
		return errors.New("honeypot Discord adapter is not configured")
	}
	return a.service.HandleTemplateUnavailable(ctx, guildID, templateID)
}

// IncidentObserver refreshes derived Discord presentation after a case is saved.
// Its failure must never undo or repeat the moderation operation.
type IncidentObserver interface {
	IncidentCreated(context.Context, string) error
}

// PresentationWorker runs optional derived warning delivery independently of
// moderation and message cleanup. Cancellation must interrupt transport work.
type PresentationWorker interface{ RunPresentation(context.Context) }

// Runtime is a bounded, independently drainable honeypot gateway worker pool.
type Runtime struct {
	observer      IncidentObserver
	adapter       *DiscordAdapter
	events        chan Message
	mu            sync.RWMutex
	closed        bool
	wg            sync.WaitGroup
	cleanupWG     sync.WaitGroup
	cleanupCtx    context.Context
	cancelCleanup context.CancelFunc
}

// NewRuntime starts isolated workers so gateway handling never runs on a moderation action queue.
func NewRuntime(ctx context.Context, adapter *DiscordAdapter, capacity, workers int, observers ...IncidentObserver) *Runtime {
	if capacity < 1 {
		capacity = 256
	}
	if workers < 1 {
		workers = 1
	}
	cleanupCtx, cancelCleanup := context.WithCancel(ctx)
	runtime := &Runtime{adapter: adapter, events: make(chan Message, capacity), cleanupCtx: cleanupCtx, cancelCleanup: cancelCleanup}
	if len(observers) > 0 {
		runtime.observer = observers[0]
	}
	if presenter, ok := runtime.observer.(PresentationWorker); ok {
		runtime.cleanupWG.Add(1)
		go func() { defer runtime.cleanupWG.Done(); presenter.RunPresentation(cleanupCtx) }()
	}
	for range workers {
		runtime.wg.Add(1)
		go func() {
			defer runtime.wg.Done()
			for message := range runtime.events {
				runtime.process(ctx, message)
			}
		}()
	}
	runtime.cleanupWG.Add(1)
	go func() {
		defer runtime.cleanupWG.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			runtime.recover(cleanupCtx)
			runtime.clean(cleanupCtx, 8)
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return runtime
}

// Submit accepts a message without blocking the Discord gateway.
func (r *Runtime) Submit(message Message) error {
	if r == nil {
		return errors.New("honeypot runtime is not configured")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return errors.New("honeypot runtime is closed")
	}
	select {
	case r.events <- message:
		return nil
	default:
		return ErrQueueFull
	}
}

// Close cancels cleanup promptly while draining accepted moderation messages.
// Interrupted deletes retain their leases for restart recovery.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.events)
		r.cancelCleanup()
	}
	r.mu.Unlock()
	r.wg.Wait()
	r.cleanupWG.Wait()
}

// process contains adapter failures within one job so a bad event cannot stop
// moderation or take down unrelated workers. Message contents are never logged.
func (r *Runtime) process(ctx context.Context, event Message) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "Honeypot worker panicked", "guild_id", event.GuildID,
				"panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
		}
	}()
	// Cleanup is safe even after duplicate delivery: persistence only admits
	// messages linked to a saved case, never exempt or failed incident messages.
	defer r.clean(r.cleanupCtx, 1)
	if _, err := r.adapter.HandleMessage(ctx, event); err != nil {
		if !errors.Is(err, ErrDuplicate) && !errors.Is(err, ErrExempt) && !errors.Is(err, ErrNotTrigger) && !errors.Is(err, ErrDisabled) {
			slog.ErrorContext(ctx, "Honeypot event failed", "guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	if r.observer != nil {
		if err := r.observer.IncidentCreated(ctx, event.GuildID); err != nil {
			slog.WarnContext(ctx, "Honeypot counter update failed", "guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
	}
}

// clean polls persisted cleanup separately from moderation and logs failures
// without message content. Failed deletion remains due for a later worker/restart.
func (r *Runtime) clean(ctx context.Context, limit int) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "Honeypot cleanup worker panicked; lease will recover", "panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
		}
	}()
	if r.adapter == nil || r.adapter.service == nil || ctx.Err() != nil {
		return
	}
	if err := r.adapter.service.ProcessCleanups(ctx, limit); err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "Honeypot message cleanup will retry", "error_type", fmt.Sprintf("%T", err))
	}
}

// recover reconciles a bounded batch before cleanup polling so newly recovered
// case receipts release their source messages for deletion on the same tick.
func (r *Runtime) recover(ctx context.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "Honeypot recovery worker panicked; lease will recover", "panic_type", fmt.Sprintf("%T", recovered))
		}
	}()
	if r.adapter == nil || r.adapter.service == nil || ctx.Err() != nil {
		return
	}
	guilds, err := r.adapter.service.RecoverPending(ctx, 2)
	if err != nil && ctx.Err() == nil {
		slog.WarnContext(ctx, "Honeypot incident recovery will retry", "error_type", fmt.Sprintf("%T", err))
	}
	if r.observer != nil {
		for _, guildID := range guilds {
			if err := r.observer.IncidentCreated(ctx, guildID); err != nil {
				slog.WarnContext(ctx, "Honeypot recovered counter update failed", "guild_id", guildID)
			}
		}
	}
}
