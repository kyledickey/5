package middleware

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
)

const csrfHeader = "X-CSRF-Token"

// ValidateSecurityConfig rejects HTTP settings that would leave the API
// unbounded or the browser session boundary weak: non-positive body, timeout,
// rate-limit, or TTL settings; blank or identical cookie names; malformed or
// wildcard CORS origins; malformed trusted proxies; and, outside the "dev"
// environment, missing CORS origins or insecure cookies. It is run once at
// startup and again by NewPlatformRegistrar so a misconfigured process never
// starts serving.
func ValidateSecurityConfig(cfg config.Config) error {
	if cfg.API.MaxBodyBytes <= 0 {
		return errors.New("API_MAX_BODY_BYTES must be positive")
	}
	if cfg.API.ReadHeaderTimeoutSeconds <= 0 || cfg.API.ReadTimeoutSeconds <= 0 ||
		cfg.API.WriteTimeoutSeconds <= 0 || cfg.API.IdleTimeoutSeconds <= 0 {
		return errors.New("all API timeout settings must be positive")
	}
	policies := []config.RateLimitPolicyConfig{
		cfg.RateLimits.OAuth, cfg.RateLimits.MemberRead, cfg.RateLimits.TemplateWrite,
		cfg.RateLimits.CaseCreate, cfg.RateLimits.Retry, cfg.RateLimits.Evidence,
	}
	for _, policy := range policies {
		if policy.Maximum <= 0 || policy.WindowSeconds <= 0 {
			return errors.New("all rate-limit maximum and window settings must be positive")
		}
	}
	if cfg.RateLimits.IdempotencyTTLHours <= 0 {
		return errors.New("HTTP_IDEMPOTENCY_TTL_HOURS must be positive")
	}
	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" || strings.TrimSpace(cfg.Auth.CSRFCookieName) == "" {
		return errors.New("authentication cookie names must be configured")
	}
	if cfg.Auth.SessionCookieName == cfg.Auth.CSRFCookieName {
		return errors.New("session and CSRF cookie names must differ")
	}
	if cfg.Auth.SessionTTLHours <= 0 || cfg.Auth.StateTTLMinutes <= 0 {
		return errors.New("authentication session and state TTL settings must be positive")
	}
	if cfg.Environment != "dev" {
		if len(cfg.API.CORSAllowedOrigins) == 0 {
			return errors.New("API_CORS_ALLOWED_ORIGINS is required outside development")
		}
		if !cfg.Auth.CookieSecure {
			return errors.New("AUTH_COOKIE_SECURE must be true outside development")
		}
	}
	for _, origin := range cfg.API.CORSAllowedOrigins {
		if !isExactOrigin(origin) {
			return fmt.Errorf("invalid CORS origin %q", origin)
		}
		if strings.Contains(origin, "*") {
			return errors.New("wildcard CORS origins are not supported")
		}
	}
	for _, proxy := range cfg.API.TrustedProxies {
		if net.ParseIP(proxy) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(proxy); err != nil {
			return fmt.Errorf("invalid trusted proxy %q", proxy)
		}
	}
	return nil
}

// isExactOrigin reports whether origin is a bare scheme://host[:port] with no
// path, query, fragment, or credentials, which is the only shape the browser
// sends in an Origin header and therefore the only shape worth allow-listing.
func isExactOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.Path == ""
}

// SecurityHeaders sets browser hardening headers appropriate for a JSON API
// that never serves HTML: no framing, no content sniffing, no referrer, no
// caching of authenticated responses.
func SecurityHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	c.Header("Cache-Control", "no-store")
	c.Next()
}

// CORS rejects any request whose Origin header is not exactly one of
// allowedOrigins with 403 (requests without an Origin are not browser
// cross-origin requests and pass through untouched), reflects an allowed
// origin with credentials enabled, and answers preflight OPTIONS with 204.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := append([]string(nil), allowedOrigins...)
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && !slices.Contains(allowed, origin) {
			apierror.Write(c, http.StatusForbidden, apierror.CodeOrigin, "request origin is not allowed")
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers",
				"Content-Type, Authorization, Idempotency-Key, X-CSRF-Token, X-Request-ID, X-Correlation-ID, X-Quack-Ops-Key")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodyLimit wraps the request body in http.MaxBytesReader so JSON and form
// decoders fail with a 413 instead of allocating without bound.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// CSRF applies the double-submit check to mutating requests that authenticate
// with the session cookie: the Origin header must be one of allowedOrigins and
// the X-CSRF-Token header must equal the CSRF cookie. Safe methods, requests
// with a Bearer credential (non-browser adapters), and requests without the
// session cookie are exempt because they cannot be cross-site cookie replays.
func CSRF(auth config.AuthConfig, allowedOrigins []string) gin.HandlerFunc {
	allowed := append([]string(nil), allowedOrigins...)
	return func(c *gin.Context) {
		if !isMutatingMethod(c.Request.Method) || hasBearerCredential(c) {
			c.Next()
			return
		}
		if _, err := c.Cookie(auth.SessionCookieName); err != nil {
			c.Next()
			return
		}
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" || !slices.Contains(allowed, origin) {
			apierror.Write(c, http.StatusForbidden, apierror.CodeCSRF, "CSRF validation failed")
			return
		}
		cookieToken, err := c.Cookie(auth.CSRFCookieName)
		headerToken := strings.TrimSpace(c.GetHeader(csrfHeader))
		if err != nil || cookieToken == "" || headerToken == "" || !constantTimeEqual(cookieToken, headerToken) {
			apierror.Write(c, http.StatusForbidden, apierror.CodeCSRF, "CSRF validation failed")
			return
		}
		c.Next()
	}
}

// constantTimeEqual compares two tokens without leaking where they differ.
func constantTimeEqual(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// hasBearerCredential reports whether the request carries a non-empty
// "Authorization: Bearer" credential, which marks an explicitly authenticated
// non-browser client rather than an ambient cookie.
func hasBearerCredential(c *gin.Context) bool {
	parts := strings.Fields(c.GetHeader("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != ""
}
