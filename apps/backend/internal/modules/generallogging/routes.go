package generallogging

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

type ActorResolver func(*gin.Context) (Actor, error)

func RegisterRoutes(group *gin.RouterGroup, service *Service, resolve ActorResolver) {
	module := group.Group("/general-logging")
	module.GET("/settings", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		settings, enabled, status, err := service.Settings(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": enabled, "settings": settings, "status": status})
	})
	module.GET("/status", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		_, enabled, status, err := service.Settings(c, actor)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": enabled, "status": status})
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
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid general logging settings"})
			return
		}
		settings, err := service.UpdateSettings(c, actor, input.Enabled, input.Settings)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": input.Enabled, "settings": settings})
	})
	module.POST("/repair-channel/:channelID", func(c *gin.Context) {
		actor, ok := resolveActor(c, resolve)
		if !ok {
			return
		}
		settings, enabled, err := service.RepairDeletedChannel(c, actor, c.Param("channelID"))
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": enabled, "settings": settings})
	})
}
func resolveActor(c *gin.Context, resolve ActorResolver) (Actor, bool) {
	if resolve == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "general logging routes are not configured"})
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
	status := http.StatusBadRequest
	if errors.Is(err, ErrPermissionDenied) {
		status = http.StatusForbidden
	}
	if errors.Is(err, ErrDisabled) {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{"error": err.Error()})
}
