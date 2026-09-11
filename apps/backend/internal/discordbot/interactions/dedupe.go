package interactions

import (
	"container/list"
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

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
