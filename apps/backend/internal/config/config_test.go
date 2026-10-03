package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func validStartupConfig() Config {
	cfg := Default()
	cfg.Storage = StorageConfig{DBDSN: "user:password@tcp(database:3306)/quack", RedisURL: "redis://redis:6379/0"}
	cfg.Discord = DiscordConfig{Token: "bot-token", AppID: "application-id", OAuthScopes: "identify guilds"}
	return cfg
}

func TestValidateRejectsIncompleteAndUnsafeStartupConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "storage", mutate: func(cfg *Config) { cfg.Storage.DBDSN = "" }, want: "DATABASE_DSN"},
		{name: "discord", mutate: func(cfg *Config) { cfg.Discord.Token = "" }, want: "DISCORD_TOKEN"},
		{name: "queue", mutate: func(cfg *Config) { cfg.EventQueue.Workers = 0 }, want: "EVENT_QUEUE"},
		{name: "shutdown", mutate: func(cfg *Config) { cfg.API.ShutdownTimeoutSeconds = 0 }, want: "SHUTDOWN_TIMEOUT_SECONDS"},
		{name: "service", mutate: func(cfg *Config) { cfg.Observability.ServiceName = "" }, want: "SERVICE_NAME"},
		{name: "environment", mutate: func(cfg *Config) { cfg.Environment = "prod-ish" }, want: "ENVIRONMENT"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validStartupConfig()
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected actionable %s error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateRequiresProductionOAuthAndOperatorSecrets(t *testing.T) {
	cfg := validStartupConfig()
	cfg.Environment = "production"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected incomplete production configuration to fail")
	}
	cfg.Discord.ClientSecret = "client-secret"
	cfg.Discord.OAuthRedirectURI = "https://dashboard.example.com/auth/callback"
	cfg.API.OpsStatusToken = "ops-secret"
	cfg.Observability.MetricsToken = "metrics-secret"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected complete production configuration: %v", err)
	}
}

// TestParseEnvironmentDefaults guards the shared defaults used by startup and tests.
func TestParseEnvironmentDefaults(t *testing.T) {
	for _, values := range []map[string]string{{}, {"EVENT_QUEUE_WORKERS": "", "AUTH_COOKIE_SECURE": ""}} {
		cfg, err := parseEnvironment(values)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cfg, Default()) {
			t.Fatalf("loaded defaults differ from Default: %+v", cfg)
		}
	}
}

// TestParseEnvironmentModes applies mode-specific safety defaults without
// changing which Discord credential names are loaded.
func TestParseEnvironmentModes(t *testing.T) {
	for _, mode := range []string{"dev", "test", "staging", "production"} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := parseEnvironment(map[string]string{
				"ENVIRONMENT":               mode,
				"DISCORD_TOKEN":             "token",
				"DISCORD_APP_ID":            "app",
				"DISCORD_CLIENT_SECRET":     "secret",
				"DEV_DISCORD_TOKEN":         "ignored-dev-token",
				"DEV_DISCORD_APP_ID":        "ignored-dev-app",
				"DEV_DISCORD_CLIENT_SECRET": "ignored-dev-secret",
			})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Discord.Token != "token" || cfg.Discord.AppID != "app" || cfg.Discord.ClientSecret != "secret" {
				t.Fatalf("wrong credentials for %s", mode)
			}
			if cfg.Auth.CookieSecure != (mode != "dev") {
				t.Fatal("wrong secure cookie default")
			}
			if (len(cfg.API.CORSAllowedOrigins) > 0) != (mode == "dev") {
				t.Fatal("wrong CORS defaults")
			}
		})
	}
}

// TestParseEnvironmentOverrides verifies typed settings, nested policies, and list normalization.
func TestParseEnvironmentOverrides(t *testing.T) {
	cfg, err := parseEnvironment(map[string]string{
		"ENVIRONMENT": "production", "API_PORT": "9090", "API_MAX_BODY_BYTES": "4294967296",
		"AUTH_COOKIE_SECURE": "false", "DISCORD_COMMAND_PRUNE": "true",
		"EVENT_QUEUE_WORKERS": "7", "RATE_LIMIT_OAUTH_MAXIMUM": "42",
		"RATE_LIMIT_EVIDENCE_WINDOW_SECONDS": "123", "HTTP_IDEMPOTENCY_TTL_HOURS": "12",
		"API_CORS_ALLOWED_ORIGINS": " https://example.com, ,https://other.example.com ,",
		"API_TRUSTED_PROXIES":      " 127.0.0.1, ,10.0.0.0/8 ", "LOG_LEVEL": "debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Port != "9090" || cfg.API.MaxBodyBytes != 4294967296 || cfg.Auth.CookieSecure || !cfg.Discord.CommandPrune || cfg.EventQueue.Workers != 7 || cfg.Observability.LogLevel != "debug" {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	if cfg.RateLimits.OAuth.Maximum != 42 || cfg.RateLimits.OAuth.WindowSeconds != 600 || cfg.RateLimits.Evidence.WindowSeconds != 123 || cfg.RateLimits.IdempotencyTTLHours != 12 {
		t.Fatalf("wrong rate limits: %+v", cfg.RateLimits)
	}
	if !reflect.DeepEqual(cfg.API.CORSAllowedOrigins, []string{"https://example.com", "https://other.example.com"}) || !reflect.DeepEqual(cfg.API.TrustedProxies, []string{"127.0.0.1", "10.0.0.0/8"}) {
		t.Fatalf("wrong lists: %+v", cfg.API)
	}
}

// TestParseEnvironmentRejectsMalformedValues prevents silent fallback on decoding failures.
func TestParseEnvironmentRejectsMalformedValues(t *testing.T) {
	for _, key := range []string{"API_MAX_BODY_BYTES", "EVENT_QUEUE_WORKERS", "AUTH_COOKIE_SECURE", "DISCORD_COMMAND_PRUNE", "RATE_LIMIT_RETRY_MAXIMUM"} {
		t.Run(key, func(t *testing.T) {
			if _, err := parseEnvironment(map[string]string{key: "invalid"}); err == nil {
				t.Fatal("expected parsing error")
			}
		})
	}
	if _, err := parseEnvironment(map[string]string{"API_MAX_BODY_BYTES": "9223372036854775808"}); err == nil {
		t.Fatal("expected overflow error")
	}
}

// TestLoadDotEnvPrecedence exercises file merging without mutating the process environment.
func TestLoadDotEnvPrecedence(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("ENVIRONMENT", "dev")
	t.Setenv("API_PORT", "9090")
	// Restore the caller's value while leaving this setting absent for the file test.
	t.Setenv("DISCORD_TOKEN", "")
	if err := os.Unsetenv("DISCORD_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("API_PORT=9999\nDISCORD_TOKEN=file-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.API.Port != "9090" || cfg.Discord.Token != "file-token" {
		t.Fatal("wrong .env precedence")
	}
	if _, exists := os.LookupEnv("DISCORD_TOKEN"); exists {
		t.Fatal("Load mutated the process environment")
	}
	t.Setenv("ENVIRONMENT", "production")
	if err := os.WriteFile(".env", []byte("EVENT_QUEUE_WORKERS=invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err != nil {
		t.Fatalf("production read .env: %v", err)
	}
}

// TestValidateUsesLoadedValues ensures validation is independent of later environment changes.
func TestValidateUsesLoadedValues(t *testing.T) {
	cfg := validStartupConfig()
	t.Setenv("EVENT_QUEUE_WORKERS", "invalid")
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validation read process environment: %v", err)
	}
	cfg.API.MaxBodyBytes = 0
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "API_MAX_BODY_BYTES") {
		t.Fatalf("expected body size error, got %v", err)
	}
	cfg.API.MaxBodyBytes = Default().API.MaxBodyBytes
	cfg.RateLimits.Evidence.WindowSeconds = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "RATE_LIMIT_EVIDENCE") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}

// TestApplicationBaseURLIsOptionalExplicitAndNormalized covers environment
// loading and the independent validation used by programmatic configurations.
func TestApplicationBaseURLIsOptionalExplicitAndNormalized(t *testing.T) {
	for _, fixture := range []struct{ input, want string }{
		{"", ""},
		{" https://example.com/ ", "https://example.com"},
		{"https://example.com/quack///", "https://example.com/quack"},
		{"https://example.com/old/../quack/", "https://example.com/quack"},
		{"https://example.com:8443/a%20b/", "https://example.com:8443/a%20b"},
	} {
		cfg, err := parseEnvironment(map[string]string{"ENVIRONMENT": "test", "APPLICATION_BASE_URL": fixture.input, "API_CORS_ALLOWED_ORIGINS": "https://wrong.example"})
		if err != nil || cfg.ApplicationBaseURL != fixture.want {
			t.Fatalf("load %q: got %q err=%v", fixture.input, cfg.ApplicationBaseURL, err)
		}
	}
}

// TestApplicationBaseURLRejectsMalformedConfiguration fails startup with a
// setting-specific error rather than silently substituting a CORS origin.
func TestApplicationBaseURLRejectsMalformedConfiguration(t *testing.T) {
	for _, raw := range []string{"/dashboard", "//example.com", "http://example.com", "https://", "https://user:secret@example.com", "https://example.com?next=/", "https://example.com?", "https://example.com#section", "https://example.com#", "https://bad host/", "https://example.com:invalid", "https://example.com:0", "https://example.com:65536"} {
		t.Run(raw, func(t *testing.T) {
			_, err := parseEnvironment(map[string]string{"APPLICATION_BASE_URL": raw})
			if err == nil || !strings.Contains(err.Error(), "APPLICATION_BASE_URL") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("missing safe actionable loading failure: %v", err)
			}
			cfg := validStartupConfig()
			cfg.ApplicationBaseURL = raw
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "APPLICATION_BASE_URL") {
				t.Fatalf("validation accepted invalid URL: %v", err)
			}
		})
	}
}
