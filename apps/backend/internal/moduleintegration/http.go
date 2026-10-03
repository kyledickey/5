package moduleintegration

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// RegisterHTTP mounts every module's routes under
// /:discordGuildID/modules behind the shared live guild context, the
// authenticated-member rate limit, write idempotency and the error envelope.
// group and services are required; the Runtime's module services are assumed
// present.
func (r *Runtime) RegisterHTTP(group *gin.RouterGroup, services *quack.Services, primitives httpplatform.Primitives) error {
	if group == nil || services == nil {
		return errors.New("optional module HTTP dependencies are not configured")
	}

	modulesGroup := group.Group("/:discordGuildID/modules")
	modulesGroup.Use(middleware.RequireGuildContext(services, ""))
	modulesGroup.Use(moduleRateLimit(primitives, services.Config))
	modulesGroup.Use(moduleIdempotency(primitives, services.Config, r.Tickets))
	// Normalize feature errors before the idempotency layer persists a response;
	// the global envelope remains the final process-wide safety boundary.
	modulesGroup.Use(middleware.ErrorEnvelope)
	var closer tickets.Closer
	if r.TicketDiscord != nil {
		closer = r.TicketDiscord
	}
	tickets.RegisterRoutes(modulesGroup, r.Tickets, resolveTicketActor, closer)
	generallogging.RegisterRoutes(modulesGroup, r.Logging, resolveLoggingActor)
	honeypot.RegisterRoutes(modulesGroup, r.Honeypot, resolveHoneypotActor)
	return nil
}

// resolveHoneypotActor maps the live guild context into honeypot authority; only
// Manage Guild (guild settings write) grants CanManage.
func resolveHoneypotActor(c *gin.Context) (honeypot.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return honeypot.Actor{}, errors.New("live guild context is unavailable")
	}
	return honeypot.Actor{
		GuildID:       guildContext.Guild.ID,
		DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage:     guildContext.Can(model.PermissionActionGuildSettingsWrite),
	}, nil
}

func moduleRateLimit(primitives httpplatform.Primitives, cfg config.Config) gin.HandlerFunc {
	limit := httpplatform.RateLimit{
		Maximum: cfg.RateLimits.MemberRead.Maximum,
		Window:  time.Duration(cfg.RateLimits.MemberRead.WindowSeconds) * time.Second,
	}
	return primitives.RateLimits.Limit("optional-modules", limit, moduleSubject)
}

// moduleIdempotency authorizes mutating methods and then requires a fenced
// idempotency key for them; safe reads pass through untouched. Ticket closure
// paths are authorized as owner-or-moderator through ticketService, which may
// be nil when tickets are not mounted (closure then falls back to the settings
// write permission).
func moduleIdempotency(primitives httpplatform.Primitives, cfg config.Config, ticketService *tickets.Service) gin.HandlerFunc {
	ttl := time.Duration(cfg.RateLimits.IdempotencyTTLHours) * time.Hour
	protect := primitives.Idempotency.Protect("optional-module-write", ttl, moduleWriteSubject)
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			guild := middleware.GetGuildContext(c)
			action := model.PermissionActionGuildSettingsWrite
			path := c.FullPath()
			if isTicketClosePath(path) {
				action = model.PermissionActionTicketResolve
			}
			allowed := guild != nil && guild.Can(action)
			if isTicketClosePath(path) && ticketService != nil {
				actor, err := resolveTicketActor(c)
				if err == nil {
					ticket, _, err := ticketService.Detail(c.Request.Context(), actor, c.Param("ticketID"))
					allowed = err == nil && ticket != nil &&
						(actor.CanModerate || ticket.OwnerDiscordUserID == actor.DiscordUserID)
				}
			}
			if !allowed {
				apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "access denied")
				return
			}
			protect(c)
		default:
			c.Next()
		}
	}
}

// moduleWriteSubject scopes an idempotency key to actor, guild, method and
// path so one key cannot replay a response across distinct operations.
func moduleWriteSubject(c *gin.Context) string {
	return moduleSubject(c) + ":" + c.Request.Method + ":" + c.Request.URL.EscapedPath()
}

// moduleSubject is the rate-limit and idempotency identity: internal guild plus
// actor, or "unknown" before guild context is established.
func moduleSubject(c *gin.Context) string {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return "unknown"
	}
	return guildContext.Guild.ID + ":" + guildContext.ActorDiscordUserID
}

func resolveTicketActor(c *gin.Context) (tickets.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return tickets.Actor{}, errors.New("live guild context is unavailable")
	}
	return tickets.Actor{
		GuildID:       guildContext.Guild.ID,
		DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage:     guildContext.Can(model.PermissionActionGuildSettingsWrite),
		CanModerate:   guildContext.Can(model.PermissionActionTicketResolve),
	}, nil
}

func resolveLoggingActor(c *gin.Context) (generallogging.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return generallogging.Actor{}, errors.New("live guild context is unavailable")
	}
	return generallogging.Actor{
		GuildID:       guildContext.Guild.ID,
		DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage:     guildContext.Can(model.PermissionActionGuildSettingsWrite),
	}, nil
}

// isTicketClosePath matches the close route and its resolve/cancel aliases so
// all three share the owner-or-moderator authorization before idempotent replay.
func isTicketClosePath(path string) bool {
	for _, action := range []string{"close", "resolve", "cancel"} {
		if strings.HasSuffix(path, "/tickets/:ticketID/"+action) {
			return true
		}
	}
	return false
}
