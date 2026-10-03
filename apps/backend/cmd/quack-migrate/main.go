package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/internal/store"
)

// main creates or reconciles the database schema without starting Discord,
// Redis, or HTTP adapters. Normal startup does the same thing through
// Store.Migrate; this command exists for operators preparing a database first.
func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: quack-migrate")
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

	return store.New(db, nil).InitializeSchema()
}
