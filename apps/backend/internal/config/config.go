package config

import (
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Load reads environment configuration, merging .env only in development
// without changing process variables. Existing process values take precedence.
// Parsing errors are returned before runtime assembly opens any connections.
func Load() (Config, error) {
	values := env.ToMap(os.Environ())
	if mode, exists := values["ENVIRONMENT"]; !exists || mode == "dev" {
		fileValues, err := godotenv.Read(".env")
		if err != nil {
			slog.Debug("Development .env file was not loaded")
		} else {
			for key, value := range fileValues {
				if _, exists := values[key]; !exists {
					values[key] = value
				}
			}
		}
	}
	return parseEnvironment(values)
}

// parseEnvironment applies process-specific defaults and credential selection
// to an environment snapshot; env owns all typed decoding and field bindings.
func parseEnvironment(values map[string]string) (Config, error) {
	cfg := Default()
	if mode, exists := values["ENVIRONMENT"]; exists {
		cfg.Environment = mode
	}
	if cfg.Environment == "dev" {
		for _, key := range []string{"DISCORD_TOKEN", "DISCORD_APP_ID", "DISCORD_CLIENT_SECRET"} {
			values[key] = values["DEV_"+key]
		}
	} else {
		cfg.API.CORSAllowedOrigins = nil
		cfg.Auth.CookieSecure = true
	}
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: values}); err != nil {
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

// cleanList normalizes decoded origins and proxies while discarding blank entries.
func cleanList(values []string) []string {
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return slices.DeleteFunc(values, func(value string) bool { return value == "" })
}

// Default returns development-safe configuration defaults before environment overrides are applied.
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
		Discord:       DiscordConfig{OAuthScopes: "identify guilds"},
		RateLimits:    defaultRateLimits(),
		EventQueue:    EventQueueConfig{Size: 1000, Workers: 3},
		Observability: ObservabilityConfig{LogLevel: "info", ServiceName: "quack"},
	}
}

// defaultRateLimits returns the product-wide adapter policy defaults used in tests and development.
func defaultRateLimits() RateLimitConfig {
	return RateLimitConfig{
		OAuth:               RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 600},
		MemberRead:          RateLimitPolicyConfig{Maximum: 120, WindowSeconds: 60},
		TemplateWrite:       RateLimitPolicyConfig{Maximum: 30, WindowSeconds: 60},
		CaseCreate:          RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 60},
		Retry:               RateLimitPolicyConfig{Maximum: 10, WindowSeconds: 60},
		Evidence:            RateLimitPolicyConfig{Maximum: 20, WindowSeconds: 60},
		IdempotencyTTLHours: 24,
	}
}
