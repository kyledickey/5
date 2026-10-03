package modules_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/driver/mysql"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRegistryKeepsGuildsAndModulesIndependent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:module-registry?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(modules.SchemaTypes()...); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db),
		modules.Descriptor{ID: modules.Tickets, DisplayName: "Tickets"},
		modules.Descriptor{ID: modules.GeneralLogging, DisplayName: "General logging"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild-a", ModuleID: modules.Tickets, Enabled: true, ConfigJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild-a", ModuleID: modules.GeneralLogging, Enabled: false, ConfigJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	if got, err := registry.Configuration(ctx, "guild-a", modules.Tickets); err != nil || got == nil || !got.Enabled {
		t.Fatalf("tickets: %+v, %v", got, err)
	}
	if got, err := registry.Configuration(ctx, "guild-a", modules.GeneralLogging); err != nil || got == nil || got.Enabled {
		t.Fatalf("logging: %+v, %v", got, err)
	}
	if got, err := registry.Configuration(ctx, "guild-b", modules.Tickets); err != nil || got != nil {
		t.Fatalf("guild leak: %+v, %v", got, err)
	}
}

func TestMySQLOptionalModuleMigrations(t *testing.T) {
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ParseTime = true
	databaseName := fmt.Sprintf("quack_modules_%d", time.Now().UTC().UnixNano())
	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := gorm.Open(mysql.Open(adminCfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`").Error; err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	testCfg := *cfg
	testCfg.DBName = databaseName
	db, err := gorm.Open(mysql.Open(testCfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(append(modules.SchemaTypes(), tickets.SchemaTypes()...)...); err != nil {
		t.Fatalf("create module schema: %v", err)
	}
	for _, table := range []string{"module_configurations", "module_import_records", "tickets", "ticket_events", "ticket_transcripts", "ticket_member_states"} {
		if !db.Migrator().HasTable(table) {
			t.Errorf("missing table %s", table)
		}
	}
}
