package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
)

func TestIdempotencyLifecycleExpiryAndFencing(t *testing.T) {
	server, client := newRedisHarness(t)
	store := NewIdempotencyStore(client, "test:idempotency:")
	ctx := context.Background()

	first, err := store.Begin(ctx, "case-create", "caller-secret-key", time.Minute, "")
	if err != nil || first.State != IdempotencyAcquired || first.LeaseToken == "" {
		t.Fatalf("unexpected acquisition: %+v err=%v", first, err)
	}
	duplicate, err := store.Begin(ctx, "case-create", "caller-secret-key", time.Minute, "")
	if err != nil || duplicate.State != IdempotencyInProgress || duplicate.LeaseToken != "" {
		t.Fatalf("unexpected in-progress replay: %+v err=%v", duplicate, err)
	}
	if err := store.Complete(ctx, "case-create", "caller-secret-key", "wrong-token", 201, []byte(`{"case_id":"case-1"}`), time.Hour); err == nil {
		t.Fatal("expected fencing to reject the wrong lease owner")
	}
	body := []byte(`{"case_id":"case-1"}`)
	if err := store.Complete(ctx, "case-create", "caller-secret-key", first.LeaseToken, 201, body, time.Hour); err != nil {
		t.Fatalf("complete: %v", err)
	}
	replay, err := store.Begin(ctx, "case-create", "caller-secret-key", time.Minute, "")
	if err != nil || replay.State != IdempotencyComplete || replay.StatusCode != 201 || string(replay.Body) != string(body) {
		t.Fatalf("unexpected completed replay: %+v err=%v", replay, err)
	}
	for _, key := range server.Keys() {
		if key == "caller-secret-key" || len(key) < len("test:idempotency:")+64 {
			t.Fatalf("expected opaque hashed Redis key, got %q", key)
		}
	}
	server.FastForward(time.Hour)
	afterExpiry, err := store.Begin(ctx, "case-create", "caller-secret-key", time.Minute, "")
	if err != nil || afterExpiry.State != IdempotencyAcquired {
		t.Fatalf("expected acquisition after TTL expiry, got %+v err=%v", afterExpiry, err)
	}

	if result, err := NewIdempotencyStore(nil, "").Begin(ctx, "scope", "key", time.Minute, ""); !errors.Is(err, ErrUnavailable) || result.State != "" {
		t.Fatalf("expected deterministic fail-closed unavailable result, got %+v err=%v", result, err)
	}
}

func TestHTTPIdempotencyMiddlewareReplaysOriginalResult(t *testing.T) {
	_, client := newRedisHarness(t)
	store := NewIdempotencyStore(client, "test:http-idempotency:")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestContext, middleware.ErrorEnvelope)
	var executions atomic.Int64
	router.POST("/cases", store.Protect("case-create", time.Hour, func(*gin.Context) string { return "actor:guild" }), func(c *gin.Context) {
		executions.Add(1)
		c.JSON(http.StatusCreated, gin.H{"case_id": "case-1"})
	})

	request := httptest.NewRequest(http.MethodPost, "/cases", nil)
	request.Header.Set("Idempotency-Key", "caller-secret-key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Idempotency-Replayed") != "" {
		t.Fatalf("unexpected original response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	originalBody := response.Body.String()

	request = httptest.NewRequest(http.MethodPost, "/cases", nil)
	request.Header.Set("Idempotency-Key", "caller-secret-key")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Idempotency-Replayed") != "true" || response.Body.String() != originalBody {
		t.Fatalf("unexpected replay response: status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	if got := executions.Load(); got != 1 {
		t.Fatalf("expected handler to execute once, got %d", got)
	}
}

func TestIdempotencyConcurrentAcquisition(t *testing.T) {
	_, client := newRedisHarness(t)
	store := NewIdempotencyStore(client, "test:idempotency-race:")
	var acquired atomic.Int64
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.Begin(context.Background(), "evidence", "same-key", time.Minute, "")
			if err != nil {
				t.Errorf("begin: %v", err)
				return
			}
			if result.State == IdempotencyAcquired {
				acquired.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := acquired.Load(); got != 1 {
		t.Fatalf("expected exactly one lease owner, got %d", got)
	}
}
