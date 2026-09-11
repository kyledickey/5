package apierror

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/quack/idutil"
)

// Code is a stable, machine-readable classification of an HTTP failure. Clients
// branch on Code; Message is free-form and may change wording.
type Code string

const (
	CodeValidation     Code = "validation_failed"
	CodeAuthentication Code = "authentication_required"
	CodeReauthenticate Code = "reauthentication_required"
	CodeAuthorization  Code = "authorization_denied"
	CodeNotFound       Code = "not_found"
	CodeConflict       Code = "conflict"
	CodeRateLimited    Code = "rate_limited"
	CodeCSRF           Code = "csrf_rejected"
	CodeOrigin         Code = "origin_rejected"
	CodeBodyTooLarge   Code = "body_too_large"
	CodeDependency     Code = "dependency_unavailable"
	CodeInternal       Code = "internal_error"
)

// Response is the top-level error envelope returned by every HTTP failure.
type Response struct {
	Error Detail `json:"error"`
}

// Detail carries the client-safe failure classification and the trace
// identifiers a client can quote when reporting a problem.
type Detail struct {
	Code          Code   `json:"code"`
	Message       string `json:"message"`
	RequestID     string `json:"request_id"`
	CorrelationID string `json:"correlation_id"`
}

// Write aborts the Gin request with status and a Response built from code,
// message, and the trace identifiers on the request context. message must
// already be safe to return to clients; Write does not redact it.
func Write(c *gin.Context, status int, code Code, message string) {
	requestID, correlationID := idutil.TraceIDsFromContext(c.Request.Context())
	c.AbortWithStatusJSON(status, Response{Error: Detail{
		Code:          code,
		Message:       message,
		RequestID:     requestID,
		CorrelationID: correlationID,
	}})
}

// Default returns the code and generic message used when a handler produced a
// failure status without a structured body. Unknown statuses map to
// CodeInternal so no unclassified failure ever reaches a client.
func Default(status int) (Code, string) {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return CodeValidation, "request validation failed"
	case http.StatusUnauthorized:
		return CodeAuthentication, "authentication required"
	case http.StatusForbidden:
		return CodeAuthorization, "access denied"
	case http.StatusNotFound:
		return CodeNotFound, "resource not found"
	case http.StatusConflict:
		return CodeConflict, "request conflicts with current state"
	case http.StatusRequestEntityTooLarge:
		return CodeBodyTooLarge, "request body is too large"
	case http.StatusTooManyRequests:
		return CodeRateLimited, "rate limit exceeded"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return CodeDependency, "dependency unavailable"
	default:
		return CodeInternal, "request failed"
	}
}
