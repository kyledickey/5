package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/quack/idutil"
)

const (
	RequestIDHeader     = "X-Request-ID"
	CorrelationIDHeader = "X-Correlation-ID"
	ContextRequestIDKey = "request_id"
)

// RequestContext attaches request and correlation IDs to the request context
// and echoes them as response headers. Caller-supplied header values are kept
// only when idutil.NormalizeTraceID accepts them; otherwise fresh IDs are
// generated so downstream logs and error envelopes always carry a trace.
// It must be installed before any middleware that logs or writes errors.
func RequestContext(c *gin.Context) {
	ctx := idutil.ContextWithTrace(c.Request.Context(), c.GetHeader(RequestIDHeader), c.GetHeader(CorrelationIDHeader))
	c.Request = c.Request.WithContext(ctx)

	requestID, correlationID := idutil.TraceIDsFromContext(ctx)
	c.Set(ContextRequestIDKey, requestID)
	c.Header(RequestIDHeader, requestID)
	c.Header(CorrelationIDHeader, correlationID)

	c.Next()
}

// traceAttrs returns slog key/value pairs identifying the request, followed by
// extra, so log lines about credentials can be correlated without ever
// including the credential itself.
func traceAttrs(c *gin.Context, extra ...any) []any {
	requestID, correlationID := idutil.TraceIDsFromContext(c.Request.Context())
	return append([]any{"request_id", requestID, "correlation_id", correlationID}, extra...)
}
