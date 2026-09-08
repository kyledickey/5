package config

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Validate rejects incomplete or unsafe startup configuration before any
// database, Redis, Discord, worker, or listener side effect occurs.
func (c Config) Validate() error {
	if _, err := normalizeApplicationBaseURL(c.ApplicationBaseURL); err != nil {
		return err
	}
	if c.Environment != "dev" && c.Environment != "test" && c.Environment != "staging" && c.Environment != "production" {
		return fmt.Errorf("ENVIRONMENT must be one of dev, test, staging, or production")
	}
	if strings.TrimSpace(c.API.Port) == "" {
		return fmt.Errorf("API_PORT is required")
	}
	if c.API.ShutdownTimeoutSeconds <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT_SECONDS must be positive")
	}
	if c.EventQueue.Size <= 0 || c.EventQueue.Workers <= 0 {
		return fmt.Errorf("EVENT_QUEUE_SIZE and EVENT_QUEUE_WORKERS must be positive")
	}
	if strings.TrimSpace(c.Observability.ServiceName) == "" {
		return fmt.Errorf("SERVICE_NAME is required")
	}
	if strings.TrimSpace(c.Storage.DBDSN) == "" || strings.TrimSpace(c.Storage.RedisURL) == "" {
		return fmt.Errorf("DATABASE_DSN and REDIS_URL are required")
	}
	if strings.TrimSpace(c.Discord.Token) == "" || strings.TrimSpace(c.Discord.AppID) == "" {
		return fmt.Errorf("DISCORD_TOKEN and DISCORD_APP_ID are required")
	}
	if c.Environment == "staging" || c.Environment == "production" {
		if strings.TrimSpace(c.Discord.ClientSecret) == "" || strings.TrimSpace(c.Discord.OAuthRedirectURI) == "" {
			return fmt.Errorf("DISCORD_CLIENT_SECRET and DISCORD_OAUTH_REDIRECT_URI are required outside development")
		}
		if strings.TrimSpace(c.API.OpsStatusToken) == "" || strings.TrimSpace(c.Observability.MetricsToken) == "" {
			return fmt.Errorf("OPS_STATUS_TOKEN and METRICS_TOKEN are required outside development")
		}
		redirect, err := url.Parse(c.Discord.OAuthRedirectURI)
		if err != nil || redirect.Scheme != "https" || redirect.Host == "" || redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" {
			return fmt.Errorf("DISCORD_OAUTH_REDIRECT_URI must be an exact HTTPS URL outside development")
		}
		if !slices.Contains(strings.Fields(c.Discord.OAuthScopes), "identify") || !slices.Contains(strings.Fields(c.Discord.OAuthScopes), "guilds") {
			return fmt.Errorf("DISCORD_OAUTH_SCOPES must include identify and guilds")
		}
	}
	for key, value := range map[string]int64{
		"API_MAX_BODY_BYTES":              c.API.MaxBodyBytes,
		"API_READ_HEADER_TIMEOUT_SECONDS": int64(c.API.ReadHeaderTimeoutSeconds),
		"API_READ_TIMEOUT_SECONDS":        int64(c.API.ReadTimeoutSeconds),
		"API_WRITE_TIMEOUT_SECONDS":       int64(c.API.WriteTimeoutSeconds),
		"API_IDLE_TIMEOUT_SECONDS":        int64(c.API.IdleTimeoutSeconds),
		"AUTH_SESSION_TTL_HOURS":          int64(c.Auth.SessionTTLHours),
		"AUTH_STATE_TTL_MINUTES":          int64(c.Auth.StateTTLMinutes),
		"HTTP_IDEMPOTENCY_TTL_HOURS":      int64(c.RateLimits.IdempotencyTTLHours),
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be a positive integer", key)
		}
	}
	for class, policy := range map[string]RateLimitPolicyConfig{
		"OAUTH":          c.RateLimits.OAuth,
		"MEMBER_READ":    c.RateLimits.MemberRead,
		"TEMPLATE_WRITE": c.RateLimits.TemplateWrite,
		"CASE_CREATE":    c.RateLimits.CaseCreate,
		"RETRY":          c.RateLimits.Retry,
		"EVIDENCE":       c.RateLimits.Evidence,
	} {
		if policy.Maximum <= 0 || policy.WindowSeconds <= 0 {
			return fmt.Errorf("RATE_LIMIT_%s_MAXIMUM and RATE_LIMIT_%s_WINDOW_SECONDS must be positive", class, class)
		}
	}
	return nil
}
