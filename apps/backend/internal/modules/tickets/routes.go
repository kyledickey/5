package tickets

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type ActorResolver func(*gin.Context) (Actor, error)

// Closer captures and publishes the real thread transcript before deleting it.
// HTTP callers cannot supply a substitute transcript or bypass Discord cleanup.
type Closer interface {
	Close(context.Context, Actor, string) (*Ticket, error)
}

func RegisterRoutes(group *gin.RouterGroup, service *Service, resolve ActorResolver, closer Closer) {
	module := group.Group("/tickets")
	module.GET("/settings", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		settings, enabled, err := service.Settings(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": enabled, "settings": settings})
	})
	module.GET("/status", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		status, err := service.Status(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": status})
	})
	module.PUT("/settings", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		var input struct {
			Enabled  bool     `json:"enabled"`
			Settings Settings `json:"settings"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid ticket settings"})
			return
		}
		settings, err := service.UpdateSettings(c, actor, input.Enabled, input.Settings)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": input.Enabled, "settings": settings})
	})
	module.GET("/queue", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit"))
		items, err := service.Queue(c, actor, Status(c.Query("status")), limit)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"tickets": items})
	})
	module.GET("/:ticketID", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		ticket, events, err := service.Detail(c, actor, c.Param("ticketID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ticket": ticket, "events": events})
	})
	module.GET("/:ticketID/transcript", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		transcript, err := service.Transcript(c, actor, c.Param("ticketID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"transcript": transcript})
	})
	closeTicket := func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		if closer == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ticket closure is unavailable"})
			return
		}
		ticket, err := closer.Close(c.Request.Context(), actor, c.Param("ticketID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ticket": ticket})
	}
	module.POST("/:ticketID/close", closeTicket)
	// Existing clients share the same close operation; neither alias bypasses
	// transcript capture, queue publication or thread cleanup.
	module.POST("/:ticketID/resolve", closeTicket)
	module.POST("/:ticketID/cancel", closeTicket)
}

func resolveActor(c *gin.Context, resolve ActorResolver) (Actor, bool) {
	if resolve == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ticket routes are not configured"})
		return Actor{}, false
	}
	actor, err := resolve(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return Actor{}, false
	}
	return actor, true
}
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, ErrDuplicateOpen), errors.Is(err, ErrInvalidTransition):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrRateLimited):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
	case errors.Is(err, ErrDisabled):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
