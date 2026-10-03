package platform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrUnavailable marks a fail-closed safety primitive whose Redis dependency cannot make a decision.
var ErrUnavailable = errors.New("HTTP safety primitive unavailable")

// RateLimit describes a fixed-window allowance.
type RateLimit struct {
	Maximum int
	Window  time.Duration
}

// RateDecision reports whether an operation may proceed and when capacity returns.
type RateDecision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// RateLimiter enforces atomic fixed-window limits in Redis and fails closed when Redis is unavailable.
type RateLimiter struct {
	client redis.UniversalClient
	prefix string
}

// rateLimitScript atomically creates and consumes one fixed-window counter.
var rateLimitScript = redis.NewScript(`
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {current, ttl}
`)

// NewRateLimiter constructs a fail-closed Redis-backed limiter.
func NewRateLimiter(client redis.UniversalClient, prefix string) *RateLimiter {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "http:rate:"
	}
	return &RateLimiter{client: client, prefix: prefix}
}

// Allow atomically consumes one fixed-window allowance for an opaque subject key.
func (l *RateLimiter) Allow(ctx context.Context, subject string, limit RateLimit) (RateDecision, error) {
	if l == nil || l.client == nil {
		return RateDecision{}, ErrUnavailable
	}
	if strings.TrimSpace(subject) == "" || limit.Maximum <= 0 || limit.Window <= 0 {
		return RateDecision{}, fmt.Errorf("invalid rate-limit request")
	}
	windowMillis := limit.Window.Milliseconds()
	if windowMillis < 1 {
		windowMillis = 1
	}
	result, err := rateLimitScript.Run(ctx, l.client, []string{l.key(subject)}, windowMillis).Result()
	if err != nil {
		return RateDecision{}, fmt.Errorf("%w: rate limit: %v", ErrUnavailable, err)
	}
	values, ok := result.([]any)
	if !ok || len(values) != 2 {
		return RateDecision{}, fmt.Errorf("%w: invalid rate-limit response", ErrUnavailable)
	}
	current, err := redisInt64(values[0])
	if err != nil {
		return RateDecision{}, fmt.Errorf("%w: invalid rate-limit counter", ErrUnavailable)
	}
	ttlMillis, err := redisInt64(values[1])
	if err != nil {
		return RateDecision{}, fmt.Errorf("%w: invalid rate-limit TTL", ErrUnavailable)
	}
	if ttlMillis < 0 {
		ttlMillis = windowMillis
	}
	remaining := limit.Maximum - int(current)
	if remaining < 0 {
		remaining = 0
	}
	return RateDecision{
		Allowed:    current <= int64(limit.Maximum),
		Remaining:  remaining,
		RetryAfter: time.Duration(ttlMillis) * time.Millisecond,
	}, nil
}

// key hashes actor-controlled material so Redis keys and operational tooling never expose identities or credentials.
func (l *RateLimiter) key(subject string) string {
	digest := sha256.Sum256([]byte(subject))
	return l.prefix + hex.EncodeToString(digest[:])
}

// redisInt64 normalizes integer values returned by Redis and miniredis.
func redisInt64(value any) (int64, error) {
	switch value := value.(type) {
	case int64:
		return value, nil
	case string:
		return strconv.ParseInt(value, 10, 64)
	case []byte:
		return strconv.ParseInt(string(value), 10, 64)
	default:
		return 0, fmt.Errorf("unexpected Redis integer type %T", value)
	}
}
