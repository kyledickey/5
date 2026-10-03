// Package config loads and validates the immutable process configuration.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// Config is the complete immutable process configuration passed into runtime assembly.
type Config struct {
	Environment string `mapstructure:"ENVIRONMENT"`
	// ApplicationBaseURL is the optional public website destination for links.
	// CORS origins never supply a fallback for this independently configured URL.
	ApplicationBaseURL string              `mapstructure:"APPLICATION_BASE_URL"`
	API                APIConfig           `mapstructure:",squash"`
	Discord            DiscordConfig       `mapstructure:",squash"`
	Auth               AuthConfig          `mapstructure:",squash"`
	RateLimits         RateLimitConfig     `mapstructure:",squash"`
	Storage            StorageConfig       `mapstructure:",squash"`
	EventQueue         EventQueueConfig    `mapstructure:",squash"`
	Observability      ObservabilityConfig `mapstructure:",squash"`
}

// RateLimitConfig defines documented fail-closed limits for dashboard and Discord adapter classes.
type RateLimitConfig struct {
	OAuth               RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_OAUTH"`
	MemberRead          RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_MEMBER_READ"`
	TemplateWrite       RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_TEMPLATE_WRITE"`
	CaseCreate          RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_CASE_CREATE"`
	Retry               RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_RETRY"`
	Evidence            RateLimitPolicyConfig `mapstructure:"RATE_LIMIT_EVIDENCE"`
	IdempotencyTTLHours int                   `mapstructure:"HTTP_IDEMPOTENCY_TTL_HOURS"`
}

// RateLimitPolicyConfig defines a maximum request count within a fixed window.
type RateLimitPolicyConfig struct {
	Maximum       int `mapstructure:"MAXIMUM"`
	WindowSeconds int `mapstructure:"WINDOW_SECONDS"`
}

// APIConfig controls the HTTP listener and privileged operations endpoint.
type APIConfig struct {
	Port                     string   `mapstructure:"API_PORT"`
	OpsStatusToken           string   `mapstructure:"OPS_STATUS_TOKEN"`
	CORSAllowedOrigins       []string `mapstructure:"API_CORS_ALLOWED_ORIGINS"`
	TrustedProxies           []string `mapstructure:"API_TRUSTED_PROXIES"`
	MaxBodyBytes             int64    `mapstructure:"API_MAX_BODY_BYTES"`
	ReadHeaderTimeoutSeconds int      `mapstructure:"API_READ_HEADER_TIMEOUT_SECONDS"`
	ReadTimeoutSeconds       int      `mapstructure:"API_READ_TIMEOUT_SECONDS"`
	WriteTimeoutSeconds      int      `mapstructure:"API_WRITE_TIMEOUT_SECONDS"`
	IdleTimeoutSeconds       int      `mapstructure:"API_IDLE_TIMEOUT_SECONDS"`
	ShutdownTimeoutSeconds   int      `mapstructure:"SHUTDOWN_TIMEOUT_SECONDS"`
}

// DiscordConfig contains Discord credentials, OAuth settings, and command synchronization policy.
type DiscordConfig struct {
	Token            string `mapstructure:"DISCORD_TOKEN"`
	AppID            string `mapstructure:"DISCORD_APP_ID"`
	ClientSecret     string `mapstructure:"DISCORD_CLIENT_SECRET"`
	OAuthRedirectURI string `mapstructure:"DISCORD_OAUTH_REDIRECT_URI"`
	OAuthScopes      string `mapstructure:"DISCORD_OAUTH_SCOPES"`
	CommandGuildID   string `mapstructure:"DISCORD_COMMAND_GUILD_ID"`
	CommandPrune     bool   `mapstructure:"DISCORD_COMMAND_PRUNE"`
}

// AuthConfig controls session lifetime, cookie behavior, and the post-login destination.
type AuthConfig struct {
	SessionCookieName string `mapstructure:"AUTH_SESSION_COOKIE_NAME"`
	CSRFCookieName    string `mapstructure:"AUTH_CSRF_COOKIE_NAME"`
	SessionTTLHours   int    `mapstructure:"AUTH_SESSION_TTL_HOURS"`
	StateTTLMinutes   int    `mapstructure:"AUTH_STATE_TTL_MINUTES"`
	PostLoginRedirect string `mapstructure:"AUTH_POST_LOGIN_REDIRECT"`
	CookieSecure      bool   `mapstructure:"AUTH_COOKIE_SECURE"`
}

// StorageConfig identifies the MySQL and Redis instances used by adapters.
type StorageConfig struct {
	DBDSN    string `mapstructure:"DATABASE_DSN"`
	RedisURL string `mapstructure:"REDIS_URL"`
}

// EventQueueConfig bounds the in-process action queue and worker concurrency.
type EventQueueConfig struct {
	Size    int `mapstructure:"EVENT_QUEUE_SIZE"`
	Workers int `mapstructure:"EVENT_QUEUE_WORKERS"`
}

// ObservabilityConfig controls the bounded metrics endpoint and service identity.
type ObservabilityConfig struct {
	LogLevel     string `mapstructure:"LOG_LEVEL"`
	MetricsToken string `mapstructure:"METRICS_TOKEN"`
	ServiceName  string `mapstructure:"SERVICE_NAME"`
}

// Load reads environment configuration, merging .env only in development
// without changing process variables. Existing process values take precedence.
// Parsing errors are returned before runtime assembly opens any connections.
func Load() (Config, error) {
	settings := viper.NewWithOptions(viper.ExperimentalBindStruct())
	settings.AutomaticEnv()

	if mode := settings.GetString("ENVIRONMENT"); mode == "" || mode == "dev" {
		settings.SetConfigFile(".env")
		settings.SetConfigType("dotenv")
		if err := settings.ReadInConfig(); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return Config{}, fmt.Errorf("read development .env: %w", err)
			}
			slog.Debug("Development .env file was not loaded")
		}
	}

	return unmarshal(settings)
}

// parseEnvironment loads an isolated environment snapshot for focused tests.
func parseEnvironment(values map[string]string) (Config, error) {
	settings := viper.NewWithOptions(viper.ExperimentalBindStruct())
	for key, value := range values {
		// Viper intentionally treats empty environment variables as unset.
		if value != "" {
			settings.Set(key, value)
		}
	}
	return unmarshal(settings)
}

// unmarshal applies environment-specific defaults and decodes all registered
// settings into the immutable configuration snapshot.
func unmarshal(settings *viper.Viper) (Config, error) {
	cfg := Default()
	mode := settings.GetString("ENVIRONMENT")
	if mode != "" {
		cfg.Environment = mode
	}

	if cfg.Environment != "dev" {
		cfg.API.CORSAllowedOrigins = nil
		cfg.Auth.CookieSecure = true
	}
	for _, class := range []string{"OAUTH", "MEMBER_READ", "TEMPLATE_WRITE", "CASE_CREATE", "RETRY", "EVIDENCE"} {
		for _, field := range []string{"MAXIMUM", "WINDOW_SECONDS"} {
			flatKey := "RATE_LIMIT_" + class + "_" + field
			settings.RegisterAlias("RATE_LIMIT_"+class+"."+field, flatKey)
		}
	}

	if err := settings.Unmarshal(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment configuration: %w", err)
	}

	baseURL, err := normalizeApplicationBaseURL(cfg.ApplicationBaseURL)
	if err != nil {
		return Config{}, err
	}

	cfg.ApplicationBaseURL = baseURL
	cfg.API.CORSAllowedOrigins = cleanList(cfg.API.CORSAllowedOrigins)
	cfg.API.TrustedProxies = cleanList(cfg.API.TrustedProxies)

	if cfg.Environment == "dev" && len(cfg.API.CORSAllowedOrigins) == 0 {
		cfg.API.CORSAllowedOrigins = Default().API.CORSAllowedOrigins
	}

	return cfg, nil
}

func cleanList(values []string) []string {
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return slices.DeleteFunc(values, func(value string) bool { return value == "" })
}

// Default returns development-safe defaults applied before environment overrides.
func Default() Config {
	return Config{
		Environment: "dev",
		API: APIConfig{
			Port:                     "8080",
			CORSAllowedOrigins:       []string{"http://localhost:3000", "http://127.0.0.1:3000"},
			MaxBodyBytes:             1 << 20,
			ReadHeaderTimeoutSeconds: 5,
			ReadTimeoutSeconds:       15,
			WriteTimeoutSeconds:      30,
			IdleTimeoutSeconds:       60,
			ShutdownTimeoutSeconds:   20,
		},
		Auth: AuthConfig{
			SessionCookieName: "quack_session",
			CSRFCookieName:    "quack_csrf",
			SessionTTLHours:   168,
			StateTTLMinutes:   10,
			PostLoginRedirect: "/",
		},
		Discord: DiscordConfig{OAuthScopes: "identify guilds"},
		RateLimits: RateLimitConfig{
			OAuth:               RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 600},
			MemberRead:          RateLimitPolicyConfig{Maximum: 120, WindowSeconds: 60},
			TemplateWrite:       RateLimitPolicyConfig{Maximum: 30, WindowSeconds: 60},
			CaseCreate:          RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 60},
			Retry:               RateLimitPolicyConfig{Maximum: 10, WindowSeconds: 60},
			Evidence:            RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 60},
			IdempotencyTTLHours: 24,
		},
		EventQueue:    EventQueueConfig{Size: 1000, Workers: 3},
		Observability: ObservabilityConfig{LogLevel: "info", ServiceName: "quack"},
	}
}

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

// normalizeApplicationBaseURL validates the optional user-facing website base
// independently of browser CORS policy. Canonical paths preserve an application
// mount prefix while making subsequent relative link concatenation predictable.
// Errors name the setting without echoing credentials from malformed input.
func normalizeApplicationBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(raw, "#") || parsed.Opaque != "" {
		return "", errors.New("APPLICATION_BASE_URL must be an absolute HTTPS base URL without credentials, query or fragment")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return "", errors.New("APPLICATION_BASE_URL port must be between 1 and 65535")
		}
	}
	parsed.Path = strings.TrimRight(path.Clean("/"+strings.TrimLeft(parsed.Path, "/")), "/")
	parsed.RawPath = ""
	return parsed.String(), nil
}
