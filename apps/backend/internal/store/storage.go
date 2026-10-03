package store

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	r "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Store owns the GORM handle and the optional Redis client and implements the
// persistence ports in internal/quack. The raw clients are exposed only through
// DB and Redis for adapter-local code (migrations, module stores, tests) so
// transport and domain packages cannot bypass repository rules. A Store is safe
// for concurrent use; executableMu guards the guild-fairness cursor only.
type Store struct {
	db    *gorm.DB
	redis *r.Client

	executableMu          sync.Mutex
	executableGuildCursor string
}

// New wires a Store around an open database and an optional Redis client and
// installs the append-only audit callback on db. redis may be nil, in which
// case the Redis-backed methods return an error. The callback is registered
// once per *gorm.DB, so wrapping a transaction with New is cheap.
func New(db *gorm.DB, redis *r.Client) *Store {
	installAuditImmutability(db)
	return &Store{db: db, redis: redis}
}

// WithGuildCaseLock runs fn inside one transaction while holding a row lock on
// the guild, serializing case numbering and escalation selection per guild. The
// repository passed to fn is bound to that transaction.
func (s *Store) WithGuildCaseLock(ctx context.Context, guildID string, fn func(quack.CaseRepository) error) error {
	if guildID == "" {
		return errors.New("guild id is required")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild model.Guild
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", guildID).Limit(1).Find(&guild)
		if result.Error != nil {
			return fmt.Errorf("lock guild for case creation: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return errors.New("guild not found")
		}
		return fn(New(tx, s.redis))
	})
}

// PingDatabase reports database connectivity for health endpoints.
func (s *Store) PingDatabase(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// PingRedis reports Redis connectivity for health endpoints; a Store built
// without Redis reports it as not connected.
func (s *Store) PingRedis(ctx context.Context) error {
	if s.redis == nil {
		return errors.New("redis not connected")
	}
	return s.redis.Ping(ctx).Err()
}

// HashGet reads one field of a Redis hash. The Discord command registrar uses
// it to compare stored command digests so unchanged commands skip resync.
func (s *Store) HashGet(ctx context.Context, key, field string) ([]byte, error) {
	if s.redis == nil {
		return nil, errors.New("redis not connected")
	}
	return s.redis.HGet(ctx, key, field).Bytes()
}

// HashSet writes one field of a Redis hash; see HashGet.
func (s *Store) HashSet(ctx context.Context, key, field string, value []byte) error {
	if s.redis == nil {
		return errors.New("redis not connected")
	}
	return s.redis.HSet(ctx, key, field, value).Err()
}

// Redis returns the owned Redis client (nil when the Store was built without
// one) for adapter-local features such as command caching.
func (s *Store) Redis() *r.Client {
	return s.redis
}

// DB returns the owned GORM handle for adapter-local integration, module
// stores, migration tooling, and tests.
func (s *Store) DB() *gorm.DB {
	return s.db
}
