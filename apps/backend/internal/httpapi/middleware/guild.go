package middleware

import (
	"errors"
	"net/http"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// ContextGuildKey is the Gin context key under which RequireGuildContext
// stores the resolved *quack.GuildStaffContext.
const ContextGuildKey = "guild_context"

// ContextAuthorizedWriteKey is the Gin context key under which the platform
// endpoint policy stages its idempotency handler for a write. The handler is
// deliberately not run by the policy itself: it must run only after the route's
// permission check succeeded, so a replayed response is never served to a
// caller who has since lost access.
const ContextAuthorizedWriteKey = "authorized_write_policy"

// ContinueAuthorizedWrite runs the idempotency handler staged under
// ContextAuthorizedWriteKey (clearing it so it runs at most once) and otherwise
// continues the chain. Routes that perform their own capability check instead
// of relying on RequireGuildContext must call this after that check passes.
func ContinueAuthorizedWrite(c *gin.Context) {
	if value, ok := c.Get(ContextAuthorizedWriteKey); ok {
		c.Set(ContextAuthorizedWriteKey, nil)
		if protect, ok := value.(gin.HandlerFunc); ok {
			protect(c)
			return
		}
	}
	c.Next()
}

// RequireGuildContext resolves the authenticated caller's live staff context
// for the :discordGuildID route parameter and, when requiredAction is not
// empty, verifies the caller currently holds that permission. On success the
// context is stored under ContextGuildKey for GetGuildContext and, for
// permission-checked routes, any staged idempotency handler is released via
// ContinueAuthorizedWrite.
//
// It must run after RequireAuth. It aborts with 401 when no session is
// present, 404 when the bot is not in the guild, 403 when live Discord
// authorization cannot be confirmed, and 403 when the permission is denied.
func RequireGuildContext(services *quack.Services, requiredAction model.PermissionAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := GetAuthSession(c)
		if session == nil {
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		discordGuildID := c.Param("discordGuildID")
		guildContext, err := services.Guilds.ResolveStaffContext(c.Request.Context(), session, discordGuildID)
		if err != nil {
			slog.Warn("live guild authorization denied",
				traceAttrs(c, "actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID)...)
			if errors.Is(err, quack.ErrBotNotInGuild) {
				apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, "guild not found")
			} else {
				apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "live guild authorization unavailable")
			}
			return
		}

		if err := services.Guilds.Authorize(c.Request.Context(), guildContext, requiredAction, model.AuditSourceAPI); err != nil {
			slog.Warn("guild permission denied",
				traceAttrs(c, "actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID,
					"permission_action", string(requiredAction))...)
			apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "access denied")
			return
		}

		c.Set(ContextGuildKey, guildContext)
		if requiredAction != "" {
			ContinueAuthorizedWrite(c)
			return
		}
		c.Next()
	}
}

// GetGuildContext returns the staff context stored by RequireGuildContext, or
// nil when the request did not pass through it.
func GetGuildContext(c *gin.Context) *quack.GuildStaffContext {
	v, ok := c.Get(ContextGuildKey)
	if !ok {
		return nil
	}

	guildContext, ok := v.(*quack.GuildStaffContext)
	if !ok {
		return nil
	}

	return guildContext
}
