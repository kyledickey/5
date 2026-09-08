package commands

import (
	"context"
	"testing"

	"github.com/redis/go-redis/v9"
)

// hashOnlyStore proves command caching requires no application repository methods.
type hashOnlyStore map[string][]byte

// HashGet models Redis's missing-field contract without a live server.
func (s hashOnlyStore) HashGet(_ context.Context, key, field string) ([]byte, error) {
	body, ok := s[key+"|"+field]
	if !ok {
		return nil, redis.Nil
	}
	return body, nil
}

// HashSet records encoded entries for later synchronization runs.
func (s hashOnlyStore) HashSet(_ context.Context, key, field string, value []byte) error {
	s[key+"|"+field] = value
	return nil
}

func TestCommandCacheWithHashOnlyCapability(t *testing.T) {
	ctx := context.Background()
	store := hashOnlyStore{}
	cache := newRedisCommandCache(store)
	if entry, err := cache.Get(ctx, "global", "case"); err != nil || entry != nil {
		t.Fatalf("missing cache field: entry=%+v err=%v", entry, err)
	}
	client := &fakeCommandClient{}
	syncer := testCommandSyncer(client, cache)
	specs := []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}
	for range 2 {
		if err := syncer.Sync(ctx, specs); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.created) != 1 || len(client.edited) != 0 {
		t.Fatalf("cached sync should create once: creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if _, ok := store["discord:commands:global:hashes|case"]; !ok {
		t.Fatal("cache key format changed")
	}
	if entry, err := cache.Get(ctx, "guild:other", "case"); err != nil || entry != nil {
		t.Fatalf("scope leaked: entry=%+v err=%v", entry, err)
	}
}

func TestCommandCacheWithoutInfrastructure(t *testing.T) {
	cache := newRedisCommandCache(nil)
	ctx := context.Background()
	if err := cache.Set(ctx, "global", "case", commandCacheEntry{Hash: "ignored"}); err != nil {
		t.Fatal(err)
	}
	if entry, err := cache.Get(ctx, "global", "case"); err != nil || entry != nil {
		t.Fatalf("disabled cache: entry=%+v err=%v", entry, err)
	}
	client := &fakeCommandClient{}
	if err := testCommandSyncer(client, cache).Sync(ctx, []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatal(err)
	}
	if len(client.created) != 1 {
		t.Fatalf("uncached sync created %d commands", len(client.created))
	}
}
