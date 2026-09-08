package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/internal/store"
)

// main runs the explicit migration operator command without starting Discord, Redis, or HTTP adapters.
func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run validates the requested direction, opens MySQL, and executes exactly one migration operation.
func run(args []string) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "down" && args[0] != "init" && args[0] != "adopt" && args[0] != "legacy-up") {
		return errors.New("usage: quack-migrate init|adopt|up|legacy-up|down")
	}
	_ = godotenv.Load(".env")
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		return errors.New("DATABASE_DSN is required")
	}
	db, err := store.OpenMySQL(dsn)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle: %w", err)
	}
	defer sqlDB.Close()

	repositories := store.New(db, nil)
	if args[0] == "legacy-up" {
		return repositories.MigrateLegacySchema()
	}
	if args[0] == "adopt" {
		return repositories.AdoptCurrentSchema()
	}
	if args[0] == "init" {
		return repositories.InitializeSchema()
	}
	if args[0] == "down" {
		if err := repositories.RollbackLastMigration(); err != nil {
			return fmt.Errorf("roll back latest migration: %w", err)
		}
		return nil
	}
	if err := repositories.Migrate(); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
