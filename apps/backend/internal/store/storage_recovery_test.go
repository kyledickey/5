package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRecoveryManifestDetectsRestoredDataDrift(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}
	manifest, err := repositories.BuildRecoveryManifest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := repositories.VerifyRecoveryManifest(context.Background(), *manifest); err != nil {
		t.Fatalf("verify unchanged restore: %v", err)
	}
	if err := db.Exec(`INSERT INTO v4_import_batches (id, guild_id, source_name, checksum, actor_discord_user_id, record_count, created_count, already_imported_count, warning_count, created_at) VALUES ('drift', 'guild', 'source', 'checksum', 'actor', 0, 0, 0, 0, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := repositories.VerifyRecoveryManifest(context.Background(), *manifest); err == nil {
		t.Fatal("expected restored-data drift")
	}
}

func TestRedisRecoveryProbePersistsAcrossClientsAndCleansUp(t *testing.T) {
	server := miniredis.RunT(t)
	first := redis.NewClient(&redis.Options{Addr: server.Addr()})
	if err := WriteRedisRecoveryProbe(context.Background(), first, "rehearsal", "token"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer second.Close()
	if err := VerifyAndDeleteRedisRecoveryProbe(context.Background(), second, "rehearsal", "token"); err != nil {
		t.Fatal(err)
	}
	if server.Exists("quack:v5:recovery:test:rehearsal") {
		t.Fatal("recovery probe was not cleaned up")
	}
}

func TestRealRedisRecoveryProbeDurabilityAndCleanup(t *testing.T) {
	rawURL := os.Getenv("QUACK_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("QUACK_TEST_REDIS_URL is not configured")
	}
	first, err := OpenRedis(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	namespace := fmt.Sprintf("qp-g-%d", time.Now().UTC().UnixNano())
	if err := WriteRedisRecoveryProbe(context.Background(), first, namespace, "durable-token"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second, err := OpenRedis(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := VerifyAndDeleteRedisRecoveryProbe(context.Background(), second, namespace, "durable-token"); err != nil {
		t.Fatal(err)
	}
}
