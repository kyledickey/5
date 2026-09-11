package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

const (
	ContextSessionKey = "auth_session"
	ContextUserIDKey  = "auth_user_id"
)

// sessionLookupTimeout bounds the session store round trips made per request.
const sessionLookupTimeout = 5 * time.Second

// RequireAuth resolves the caller's session from a Bearer token or the session
// cookie and stores it on the Gin context under ContextSessionKey (with the
// Discord user ID under ContextUserIDKey) for handlers to read via
// GetAuthSession. Every successful request slides the session expiry forward
// by auth.SessionTTLHours and refreshes the double-submit CSRF cookie when the
// browser authenticated with the session cookie.
//
// It aborts with 401 authentication_required when no usable session exists,
// 401 reauthentication_required (and deletes the stored session and expires
// both cookies) when the session or its Discord token has expired, and 503
// when the session store is unavailable.
func RequireAuth(s quack.Repository, auth config.AuthConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := ExtractSessionID(c, auth.SessionCookieName)
		if sessionID == "" {
			slog.Warn("authentication required", traceAttrs(c)...)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), sessionLookupTimeout)
		defer cancel()

		session, err := s.GetSession(ctx, sessionID)
		if err != nil {
			slog.Error("auth session dependency unavailable", traceAttrs(c)...)
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
			return
		}
		if session == nil || session.DiscordUserID == "" {
			slog.Warn("invalid authentication session", traceAttrs(c)...)
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		now := time.Now().UTC()
		if logMessage, message := sessionExpiry(session, now); message != "" {
			slog.Warn(logMessage, traceAttrs(c, "actor_discord_user_id", session.DiscordUserID)...)
			_ = s.DeleteSession(ctx, sessionID) // best-effort: the caller is told to sign in again regardless
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, message)
			return
		}
		if session.CSRFToken == "" {
			csrfToken, err := NewCSRFToken()
			if err != nil {
				apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "could not refresh authentication session")
				return
			}
			session.CSRFToken = csrfToken
		}

		session.LastSeenAt = now
		ttl := time.Duration(auth.SessionTTLHours) * time.Hour
		session.SessionExpiresAt = now.Add(ttl)
		refreshed, err := s.RefreshSession(ctx, session, ttl)
		if err != nil {
			slog.Error("auth session refresh dependency unavailable", traceAttrs(c)...)
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
			return
		}
		if !refreshed {
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "sign in again to continue")
			return
		}
		if _, err := c.Cookie(auth.SessionCookieName); err == nil {
			setCSRFCookie(c, auth, session.CSRFToken, int(ttl.Seconds()))
		}

		c.Set(ContextSessionKey, session)
		c.Set(ContextUserIDKey, session.DiscordUserID)
		c.Next()
	}
}

// sessionExpiry returns the log line and client message to use when session
// can no longer be used at now: the session itself has expired, or the Discord
// token it was minted from has. Both return values are "" for a live session.
// Zero expiry timestamps never expire.
func sessionExpiry(session *model.AuthSession, now time.Time) (logMessage, message string) {
	if !session.SessionExpiresAt.IsZero() && !now.Before(session.SessionExpiresAt) {
		return "authentication session expired", "sign in again to continue"
	}
	if !session.TokenExpiresAt.IsZero() && !now.Before(session.TokenExpiresAt) {
		return "Discord authorization expired", "Discord authorization expired; sign in again"
	}
	return "", ""
}

// NewCSRFToken returns a 64-hex-character random token for the double-submit
// cookie. It is a challenge the browser must echo, not a secret in itself.
func NewCSRFToken() (string, error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(body[:]), nil
}

// setCSRFCookie writes the host-only, JavaScript-readable CSRF cookie so the
// dashboard can copy it into the X-CSRF-Token header.
func setCSRFCookie(c *gin.Context, auth config.AuthConfig, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CSRFCookieName, token, maxAge, "/", "", auth.CookieSecure, false)
}

// expireAuthCookies tells the browser to drop both the session and CSRF
// cookies without echoing their values.
func expireAuthCookies(c *gin.Context, auth config.AuthConfig) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.SessionCookieName, "", -1, "/", "", auth.CookieSecure, true)
	c.SetCookie(auth.CSRFCookieName, "", -1, "/", "", auth.CookieSecure, false)
}

// GetAuthSession returns the session stored by RequireAuth, or nil when the
// request did not pass through RequireAuth.
func GetAuthSession(c *gin.Context) *model.AuthSession {
	v, ok := c.Get(ContextSessionKey)
	if !ok {
		return nil
	}

	session, ok := v.(*model.AuthSession)
	if !ok {
		return nil
	}

	return session
}

// ExtractSessionID returns the session identifier presented by the request: a
// "Bearer" Authorization header takes precedence over the cookie named
// cookieName. It returns "" when neither is present, so API clients and the
// browser dashboard share one authentication middleware.
func ExtractSessionID(c *gin.Context, cookieName string) string {
	auth := c.GetHeader("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	cookie, err := c.Cookie(cookieName)
	if err == nil {
		return strings.TrimSpace(cookie)
	}

	return ""
}
