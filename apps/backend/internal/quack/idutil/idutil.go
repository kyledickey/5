// Package idutil owns identifier generation and request-trace propagation for
// every transport in Quack: ULIDs for durable records and the request/correlation
// IDs carried through context by HTTP, Discord, and worker code.
//
// It depends only on the standard library and the ULID library. The domain
// package (internal/quack) re-exports the trace helpers for convenience, but
// packages that only need identifiers or traces should import idutil directly
// so that they do not pull the whole application core into their dependency
// graph (internal/httpapi/apierror and internal/logging rely on this).
package idutil

import (
	"context"
	"strings"
	"unicode"

	"github.com/oklog/ulid/v2"
)

// NewULID returns a new 26-character, lexicographically sortable ULID. Every
// durable record and trace identifier in Quack uses this format so IDs sort by
// creation time.
func NewULID() string { return ulid.Make().String() }

// traceContextKey is the private context key type for trace identifiers so
// other packages cannot collide with or read the values except through the
// helpers below.
type traceContextKey string

const (
	requestIDContextKey     traceContextKey = "request_id"
	correlationIDContextKey traceContextKey = "correlation_id"
)

// NewTraceID returns a fresh ULID for use as a request or correlation ID when
// the caller did not supply one.
func NewTraceID() string { return NewULID() }

// ContextWithTrace attaches both trace identifiers to ctx. An unsafe or empty
// request ID is replaced with a new one; an unsafe or empty correlation ID
// defaults to the request ID so a single request always has a correlation ID.
func ContextWithTrace(ctx context.Context, requestID, correlationID string) context.Context {
	ctx = ContextWithRequestID(ctx, requestID)
	if NormalizeTraceID(correlationID) == "" {
		correlationID = RequestIDFromContext(ctx)
	}
	return ContextWithCorrelationID(ctx, correlationID)
}

// ContextWithRequestID attaches a request ID to ctx, generating a new one when
// requestID is empty or fails NormalizeTraceID.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	requestID = NormalizeTraceID(requestID)
	if requestID == "" {
		requestID = NewTraceID()
	}
	return context.WithValue(ctx, requestIDContextKey, requestID)
}

// ContextWithCorrelationID attaches a correlation ID to ctx, generating a new
// one when correlationID is empty or fails NormalizeTraceID.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	correlationID = NormalizeTraceID(correlationID)
	if correlationID == "" {
		correlationID = NewTraceID()
	}
	return context.WithValue(ctx, correlationIDContextKey, correlationID)
}

// RequestIDFromContext returns the request ID stored in ctx, or "" when none
// is present. A nil ctx is tolerated and yields "".
func RequestIDFromContext(ctx context.Context) string {
	return traceIDFromContext(ctx, requestIDContextKey)
}

// CorrelationIDFromContext returns the correlation ID stored in ctx, or ""
// when none is present. A nil ctx is tolerated and yields "".
func CorrelationIDFromContext(ctx context.Context) string {
	return traceIDFromContext(ctx, correlationIDContextKey)
}

// TraceIDsFromContext returns the request and correlation IDs from ctx. When
// only a request ID is present it is also returned as the correlation ID so
// log lines and error envelopes never show a blank correlation for a traced
// request.
func TraceIDsFromContext(ctx context.Context) (string, string) {
	requestID := RequestIDFromContext(ctx)
	correlationID := CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = requestID
	}
	return requestID, correlationID
}

// NormalizeTraceID trims value and returns it unchanged when it is a safe
// trace identifier: at most 128 characters consisting only of letters, digits,
// '-', '_', '.', or ':'. Anything else returns "" so caller-supplied header
// values cannot inject log or response content.
func NormalizeTraceID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return ""
	}
	return value
}

func traceIDFromContext(ctx context.Context, key traceContextKey) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(key).(string)
	return NormalizeTraceID(value)
}
