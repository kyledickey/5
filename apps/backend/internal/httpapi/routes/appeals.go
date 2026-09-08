package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterAppealAndMemberRoutes mounts target-owned case and appeal routes on an authenticated member group.
func RegisterAppealAndMemberRoutes(group *gin.RouterGroup, services *quack.Services, appeals *quack.AppealService, primitives httpplatform.Primitives) error {
	if group == nil || services == nil || appeals == nil || primitives.RateLimits == nil || primitives.Idempotency == nil {
		return errors.New("appeal member route dependencies are not configured")
	}
	member := group.Group("")
	member.Use(primitives.RateLimits.Limit("member-reads-and-appeals", memberReadLimit(services), memberSubject))
	member.GET("/guilds/:guildID/cases", func(c *gin.Context) { listMemberOwnedCases(c, services) })
	member.GET("/cases/:caseID", func(c *gin.Context) { getMemberOwnedCase(c, services) })
	member.POST("/cases/:caseID/appeal", memberWriteIdempotency(primitives, services, "appeal-submit"), func(c *gin.Context) { submitAppeal(c, appeals) })
	member.GET("/appeals/:appealID", func(c *gin.Context) { getMemberAppeal(c, appeals) })
	return nil
}

// RegisterAppealStaffRoutes mounts appeal settings, queue, decisions, and explicit reversal routes on an authenticated guild group.
func RegisterAppealStaffRoutes(group *gin.RouterGroup, services *quack.Services, appeals *quack.AppealService, primitives httpplatform.Primitives) error {
	if group == nil || services == nil || appeals == nil || primitives.RateLimits == nil || primitives.Idempotency == nil {
		return errors.New("appeal staff route dependencies are not configured")
	}
	staff := group.Group("/:discordGuildID")
	staff.Use(middleware.RequireGuildContext(services, ""))
	staff.Use(primitives.RateLimits.Limit("appeal-staff", memberReadLimit(services), staffAppealSubject))
	staff.GET("/appeal-settings", func(c *gin.Context) { getAppealSettings(c, appeals) })
	staff.GET("/appeals", func(c *gin.Context) { listStaffAppeals(c, appeals) })
	staff.GET("/appeals/:appealID", func(c *gin.Context) { getStaffAppeal(c, appeals) })
	staff.POST("/appeals/:appealID/accept", staffWriteIdempotency(primitives, services, "appeal-accept"), func(c *gin.Context) { acceptAppeal(c, appeals) })
	staff.POST("/appeals/:appealID/reject", staffWriteIdempotency(primitives, services, "appeal-reject"), func(c *gin.Context) { rejectAppeal(c, appeals) })
	staff.POST("/appeals/:appealID/close", staffWriteIdempotency(primitives, services, "appeal-close"), func(c *gin.Context) { closeAppeal(c, appeals) })
	staff.POST("/appeals/:appealID/reversals", staffWriteIdempotency(primitives, services, "appeal-reversal"), func(c *gin.Context) { reverseAcceptedAppeal(c, services, appeals) })
	return nil
}

// getAppealSettings returns the effective appeal form for a guild.
// @Summary Get guild appeal settings
// @Tags Appeals
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} quack.AppealSettingsResponse
// @Failure 403 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeal-settings [get]
func getAppealSettings(c *gin.Context, appeals *quack.AppealService) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil || !guildContext.Can(model.PermissionActionGuildSettingsRead) {
		writeAppealError(c, quack.ErrAppealPermissionDenied)
		return
	}
	result, err := appeals.GetSettings(c.Request.Context(), guildContext.Guild.ID)
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// listStaffAppeals returns the guild-scoped appeal review queue.
// @Summary List guild appeals
// @Tags Appeals
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param status query string false "Appeal status"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Security CookieAuth
// @Success 200 {object} quack.AppealListResponse
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals [get]
func listStaffAppeals(c *gin.Context, appeals *quack.AppealService) {
	limit, offset, err := parsePageInts(c.Query("limit"), c.Query("offset"))
	if err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid pagination")
		return
	}
	result, err := appeals.ListStaff(c.Request.Context(), middleware.GetGuildContext(c), model.AppealStatus(strings.TrimSpace(c.Query("status"))), limit, offset)
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// getStaffAppeal returns one appeal to authorized guild staff.
// @Summary Get a guild appeal
// @Tags Appeals
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID} [get]
func getStaffAppeal(c *gin.Context, appeals *quack.AppealService) {
	result, err := appeals.GetStaff(c.Request.Context(), middleware.GetGuildContext(c), c.Param("appealID"))
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"appeal": result})
}
