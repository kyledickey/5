// Package platform provides Redis-backed HTTP safety primitives for feature registrars.
package platform

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/redis/go-redis/v9"
)

const idempotencyKeyHeader = "Idempotency-Key"

// RedisProvider is the narrow adapter contract used by the integration checkpoint to construct HTTP safety primitives.
type RedisProvider interface {
	Redis() *redis.Client
}

// Primitives groups the reusable rate-limit and idempotency contracts exposed to feature registrars.
type Primitives struct {
	RateLimits  *RateLimiter
	Idempotency *IdempotencyStore
}

// New builds primitives from an adapter that explicitly exposes its owned Redis
// client. A nil provider yields fail-closed primitives rather than no limit.
func New(provider RedisProvider) Primitives {
	if provider == nil {
		return Primitives{RateLimits: NewRateLimiter(nil, ""), Idempotency: NewIdempotencyStore(nil, "")}
	}
	client := provider.Redis()
	return Primitives{RateLimits: NewRateLimiter(client, ""), Idempotency: NewIdempotencyStore(client, "")}
}

// SubjectFunc returns a non-secret actor or guild scope used only as hashed limiter input.
type SubjectFunc func(*gin.Context) string

// Limit installs a fail-closed rate limit for one endpoint class.
func (l *RateLimiter) Limit(class string, limit RateLimit, subject SubjectFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(class) == "" || subject == nil {
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "rate-limit configuration failed")
			return
		}
		decision, err := l.Allow(c.Request.Context(), class+":"+subject(c), limit)
		if err != nil {
			if errors.Is(err, ErrUnavailable) {
				apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "rate-limit service unavailable")
				return
			}
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "rate-limit configuration failed")
			return
		}
		c.Header("RateLimit-Limit", strconv.Itoa(limit.Maximum))
		c.Header("RateLimit-Remaining", strconv.Itoa(decision.Remaining))
		if !decision.Allowed {
			retrySeconds := int(decision.RetryAfter.Round(time.Second).Seconds())
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			c.Header("Retry-After", strconv.Itoa(retrySeconds))
			apierror.Write(c, http.StatusTooManyRequests, apierror.CodeRateLimited, "rate limit exceeded")
			return
		}
		c.Next()
	}
}

// ClientIPSubject scopes public OAuth limits to Gin's canonical client address.
func ClientIPSubject(c *gin.Context) string {
	if c == nil {
		return "unknown"
	}
	return fmt.Sprintf("ip:%s", c.ClientIP())
}

// Protect requires an idempotency key and returns the original/in-progress result without executing a write twice.
func (s *IdempotencyStore) Protect(class string, ttl time.Duration, subject SubjectFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.TrimSpace(class) == "" || subject == nil || ttl <= 0 {
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "idempotency configuration failed")
			return
		}
		key := strings.TrimSpace(c.GetHeader(idempotencyKeyHeader))
		if key == "" || len(key) > 256 {
			apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "a valid Idempotency-Key header is required")
			return
		}
		scope := class + ":" + subject(c) + ":" + c.Request.Method + ":" + c.Request.URL.EscapedPath()
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, (4<<20)+1))
		if err != nil || len(body) > 4<<20 {
			apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "request body is unavailable or too large")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		digest := sha256.Sum256(append([]byte(c.Request.URL.RawQuery+"\x00"+c.ContentType()+"\x00"), body...))
		result, err := s.Begin(c.Request.Context(), scope, key, ttl, hex.EncodeToString(digest[:]))
		if err != nil {
			if errors.Is(err, ErrUnavailable) {
				apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "idempotency service unavailable")
				return
			}
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "idempotency configuration failed")
			return
		}
		switch result.State {
		case IdempotencyConflict:
			apierror.Write(c, http.StatusConflict, apierror.CodeConflict, "Idempotency-Key was already used with a different request")
			return
		case IdempotencyInProgress:
			c.Header("Retry-After", "1")
			apierror.Write(c, http.StatusConflict, apierror.CodeConflict, "an identical request is still in progress")
			return
		case IdempotencyComplete:
			c.Abort()
			c.Header("Idempotency-Replayed", "true")
			c.Header("Content-Type", "application/json; charset=utf-8")
			c.Status(result.StatusCode)
			_, _ = c.Writer.Write(result.Body)
			return
		case IdempotencyAcquired:
			// Continue below as the single lease owner.
		default:
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "idempotency service unavailable")
			return
		}

		// The response is buffered so it is only sent once Redis has durably
		// recorded it; otherwise a retry could observe a different result.
		original := c.Writer
		captured := &middleware.BufferedWriter{ResponseWriter: original}
		c.Writer = captured
		defer func() { c.Writer = original }()
		c.Next()
		c.Writer = original
		if err := s.Complete(c.Request.Context(), scope, key, result.LeaseToken, captured.Status(), captured.Body(), ttl); err != nil {
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "idempotency result could not be recorded")
			return
		}
		original.WriteHeader(captured.Status())
		if len(captured.Body()) > 0 {
			_, _ = original.Write(captured.Body())
		}
	}
}
