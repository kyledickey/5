package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// @Summary List guild audit entries
// @Tags Audit
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Param action query string false "Audit action"
// @Param resource_type query string false "Resource type"
// @Param result query string false "Audit result"
// @Security CookieAuth
// @Success 200 {object} quack.AuditListResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/audit-log [get]
func listAuditLog(c *gin.Context, services *quack.Services) {
	result, err := services.Audits.List(c.Request.Context(), middleware.GetGuildContext(c), quack.AuditListInput{
		Limit:               c.Query("limit"),
		Offset:              c.Query("offset"),
		ActorDiscordUserID:  c.Query("actor_discord_user_id"),
		Source:              c.Query("source"),
		Action:              c.Query("action"),
		ResourceType:        c.Query("resource_type"),
		ResourceID:          c.Query("resource_id"),
		Result:              c.Query("result"),
		CaseID:              c.Query("case_id"),
		MemberDiscordUserID: c.Query("member_discord_user_id"),
		CreatedAfter:        c.Query("created_after"),
		CreatedBefore:       c.Query("created_before"),
		ReadSource:          model.AuditSourceAPI,
		BeforeID:            c.Query("before_id"),
	})
	if err != nil {
		writeAuditError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

func writeAuditError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrAuditValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrAuditPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, err.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "audit operation failed")
	}
}

// getStatistics returns a bounded guild-scoped moderation snapshot.
// @Summary Get guild moderation statistics
// @Tags Audit
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param from query string false "Inclusive RFC3339 start"
// @Param to query string false "Exclusive RFC3339 end"
// @Security CookieAuth
// @Success 200 {object} model.StaffStatistics
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/statistics [get]
func getStatistics(c *gin.Context, statistics *quack.StaffStatisticsService) {
	result, err := statistics.Get(c.Request.Context(), middleware.GetGuildContext(c), quack.StatisticsInput{From: c.Query("from"), To: c.Query("to")})
	if err != nil {
		writeStatisticsError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeStatisticsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrStatisticsValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrStatisticsPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "statistics access denied")
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "statistics operation failed")
	}
}
