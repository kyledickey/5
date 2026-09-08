package tickets

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// ActorResolver resolves authenticated request context into current ticket authority.
type ActorResolver func(*gin.Context) (Actor, error)

// RegisterRoutes exposes ticket settings, status, queue, detail, transcript, and lifecycle APIs.
func RegisterRoutes(group *gin.RouterGroup, service *Service, resolve ActorResolver) {
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
	module.POST("/:ticketID/resolve", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		var input struct {
			Transcript string `json:"transcript"`
		}
		if err := c.ShouldBindJSON(&input); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid resolution payload"})
			return
		}
		ticket, err := service.Resolve(c, actor, c.Param("ticketID"), input.Transcript)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ticket": ticket})
	})
	module.POST("/:ticketID/cancel", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		ticket, err := service.Cancel(c, actor, c.Param("ticketID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"ticket": ticket})
	})
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
