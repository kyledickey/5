package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	r "github.com/redis/go-redis/v9"
)

// commandCacheEntry pairs a Discord command ID with the last synchronized definition hash.
type commandCacheEntry struct {
	DiscordCommandID string `json:"discord_command_id"`
	Hash             string `json:"hash"`
}

// commandHashCache remembers synchronized definitions independently for each Discord scope.
type commandHashCache interface {
	Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error)
	Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error
}

// noopCommandCache leaves synchronization enabled when no cache capability is supplied.
type noopCommandCache struct{}

// Get reports a cache miss so synchronization compares the live Discord definition.
func (noopCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	return nil, nil
}

// Set discards fingerprints when caching is disabled.
func (noopCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	return nil
}

// CommandHashStore provides only the hash operations used to remember synchronized
// Discord command definitions. Missing fields must return redis.Nil.
type CommandHashStore interface {
	HashGet(ctx context.Context, key, field string) ([]byte, error)
	HashSet(ctx context.Context, key, field string, value []byte) error
}

// redisCommandCache encodes fingerprints in per-scope hashes using only hash storage.
type redisCommandCache struct {
	store CommandHashStore
}

// newRedisCommandCache uses the supplied hash capability, or disables caching when absent.
func newRedisCommandCache(store CommandHashStore) commandHashCache {
	if store == nil {
		return noopCommandCache{}
	}
	return redisCommandCache{store: store}
}

// Get decodes a stored fingerprint, treating absent Redis fields as cache misses.
func (c redisCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	body, err := c.store.HashGet(ctx, commandCacheKey(scope), commandName)
	if err != nil {
		if errors.Is(err, r.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read command cache: %w", err)
	}

	var entry commandCacheEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decode command cache: %w", err)
	}
	return &entry, nil
}

// Set persists the synchronized command ID and hash without expiring the fingerprint.
func (c redisCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode command cache: %w", err)
	}
	if err := c.store.HashSet(ctx, commandCacheKey(scope), commandName, body); err != nil {
		return fmt.Errorf("write command cache: %w", err)
	}
	return nil
}

// commandCacheKey isolates global and guild fingerprints within the existing Redis namespace.
func commandCacheKey(scope string) string {
	return "discord:commands:" + scope + ":hashes"
}
