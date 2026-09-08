package config

// Config is the complete immutable process configuration passed into runtime assembly.
type Config struct {
	Environment string `env:"ENVIRONMENT"`
	// ApplicationBaseURL is the optional public website destination for links.
	// CORS origins never supply a fallback for this independently configured URL.
	ApplicationBaseURL string `env:"APPLICATION_BASE_URL"`
	API                APIConfig
	Discord            DiscordConfig
	Auth               AuthConfig
	RateLimits         RateLimitConfig
	Storage            StorageConfig
	EventQueue         EventQueueConfig
	Observability      ObservabilityConfig
}

// RateLimitConfig defines documented fail-closed limits for dashboard and Discord adapter classes.
type RateLimitConfig struct {
	OAuth               RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_OAUTH_"`
	MemberRead          RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_MEMBER_READ_"`
	TemplateWrite       RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_TEMPLATE_WRITE_"`
	CaseCreate          RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_CASE_CREATE_"`
	Retry               RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_RETRY_"`
	Evidence            RateLimitPolicyConfig `envPrefix:"RATE_LIMIT_EVIDENCE_"`
	IdempotencyTTLHours int                   `env:"HTTP_IDEMPOTENCY_TTL_HOURS"`
}

// RateLimitPolicyConfig defines a maximum request count within a fixed window.
type RateLimitPolicyConfig struct {
	Maximum       int `env:"MAXIMUM"`
	WindowSeconds int `env:"WINDOW_SECONDS"`
}

// APIConfig controls the HTTP listener and privileged operations endpoint.
type APIConfig struct {
	Port                     string   `env:"API_PORT"`
	OpsStatusToken           string   `env:"OPS_STATUS_TOKEN"`
	CORSAllowedOrigins       []string `env:"API_CORS_ALLOWED_ORIGINS"`
	TrustedProxies           []string `env:"API_TRUSTED_PROXIES"`
	MaxBodyBytes             int64    `env:"API_MAX_BODY_BYTES"`
	ReadHeaderTimeoutSeconds int      `env:"API_READ_HEADER_TIMEOUT_SECONDS"`
	ReadTimeoutSeconds       int      `env:"API_READ_TIMEOUT_SECONDS"`
	WriteTimeoutSeconds      int      `env:"API_WRITE_TIMEOUT_SECONDS"`
	IdleTimeoutSeconds       int      `env:"API_IDLE_TIMEOUT_SECONDS"`
	ShutdownTimeoutSeconds   int      `env:"SHUTDOWN_TIMEOUT_SECONDS"`
}

// DiscordConfig contains Discord credentials, OAuth settings, and command synchronization policy.
type DiscordConfig struct {
	Token            string `env:"DISCORD_TOKEN"`
	AppID            string `env:"DISCORD_APP_ID"`
	ClientSecret     string `env:"DISCORD_CLIENT_SECRET"`
	OAuthRedirectURI string `env:"DISCORD_OAUTH_REDIRECT_URI"`
	OAuthScopes      string `env:"DISCORD_OAUTH_SCOPES"`
	CommandGuildID   string `env:"DISCORD_COMMAND_GUILD_ID"`
	CommandPrune     bool   `env:"DISCORD_COMMAND_PRUNE"`
}

// AuthConfig controls session lifetime, cookie behavior, and the post-login destination.
type AuthConfig struct {
	SessionCookieName string `env:"AUTH_SESSION_COOKIE_NAME"`
	CSRFCookieName    string `env:"AUTH_CSRF_COOKIE_NAME"`
	SessionTTLHours   int    `env:"AUTH_SESSION_TTL_HOURS"`
	StateTTLMinutes   int    `env:"AUTH_STATE_TTL_MINUTES"`
	PostLoginRedirect string `env:"AUTH_POST_LOGIN_REDIRECT"`
	CookieSecure      bool   `env:"AUTH_COOKIE_SECURE"`
}

// StorageConfig identifies the MySQL and Redis instances used by adapters.
type StorageConfig struct {
	DBDSN    string `env:"DATABASE_DSN"`
	RedisURL string `env:"REDIS_URL"`
}

// EventQueueConfig bounds the in-process action queue and worker concurrency.
type EventQueueConfig struct {
	Size    int `env:"EVENT_QUEUE_SIZE"`
	Workers int `env:"EVENT_QUEUE_WORKERS"`
}

// ObservabilityConfig controls the bounded metrics endpoint and service identity.
type ObservabilityConfig struct {
	LogLevel     string `env:"LOG_LEVEL"`
	MetricsToken string `env:"METRICS_TOKEN"`
	ServiceName  string `env:"SERVICE_NAME"`
}
