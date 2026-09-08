package honeypot_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLIncidentLeasePrecision exercises the production timestamp precision
// for normal completion and an expired lease's recovery/cleanup transition.
func TestMySQLIncidentLeasePrecision(t *testing.T) {
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ParseTime = true
	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := gorm.Open(gormmysql.Open(adminCfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("quack_honeypot_recovery_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE `" + databaseName + "`").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`").Error; err != nil {
			t.Error(err)
		}
		if db, err := admin.DB(); err == nil {
			_ = db.Close()
		}
	})
	cfg.DBName = databaseName
	db, err := gorm.Open(gormmysql.Open(cfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := modules.RegistryMigration().Apply(db); err != nil {
		t.Fatal(err)
	}
	if err := honeypot.Migration().Apply(db); err != nil {
		t.Fatal(err)
	}
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), honeypot.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	validator := &validatorFake{}
	a := &recoveryApplier{cleanupApplier: &cleanupApplier{applierFake: &applierFake{}, attempts: map[string]int{}}, saved: map[string]string{}}
	service := honeypot.NewService(registry, honeypot.NewStore(db), nil, validator, validator, a)
	f := &fixture{db: db, registry: registry, service: service, validator: validator, applier: a.applierFake}
	enable(t, f, "guild-a")
	initial := message("normal")
	initial.AuthorDiscordUserID = "another-member"
	if _, err := service.HandleMessage(context.Background(), initial); err != nil {
		t.Fatalf("primary timestamp completion: %v", err)
	}
	trigger, _ := interruptedIncident(t, f)
	expireIncident(t, f, trigger)
	if guilds, err := service.RecoverPending(context.Background(), 1); err != nil || len(guilds) != 1 {
		t.Fatalf("recovery timestamp completion: %v %v", guilds, err)
	}
	if err := service.ProcessCleanups(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	if a.count() != 2 || len(a.attempts) != 2 {
		t.Fatal("MySQL lease lost work", a.count(), a.attempts)
	}
}
