package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/quack/idutil"
)

// BufferedWriter holds a handler's status and body in memory instead of
// sending them, so the middleware that installed it can rewrite or record the
// response before anything reaches the client. Nothing is flushed until the
// installer writes it through the original gin.ResponseWriter.
type BufferedWriter struct {
	gin.ResponseWriter
	status int
	body   bytes.Buffer
}

// WriteHeader records the first status a handler selects; later calls are
// ignored, matching net/http semantics.
func (w *BufferedWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

// WriteHeaderNow records an implicit 200 when a handler writes a body without
// choosing a status.
func (w *BufferedWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *BufferedWriter) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	return w.body.Write(body)
}

func (w *BufferedWriter) WriteString(body string) (int, error) {
	w.WriteHeaderNow()
	return w.body.WriteString(body)
}

// Status reports the recorded status, defaulting to 200 when none was chosen.
func (w *BufferedWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *BufferedWriter) Size() int { return w.body.Len() }

// Written reports whether the handler selected a status or wrote any body.
func (w *BufferedWriter) Written() bool { return w.status != 0 || w.body.Len() > 0 }

// Body returns the buffered response bytes.
func (w *BufferedWriter) Body() []byte { return w.body.Bytes() }

// ErrorEnvelope guarantees every 4xx/5xx response is an apierror.Response.
// A failure body that already decodes as the envelope is passed through; any
// other failure body (a raw string, a legacy {"error": ...} map, an adapter
// error) is discarded and replaced with apierror.Default for the status, so
// unsafe details never leak. Success bodies are forwarded unchanged apart from
// Content-Length, which is dropped because the body may have been rewritten.
func ErrorEnvelope(c *gin.Context) {
	original := c.Writer
	buffered := &BufferedWriter{ResponseWriter: original}
	c.Writer = buffered
	defer func() { c.Writer = original }()
	c.Next()

	status := buffered.Status()
	body := buffered.Body()
	if status >= http.StatusBadRequest {
		var structured apierror.Response
		if err := json.Unmarshal(body, &structured); err != nil || structured.Error.Code == "" {
			code, message := apierror.Default(status)
			requestID, correlationID := idutil.TraceIDsFromContext(c.Request.Context())
			structured = apierror.Response{Error: apierror.Detail{
				Code:          code,
				Message:       message,
				RequestID:     requestID,
				CorrelationID: correlationID,
			}}
		}
		body, _ = json.Marshal(structured) // cannot fail: plain struct of strings
		original.Header().Set("Content-Type", "application/json; charset=utf-8")
	}

	original.Header().Del("Content-Length")
	original.WriteHeader(status)
	if len(body) > 0 {
		_, _ = original.Write(body) // best-effort: the client has gone if this fails and there is no one to tell
	}
}
