package honeypot

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ActorResolver func(*gin.Context) (Actor, error)

func RegisterRoutes(group *gin.RouterGroup, service *Service, resolve ActorResolver) {
	module := group.Group("/honeypot")
	module.GET("/settings", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		settings, status, err := service.Settings(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"settings": settings, "status": status})
	})
	module.GET("/status", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		_, status, err := service.Settings(c, actor)
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid honeypot settings"})
			return
		}
		settings, status, err := service.UpdateSettings(c, actor, input.Enabled, input.Settings)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"settings": settings, "status": status})
	})
	module.POST("/repair", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		settings, status, err := service.Repair(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"settings": settings, "status": status})
	})
}

func resolveActor(c *gin.Context, resolve ActorResolver) (Actor, bool) {
	if resolve == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "honeypot routes are not configured"})
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
	case errors.Is(err, ErrDisabled), errors.Is(err, ErrChannelUnavailable), errors.Is(err, ErrTemplateUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}
