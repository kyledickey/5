package quack

import (
	"context"

	"github.com/quackdiscord/bot/internal/quack/idutil"
)

// The functions below re-export idutil's trace helpers so transports and
// workers that already depend on quack do not need a second import for the
// context keys.

// NewTraceID creates a sortable random identifier for tracing across HTTP, Discord, and workers.
func NewTraceID() string {
	return idutil.NewTraceID()
}

// ContextWithRequestID returns a derived context carrying the request id.
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return idutil.ContextWithRequestID(ctx, requestID)
}

// ContextWithCorrelationID returns a derived context carrying the correlation id.
func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return idutil.ContextWithCorrelationID(ctx, correlationID)
}

// ContextWithTrace returns a derived context carrying both the request and correlation ids.
func ContextWithTrace(ctx context.Context, requestID, correlationID string) context.Context {
	return idutil.ContextWithTrace(ctx, requestID, correlationID)
}

// RequestIDFromContext returns the request id stored on ctx, or "" when absent.
func RequestIDFromContext(ctx context.Context) string {
	return idutil.RequestIDFromContext(ctx)
}

// CorrelationIDFromContext returns the correlation id stored on ctx, or "" when absent.
func CorrelationIDFromContext(ctx context.Context) string {
	return idutil.CorrelationIDFromContext(ctx)
}

// TraceIDsFromContext returns the request id and correlation id stored on ctx; either may be "".
func TraceIDsFromContext(ctx context.Context) (string, string) {
	return idutil.TraceIDsFromContext(ctx)
}
