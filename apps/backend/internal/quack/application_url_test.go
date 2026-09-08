package quack

import (
	"testing"

	"github.com/quackdiscord/bot/internal/config"
)

// TestApplicationLinksNeverInferCORSOrigin verifies service composition uses
// only the explicit website destination; native appeals need no website.
func TestApplicationLinksNeverInferCORSOrigin(t *testing.T) {
	for _, base := range []string{"", "https://website.example/quack"} {
		cfg := config.Default()
		cfg.API.CORSAllowedOrigins = []string{"https://cors-only.example"}
		cfg.ApplicationBaseURL = base
		services := NewWithConfigDependencies(cfg, nil, nil, nil, nil)
		if services.Actions.dashboardBaseURL != base {
			t.Fatalf("application destination %q became %q", base, services.Actions.dashboardBaseURL)
		}
	}
}
