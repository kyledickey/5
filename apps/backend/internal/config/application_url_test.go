package config

import (
	"strings"
	"testing"
)

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
