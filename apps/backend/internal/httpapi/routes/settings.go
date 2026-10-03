package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// getGuildSettings returns the guild-owned setup state to current Manage Guild authorities.
// @Summary Get guild settings
// @Tags Guild settings
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings [get]
func getGuildSettings(c *gin.Context, services *quack.Services) {
	settings, err := services.Settings.Get(c.Request.Context(), middleware.GetGuildContext(c))
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// updateGuildSettings applies a partial authorized guild settings write.
// @Summary Update guild settings
// @Tags Guild settings
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param settings body quack.GuildSettingsInput true "Partial guild settings"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings [patch]
func updateGuildSettings(c *gin.Context, services *quack.Services) {
	var input quack.GuildSettingsInput
	if err := decodeStrictJSON(c, &input); err != nil {
		writeGuildSettingsError(c, services.Settings.RejectUpdatePayload(c.Request.Context(), middleware.GetGuildContext(c), err))
		return
	}
	settings, err := services.Settings.Update(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// acknowledgeStarterPolicyNotice explicitly marks the one-time dashboard setup notice complete.
// @Summary Acknowledge the starter policy notice
// @Tags Guild settings
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge [post]
func acknowledgeStarterPolicyNotice(c *gin.Context, services *quack.Services) {
	settings, err := services.Settings.AcknowledgeStarterPolicyNotice(c.Request.Context(), middleware.GetGuildContext(c))
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

func writeGuildSettingsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrGuildSettingsValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsNotFound):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, err.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "guild settings operation failed")
	}
}
