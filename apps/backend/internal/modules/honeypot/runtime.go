package honeypot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
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

// Runtime is a bounded, independently drainable honeypot gateway worker pool.
type Runtime struct {
	adapter *DiscordAdapter
	events  chan Message
	mu      sync.RWMutex
	closed  bool
	wg      sync.WaitGroup
}

// NewRuntime starts isolated workers so gateway handling never runs on a moderation action queue.
func NewRuntime(ctx context.Context, adapter *DiscordAdapter, capacity, workers int) *Runtime {
	if capacity < 1 {
		capacity = 256
	}
	if workers < 1 {
		workers = 1
	}
	runtime := &Runtime{adapter: adapter, events: make(chan Message, capacity)}
	for range workers {
		runtime.wg.Add(1)
		go func() {
			defer runtime.wg.Done()
			for message := range runtime.events {
				runtime.process(ctx, message)
			}
		}()
	}
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

// Close drains accepted messages exactly once without stopping another module.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.events)
	}
	r.mu.Unlock()
	r.wg.Wait()
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
	if _, err := r.adapter.HandleMessage(ctx, event); err != nil && !errors.Is(err, ErrDuplicate) && !errors.Is(err, ErrExempt) && !errors.Is(err, ErrNotTrigger) && !errors.Is(err, ErrDisabled) {
		slog.ErrorContext(ctx, "Honeypot event failed", "guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
	}
}
