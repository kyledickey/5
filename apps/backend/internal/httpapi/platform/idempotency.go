package platform

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxIdempotencyResponseBytes = 64 << 10

// IdempotencyState identifies whether a caller acquired work or observed an existing result.
type IdempotencyState string

const (
	IdempotencyConflict   IdempotencyState = "conflict"
	IdempotencyAcquired   IdempotencyState = "acquired"
	IdempotencyInProgress IdempotencyState = "in_progress"
	IdempotencyComplete   IdempotencyState = "complete"
)

// IdempotencyResult is the deterministic outcome returned for an idempotency key.
type IdempotencyResult struct {
	State      IdempotencyState
	LeaseToken string
	StatusCode int
	Body       []byte
}

// IdempotencyStore coordinates externally retried writes using fenced Redis leases.
type IdempotencyStore struct {
	client redis.UniversalClient
	prefix string
}

// beginIdempotencyScript atomically returns an existing state or creates one fenced lease.
var beginIdempotencyScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 1 then
  if (redis.call("HGET", KEYS[1], "fingerprint") or "") ~= ARGV[3] then return {"conflict", "", "0", "", redis.call("PTTL", KEYS[1])} end
  return {redis.call("HGET", KEYS[1], "state"), "", redis.call("HGET", KEYS[1], "status") or "0", redis.call("HGET", KEYS[1], "body") or "", redis.call("PTTL", KEYS[1])}
end
redis.call("HSET", KEYS[1], "state", "in_progress", "token", ARGV[1], "status", "0", "body", "", "fingerprint", ARGV[3])
redis.call("PEXPIRE", KEYS[1], ARGV[2])
return {"acquired", ARGV[1], "0", "", redis.call("PTTL", KEYS[1])}
`)

// completeIdempotencyScript stores a result only for the current in-progress lease owner.
var completeIdempotencyScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then return -1 end
if redis.call("HGET", KEYS[1], "token") ~= ARGV[1] then return -2 end
if redis.call("HGET", KEYS[1], "state") ~= "in_progress" then return -3 end
redis.call("HSET", KEYS[1], "state", "complete", "status", ARGV[2], "body", ARGV[3])
redis.call("PEXPIRE", KEYS[1], ARGV[4])
return 1
`)

// NewIdempotencyStore constructs a fail-closed Redis-backed idempotency store.
func NewIdempotencyStore(client redis.UniversalClient, prefix string) *IdempotencyStore {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "http:idempotency:"
	}
	return &IdempotencyStore{client: client, prefix: prefix}
}

// Begin acquires a fenced lease or returns the original in-progress/completed state without executing work twice.
func (s *IdempotencyStore) Begin(ctx context.Context, scope, key string, ttl time.Duration, fingerprint string) (IdempotencyResult, error) {
	if s == nil || s.client == nil {
		return IdempotencyResult{}, ErrUnavailable
	}
	if strings.TrimSpace(scope) == "" || strings.TrimSpace(key) == "" || ttl <= 0 {
		return IdempotencyResult{}, fmt.Errorf("invalid idempotency request")
	}
	token, err := randomToken()
	if err != nil {
		return IdempotencyResult{}, fmt.Errorf("generate idempotency lease: %w", err)
	}
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	raw, err := beginIdempotencyScript.Run(ctx, s.client, []string{s.redisKey(scope, key)}, token, ttlMillis, fingerprint).Result()
	if err != nil {
		return IdempotencyResult{}, fmt.Errorf("%w: begin idempotency: %v", ErrUnavailable, err)
	}
	values, ok := raw.([]any)
	if !ok || len(values) != 5 {
		return IdempotencyResult{}, fmt.Errorf("%w: invalid idempotency response", ErrUnavailable)
	}
	state, err := redisString(values[0])
	if err != nil {
		return IdempotencyResult{}, fmt.Errorf("%w: invalid idempotency state", ErrUnavailable)
	}
	leaseToken, _ := redisString(values[1])
	statusCode, err := redisInt64(values[2])
	if err != nil {
		return IdempotencyResult{}, fmt.Errorf("%w: invalid idempotency status", ErrUnavailable)
	}
	body, _ := redisString(values[3])
	if remainingMillis, err := redisInt64(values[4]); err != nil || remainingMillis < 0 {
		return IdempotencyResult{}, fmt.Errorf("%w: invalid idempotency TTL", ErrUnavailable)
	}
	return IdempotencyResult{
		State:      IdempotencyState(state),
		LeaseToken: leaseToken,
		StatusCode: int(statusCode),
		Body:       []byte(body),
	}, nil
}

// Complete stores the original response if and only if the caller still owns the in-progress lease.
func (s *IdempotencyStore) Complete(ctx context.Context, scope, key, leaseToken string, statusCode int, body []byte, ttl time.Duration) error {
	if s == nil || s.client == nil {
		return ErrUnavailable
	}
	if strings.TrimSpace(scope) == "" || strings.TrimSpace(key) == "" || leaseToken == "" || statusCode < 100 || statusCode > 599 || ttl <= 0 {
		return fmt.Errorf("invalid idempotency completion")
	}
	if len(body) > maxIdempotencyResponseBytes {
		return fmt.Errorf("idempotency response exceeds %d bytes", maxIdempotencyResponseBytes)
	}
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	result, err := completeIdempotencyScript.Run(ctx, s.client, []string{s.redisKey(scope, key)}, leaseToken, statusCode, body, ttlMillis).Int64()
	if err != nil {
		return fmt.Errorf("%w: complete idempotency: %v", ErrUnavailable, err)
	}
	switch result {
	case 1:
		return nil
	case -1:
		return errors.New("idempotency lease expired")
	case -2:
		return errors.New("idempotency lease ownership lost")
	default:
		return errors.New("idempotency operation already completed")
	}
}

// redisKey hashes caller-controlled scope and key material before storage.
func (s *IdempotencyStore) redisKey(scope, key string) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + key))
	return s.prefix + hex.EncodeToString(digest[:])
}

// randomToken returns an unguessable fencing token.
func randomToken() (string, error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(body[:]), nil
}

// redisString normalizes Redis bulk-string values.
func redisString(value any) (string, error) {
	switch value := value.(type) {
	case string:
		return value, nil
	case []byte:
		return string(value), nil
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("unexpected Redis string type %T", value)
	}
}
