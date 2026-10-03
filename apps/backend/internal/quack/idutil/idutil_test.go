package idutil

import (
	"context"
	"strings"
	"testing"
)

// TestNewULIDProducesUniqueSortableIdentifiers proves consecutive ULIDs are 26
// characters, distinct, and never sort before an earlier one.
func TestNewULIDProducesUniqueSortableIdentifiers(t *testing.T) {
	previous := ""
	seen := map[string]bool{}
	for range 200 {
		id := NewULID()
		if len(id) != 26 || seen[id] || id < previous {
			t.Fatalf("unexpected ULID %q after %q", id, previous)
		}
		seen[id] = true
		previous = id
	}
	if NewTraceID() == "" {
		t.Fatal("NewTraceID returned an empty identifier")
	}
}

// TestNormalizeTraceIDAcceptsOnlySafeIdentifiers proves whitespace is trimmed and
// oversized or unsafe header values are rejected rather than propagated.
func TestNormalizeTraceIDAcceptsOnlySafeIdentifiers(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "trimmed", input: "  req-1.a:b_c  ", want: "req-1.a:b_c"},
		{name: "empty", input: "   ", want: ""},
		{name: "too long", input: strings.Repeat("a", 129), want: ""},
		{name: "max length", input: strings.Repeat("a", 128), want: strings.Repeat("a", 128)},
		{name: "newline injection", input: "req\nlevel=error", want: ""},
		{name: "space inside", input: "req 1", want: ""},
		{name: "slash", input: "req/1", want: ""},
		{name: "unicode letters", input: "réq1", want: "réq1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeTraceID(test.input); got != test.want {
				t.Fatalf("NormalizeTraceID(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

// TestContextWithTraceGeneratesAndDefaultsIdentifiers proves missing or unsafe
// IDs are replaced and a missing correlation ID defaults to the request ID.
func TestContextWithTraceGeneratesAndDefaultsIdentifiers(t *testing.T) {
	ctx := ContextWithTrace(context.Background(), "request-1", "")
	if RequestIDFromContext(ctx) != "request-1" || CorrelationIDFromContext(ctx) != "request-1" {
		t.Fatalf("correlation did not default to request: %q %q", RequestIDFromContext(ctx), CorrelationIDFromContext(ctx))
	}

	ctx = ContextWithTrace(context.Background(), "bad value\n", "corr-1")
	requestID := RequestIDFromContext(ctx)
	if requestID == "" || strings.Contains(requestID, "bad") || CorrelationIDFromContext(ctx) != "corr-1" {
		t.Fatalf("unsafe request ID was not replaced: %q %q", requestID, CorrelationIDFromContext(ctx))
	}

	ctx = ContextWithTrace(context.Background(), "", "")
	requestID, correlationID := TraceIDsFromContext(ctx)
	if requestID == "" || correlationID != requestID {
		t.Fatalf("generated trace should share one identifier: %q %q", requestID, correlationID)
	}
}

// TestTraceIDsFromContextFallsBackToRequestID proves a context carrying only a
// request ID still reports that ID as its correlation ID, and that an untraced
// or nil context reports empty identifiers instead of panicking.
func TestTraceIDsFromContextFallsBackToRequestID(t *testing.T) {
	ctx := ContextWithRequestID(context.Background(), "request-only")
	if requestID, correlationID := TraceIDsFromContext(ctx); requestID != "request-only" || correlationID != "request-only" {
		t.Fatalf("unexpected fallback: %q %q", requestID, correlationID)
	}
	if requestID, correlationID := TraceIDsFromContext(context.Background()); requestID != "" || correlationID != "" {
		t.Fatalf("untraced context reported identifiers: %q %q", requestID, correlationID)
	}
	//lint:ignore SA1012 a nil context is the documented tolerated input
	if got := RequestIDFromContext(nil); got != "" {
		t.Fatalf("nil context reported %q", got)
	}
}
