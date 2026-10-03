package interactions

import (
	"container/list"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/redis/go-redis/v9"
)

// ComponentRegistry maps "namespace:action" keys to button/select handlers and,
// separately, to modal-submit handlers. Registration happens during startup
// before the gateway opens; lookups afterwards are read-only, so no lock is needed.
type ComponentRegistry struct {
	components map[string]ui.Handler
	modals     map[string]ui.Handler
}

// NewComponentRegistry returns an empty registry.
func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{
		components: map[string]ui.Handler{},
		modals:     map[string]ui.Handler{},
	}
}

// RegisterComponent binds a button or select handler to namespace:action.
// Registering the same key twice is an error so two features cannot silently
// compete for one custom ID.
func (r *ComponentRegistry) RegisterComponent(namespace, action string, handler ui.Handler) error {
	return r.register(r.components, namespace, action, handler)
}

// RegisterModal binds a modal-submit handler to namespace:action, in a keyspace
// separate from components so a form and its opening button may share a name.
func (r *ComponentRegistry) RegisterModal(namespace, action string, handler ui.Handler) error {
	return r.register(r.modals, namespace, action, handler)
}

// LookupComponent decodes a component custom ID and returns its handler. The
// boolean is false for an unregistered key; the error reports a malformed ID.
func (r *ComponentRegistry) LookupComponent(customID string) (ui.Handler, bool, error) {
	return r.lookup(r.components, customID)
}

// LookupModal is LookupComponent for modal submissions.
func (r *ComponentRegistry) LookupModal(customID string) (ui.Handler, bool, error) {
	return r.lookup(r.modals, customID)
}

func (r *ComponentRegistry) register(target map[string]ui.Handler, namespace, action string, handler ui.Handler) error {
	if handler == nil {
		return errors.New("component handler is required")
	}
	key := Key(namespace, action)
	if key == ":" || strings.HasPrefix(key, ":") || strings.HasSuffix(key, ":") {
		return errors.New("component namespace and action are required")
	}
	if _, exists := target[key]; exists {
		return errors.New("component handler is already registered")
	}
	target[key] = handler
	return nil
}

func (r *ComponentRegistry) lookup(source map[string]ui.Handler, customID string) (ui.Handler, bool, error) {
	parsed, err := ui.DecodeCustomID(customID)
	if err != nil {
		return nil, false, err
	}
	handler, ok := source[Key(parsed.Namespace, parsed.Action)]
	return handler, ok, nil
}

// InteractionDeduper claims each Discord interaction ID exactly once within a
// TTL so a redelivered interaction never runs a handler twice. Without Redis it
// is process-local (an in-memory map bounded by maxSize); with Redis the claim
// survives restarts and is shared across replicas.
type InteractionDeduper struct {
	mu      sync.Mutex
	seen    map[string]*list.Element
	order   list.List
	ttl     time.Duration
	maxSize int
	now     func() time.Time
	redis   redis.UniversalClient
	prefix  string
}

// NewInteractionDeduper constructs a process-local duplicate boundary. Discord
// retries are short-lived and case creation also retains its durable idempotency key.
func NewInteractionDeduper(ttl time.Duration, maxSize int) *InteractionDeduper {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &InteractionDeduper{seen: make(map[string]*list.Element), ttl: ttl, maxSize: maxSize, now: time.Now}
}

// NewRedisInteractionDeduper constructs a restart-durable duplicate boundary.
// Redis errors fail closed because executing a moderation interaction twice is
// less safe than asking Discord to retry after the dependency recovers.
func NewRedisInteractionDeduper(client redis.UniversalClient, ttl time.Duration) *InteractionDeduper {
	deduper := NewInteractionDeduper(ttl, 10000)
	deduper.redis = client
	deduper.prefix = "discord:interaction:"
	return deduper
}

// Claim returns true exactly once for an interaction ID within the configured
// window. Empty IDs are never claimed. When the in-memory table is full and
// nothing has expired it fails closed rather than evicting a live claim.
func (d *InteractionDeduper) Claim(id string) bool {
	if id == "" {
		return false
	}
	if d.redis != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		claimed, err := d.redis.SetNX(ctx, d.prefix+id, "claimed", d.ttl).Result()
		if err != nil {
			slog.ErrorContext(ctx, "Discord interaction claim unavailable", "error", err)
		}
		return err == nil && claimed
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now().UTC()
	// Claims share one TTL, so insertion order is also expiration order.
	for front := d.order.Front(); front != nil; front = d.order.Front() {
		claim := front.Value.(interactionClaim)
		if claim.expires.After(now) {
			break
		}
		delete(d.seen, claim.id)
		d.order.Remove(front)
	}
	if _, exists := d.seen[id]; exists {
		return false
	}
	if len(d.seen) >= d.maxSize {
		return false
	}
	d.seen[id] = d.order.PushBack(interactionClaim{id: id, expires: now.Add(d.ttl)})
	return true
}

// interactionClaim retains an accepted interaction until its replay window ends.
type interactionClaim struct {
	id      string
	expires time.Time
}
