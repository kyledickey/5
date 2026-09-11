package interactions_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/redis/go-redis/v9"
)

// TestInteractionDeduperClaimsOneConcurrentDelivery proves that of many
// simultaneous claims for one interaction ID exactly one succeeds.
func TestInteractionDeduperClaimsOneConcurrentDelivery(t *testing.T) {
	deduper := interactions.NewInteractionDeduper(time.Minute, 100)
	var claimed atomic.Int64
	var wait sync.WaitGroup
	for range 100 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if deduper.Claim("interaction-1") {
				claimed.Add(1)
			}
		}()
	}
	wait.Wait()
	if claimed.Load() != 1 {
		t.Fatalf("expected exactly one claim, got %d", claimed.Load())
	}
}

// TestRedisInteractionDeduperSurvivesRestartAndFailsClosed proves a Redis claim
// is visible to a second process and that an unreachable Redis refuses claims.
func TestRedisInteractionDeduperSurvivesRestartAndFailsClosed(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := interactions.NewRedisInteractionDeduper(client, time.Minute)
	if !first.Claim("interaction-1") {
		t.Fatal("expected first process to claim interaction")
	}
	second := interactions.NewRedisInteractionDeduper(client, time.Minute)
	if second.Claim("interaction-1") {
		t.Fatal("expected restarted process to observe durable claim")
	}
	server.Close()
	if second.Claim("interaction-2") {
		t.Fatal("expected unavailable Redis to fail closed")
	}
	if err := client.Ping(context.Background()).Err(); err == nil {
		t.Fatal("expected Redis fixture to be unavailable")
	}
}

// TestInteractionCapacityCannotEvictLiveClaims proves a full table refuses new
// IDs instead of forgetting an unexpired one.
func TestInteractionCapacityCannotEvictLiveClaims(t *testing.T) {
	deduper := interactions.NewInteractionDeduper(time.Minute, 1)
	if !deduper.Claim("first") {
		t.Fatal("initial claim rejected")
	}
	if deduper.Claim("second") {
		t.Fatal("capacity must fail closed until a claim expires")
	}
	if deduper.Claim("first") {
		t.Fatal("capacity pressure forgot a live interaction")
	}
}
