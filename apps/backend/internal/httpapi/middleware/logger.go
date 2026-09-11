package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

// Logger records one structured line per request after the handler chain
// completes: info for success, warn for 4xx, error for 5xx. It logs the
// matched route pattern rather than the raw path so user-supplied path
// segments and query parameters (including OAuth codes) never reach logs.
func Logger(c *gin.Context) {
	start := time.Now()
	c.Next()
	level := slog.LevelInfo
	if c.Writer.Status() >= 500 {
		level = slog.LevelError
	} else if c.Writer.Status() >= 400 {
		level = slog.LevelWarn
	}
	path := c.FullPath()
	if path == "" {
		path = "unmatched"
	}
	slog.Log(c.Request.Context(), level, "HTTP request completed",
		append(traceAttrs(c),
			"method", c.Request.Method, "route", path,
			"status", c.Writer.Status(), "duration", time.Since(start))...)
}
