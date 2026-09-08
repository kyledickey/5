// Package readiness exercises the final process composition across Quack's
// storage, service, optional-module, and HTTP boundaries.
package readiness

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/routes"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCleanInstallComposesEveryAcceptedV5Surface rehearses a clean migration
// and proves the accepted core, appeal, audit, and optional-module HTTP
// registrars can coexist in one process without external network access.
func TestCleanInstallComposesEveryAcceptedV5Surface(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:v5-readiness-clean-install?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open clean database: %v", err)
	}
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	repository := store.New(db, redisClient)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate clean database: %v", err)
	}
	assertCurrentSchemaReady(t, db)

	cfg := config.Default()
	cfg.Discord.AppID = "123456789012345678"
	services := quack.NewWithConfigDependencies(cfg, repository, nil, nil, nil)
	session, err := discordgo.New("Bot readiness-test-token")
	if err != nil {
		t.Fatalf("construct offline Discord session: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	modules, err := moduleintegration.New(ctx, repository, session, services)
	if err != nil {
		t.Fatalf("compose optional modules: %v", err)
	}
	t.Cleanup(modules.Close)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := routes.SetupRoutesWithModules(engine, services, modules); err != nil {
		t.Fatalf("compose HTTP routes: %v", err)
	}
	assertRoutes(t, engine, []string{
		"GET /livez",
		"GET /readyz",
		"GET /metrics",
		"GET /status",
		"GET /guilds/:discordGuildID/templates",
		"POST /guilds/:discordGuildID/cases",
		"POST /guilds/:discordGuildID/cases/:caseRef/void",
		"GET /guilds/:discordGuildID/audit-log",
		"GET /guilds/:discordGuildID/statistics",
		"GET /guilds/:discordGuildID/appeals",
		"POST /guilds/:discordGuildID/appeals/:appealID/accept",
		"GET /members/me/guilds/:guildID/cases",
		"POST /members/me/cases/:caseID/appeal",
		"GET /guilds/:discordGuildID/modules/tickets/status",
		"GET /guilds/:discordGuildID/modules/general-logging/settings",
		"GET /guilds/:discordGuildID/modules/honeypot/settings",
	})
}

// assertCurrentSchemaReady verifies clean startup uses the current definitions
// and has completed initialization without replaying the retired migration chain.
func assertCurrentSchemaReady(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasTable("quack_schema_migrations") {
		t.Fatal("clean installation replayed historical migrations")
	}
	if version, err := store.New(db, nil).MigrationReadiness(context.Background()); err != nil || version != 1 {
		t.Fatalf("current schema is not ready: version=%d err=%v", version, err)
	}
}

// assertRoutes verifies the final router exposes each product surface through
// the central composition point rather than package-local tests alone.
func assertRoutes(t *testing.T, engine *gin.Engine, expected []string) {
	t.Helper()
	present := make(map[string]bool, len(engine.Routes()))
	for _, route := range engine.Routes() {
		present[fmt.Sprintf("%s %s", route.Method, route.Path)] = true
	}
	for _, route := range expected {
		if !present[route] {
			t.Errorf("final composition is missing route %s", route)
		}
	}
}
