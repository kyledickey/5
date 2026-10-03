package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/internal/store"
)

const usage = "usage: quack-storage-verify mysql-capture|mysql-verify|redis-write|redis-verify"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	_ = godotenv.Load(".env")
	switch args[0] {
	case "mysql-capture", "mysql-verify":
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
			return err
		}
		defer sqlDB.Close()
		repositories := store.New(db, nil)
		if args[0] == "mysql-capture" {
			manifest, err := repositories.BuildRecoveryManifest(ctx)
			if err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(manifest)
		}
		var manifest store.RecoveryManifest
		if err := json.NewDecoder(os.Stdin).Decode(&manifest); err != nil {
			return fmt.Errorf("decode recovery manifest: %w", err)
		}
		return repositories.VerifyRecoveryManifest(ctx, manifest)
	case "redis-write", "redis-verify":
		rawURL, namespace, token := os.Getenv("REDIS_URL"), os.Getenv("QUACK_RECOVERY_NAMESPACE"), os.Getenv("QUACK_RECOVERY_TOKEN")
		if rawURL == "" || namespace == "" || token == "" {
			return errors.New("REDIS_URL, QUACK_RECOVERY_NAMESPACE, and QUACK_RECOVERY_TOKEN are required")
		}
		client, err := store.OpenRedis(rawURL)
		if err != nil {
			return err
		}
		defer client.Close()
		if args[0] == "redis-write" {
			return store.WriteRedisRecoveryProbe(ctx, client, namespace, token)
		}
		return store.VerifyAndDeleteRedisRecoveryProbe(ctx, client, namespace, token)
	default:
		return errors.New(usage)
	}
}
