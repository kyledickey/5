package apierror

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/quack/idutil"
)

// TestWriteAbortsWithStableEnvelopeAndTraceIDs proves Write stops the handler
// chain and emits the documented envelope carrying the request's trace IDs.
func TestWriteAbortsWithStableEnvelopeAndTraceIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	reached := false
	router.GET("/denied", func(c *gin.Context) {
		c.Request = c.Request.WithContext(idutil.ContextWithTrace(c.Request.Context(), "request-1", "correlation-1"))
		Write(c, http.StatusForbidden, CodeAuthorization, "access denied")
	}, func(*gin.Context) { reached = true })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/denied", nil))
	if response.Code != http.StatusForbidden || reached {
		t.Fatalf("Write did not abort with 403: status=%d reached=%v", response.Code, reached)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("unexpected content type %q", got)
	}
	want := `{"error":{"code":"authorization_denied","message":"access denied","request_id":"request-1","correlation_id":"correlation-1"}}`
	if response.Body.String() != want {
		t.Fatalf("unexpected envelope:\n got %s\nwant %s", response.Body.String(), want)
	}
	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.Error.Code != CodeAuthorization {
		t.Fatalf("envelope does not round-trip: %+v err=%v", decoded, err)
	}
}

// TestWriteWithoutTraceLeavesIdentifiersEmpty proves an untraced request still
// produces the envelope rather than failing on missing context values.
func TestWriteWithoutTraceLeavesIdentifiersEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	Write(c, http.StatusBadRequest, CodeValidation, "bad input")
	var decoded Response
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v body=%s", err, response.Body.String())
	}
	if decoded.Error.RequestID != "" || decoded.Error.CorrelationID != "" || decoded.Error.Message != "bad input" {
		t.Fatalf("unexpected detail: %+v", decoded.Error)
	}
}

// TestDefaultMapsEveryStatusToAStableCode proves each documented status maps to
// its code and that unknown statuses fall back to an internal error.
func TestDefaultMapsEveryStatusToAStableCode(t *testing.T) {
	tests := []struct {
		status int
		want   Code
	}{
		{http.StatusBadRequest, CodeValidation},
		{http.StatusUnprocessableEntity, CodeValidation},
		{http.StatusUnauthorized, CodeAuthentication},
		{http.StatusForbidden, CodeAuthorization},
		{http.StatusNotFound, CodeNotFound},
		{http.StatusConflict, CodeConflict},
		{http.StatusRequestEntityTooLarge, CodeBodyTooLarge},
		{http.StatusTooManyRequests, CodeRateLimited},
		{http.StatusBadGateway, CodeDependency},
		{http.StatusServiceUnavailable, CodeDependency},
		{http.StatusGatewayTimeout, CodeDependency},
		{http.StatusInternalServerError, CodeInternal},
		{http.StatusTeapot, CodeInternal},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			code, message := Default(test.status)
			if code != test.want || message == "" {
				t.Fatalf("Default(%d) = %q %q, want %q", test.status, code, message, test.want)
			}
		})
	}
}
