package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// SetupRoutes wires the router without optional modules and panics on a
// composition error, for tests and callers that have no module runtime.
func SetupRoutes(r *gin.Engine, services *quack.Services, providers ...DiscordStatusProvider) {
	if err := SetupRoutesWithModules(r, services, nil, providers...); err != nil {
		panic(err)
	}
}

// SetupRoutesWithModules installs core and optional-module registrars and
// returns composition errors to process startup.
func SetupRoutesWithModules(r *gin.Engine, services *quack.Services, moduleRuntime *moduleintegration.Runtime, providers ...DiscordStatusProvider) error {
	var discord DiscordStatusProvider
	if len(providers) > 0 {
		discord = providers[0]
	}
	redisProvider, _ := services.Store.(httpplatform.RedisProvider)
	primitives := httpplatform.New(redisProvider)

	r.GET("/status", func(c *gin.Context) { status(c, services, discord) })
	r.GET("/livez", liveness)
	r.GET("/readyz", func(c *gin.Context) { readiness(c, services, discord) })
	r.GET("/metrics", func(c *gin.Context) { metrics(c, services) })
	r.GET("/ops/status", func(c *gin.Context) { globalOpsStatus(c, services) })
	r.GET("/guilds/:discordGuildID/ops/status", func(c *gin.Context) { guildOpsStatus(c, services) })
	setupAuthRoutes(r, services, primitives)
	if err := setupGuildRoutes(r, services, moduleRuntime, primitives); err != nil {
		return err
	}
	setupMemberRoutes(r, services, primitives)
	return nil
}

func setupGuildRoutes(r *gin.Engine, services *quack.Services, moduleRuntime *moduleintegration.Runtime, primitives httpplatform.Primitives) error {
	guilds := r.Group("/guilds")
	guilds.Use(middleware.RequireAuth(services.Store, services.Config.Auth))

	guilds.POST("/:discordGuildID/templates/:templateID/restore", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateWrite), func(c *gin.Context) { restoreTemplate(c, services) })
	guilds.GET("/:discordGuildID/templates/:templateID/export", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateWrite), func(c *gin.Context) { exportTemplate(c, services) })
	guilds.POST("/:discordGuildID/templates/import", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateWrite), func(c *gin.Context) { importTemplate(c, services) })
	guilds.POST("/:discordGuildID/cases/:caseRef/void", middleware.RequireGuildContext(services, model.PermissionActionCaseVoid), func(c *gin.Context) { voidCase(c, services) })
	guilds.GET("/:discordGuildID/action-failures", middleware.RequireGuildContext(services, model.PermissionActionCaseRead), func(c *gin.Context) { listFailedActions(c, services) })
	guilds.POST("/:discordGuildID/action-failures/:executionID/retry", middleware.RequireGuildContext(services, model.PermissionActionCaseCreate), func(c *gin.Context) { retryFailedAction(c, services) })
	guilds.POST("/:discordGuildID/action-failures/:executionID/dismiss", middleware.RequireGuildContext(services, model.PermissionActionFailureDismiss), func(c *gin.Context) { dismissFailedAction(c, services) })
	guilds.POST("/:discordGuildID/cases/:caseRef/reversals", middleware.RequireGuildContext(services, model.PermissionActionCaseCreate), func(c *gin.Context) { reverseCaseAction(c, services) })
	guilds.GET("/:discordGuildID/statistics", middleware.RequireGuildContext(services, model.PermissionActionAuditRead), func(c *gin.Context) {
		getStatistics(c, services.Statistics)
	})

	registerAppealStaffRoutes(guilds, services, services.Appeals, primitives)
	if moduleRuntime != nil {
		if err := moduleRuntime.RegisterHTTP(guilds, services, primitives); err != nil {
			return err
		}
	}

	guilds.GET("", func(c *gin.Context) { listUserGuilds(c, services) })
	guilds.GET("/:discordGuildID/me", middleware.RequireGuildContext(services, ""), guildMe)
	guilds.GET("/:discordGuildID/settings", middleware.RequireGuildContext(services, model.PermissionActionGuildSettingsRead), func(c *gin.Context) {
		getGuildSettings(c, services)
	})
	guilds.PATCH("/:discordGuildID/settings", middleware.RequireGuildContext(services, model.PermissionActionGuildSettingsWrite), func(c *gin.Context) {
		updateGuildSettings(c, services)
	})
	guilds.POST("/:discordGuildID/settings/starter-policy-notice/acknowledge", middleware.RequireGuildContext(services, model.PermissionActionGuildSettingsWrite), func(c *gin.Context) {
		acknowledgeStarterPolicyNotice(c, services)
	})
	guilds.GET("/:discordGuildID/templates", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateRead), func(c *gin.Context) {
		listTemplates(c, services)
	})
	guilds.POST("/:discordGuildID/templates", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateWrite), func(c *gin.Context) {
		createTemplate(c, services)
	})
	guilds.GET("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateRead), func(c *gin.Context) {
		getTemplate(c, services)
	})
	guilds.PATCH("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateWrite), func(c *gin.Context) {
		updateTemplate(c, services, moduleRuntime)
	})
	guilds.DELETE("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services, model.PermissionActionCaseTemplateDelete), func(c *gin.Context) {
		archiveTemplate(c, services, moduleRuntime)
	})
	guilds.GET("/:discordGuildID/cases", middleware.RequireGuildContext(services, model.PermissionActionCaseRead), func(c *gin.Context) {
		listCases(c, services)
	})
	guilds.POST("/:discordGuildID/cases", middleware.RequireGuildContext(services, model.PermissionActionCaseCreate), func(c *gin.Context) {
		createCase(c, services)
	})
	guilds.GET("/:discordGuildID/cases/:caseRef", middleware.RequireGuildContext(services, model.PermissionActionCaseRead), func(c *gin.Context) {
		getCase(c, services)
	})
	guilds.GET("/:discordGuildID/users/:targetDiscordUserID/cases", middleware.RequireGuildContext(services, model.PermissionActionCaseRead), func(c *gin.Context) {
		listUserCases(c, services)
	})
	guilds.GET("/:discordGuildID/audit-log", middleware.RequireGuildContext(services, model.PermissionActionAuditRead), func(c *gin.Context) {
		listAuditLog(c, services)
	})
	return nil
}

// setupMemberRoutes mounts target-owned reads behind caller authentication
// without requiring the member to remain in the Discord guild.
func setupMemberRoutes(r *gin.Engine, services *quack.Services, primitives httpplatform.Primitives) {
	members := r.Group("/members/me")
	members.Use(middleware.RequireAuth(services.Store, services.Config.Auth))
	registerAppealMemberRoutes(members, services, services.Appeals, primitives)
}
