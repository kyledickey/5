package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
)

// Recovery converts a handler panic into a 500 error envelope. Only the panic
// value's type and the stack are logged; the value itself, the request, cookies,
// and body are not, because a panic message may quote private moderation
// content or credentials.
func Recovery(c *gin.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(c.Request.Context(), "HTTP handler panicked",
				"panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "The request could not be completed")
		}
	}()
	c.Next()
}
