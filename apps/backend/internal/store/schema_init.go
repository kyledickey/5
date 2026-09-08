package store

import (
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// currentSchema identifies databases created directly from the pre-release model
// definitions. It is separate from the historical migration checksum ledger.
type currentSchema struct {
	ID uint `gorm:"primaryKey;autoIncrement:false"`
}

// TableName identifies the direct-initialization marker.
func (currentSchema) TableName() string { return "quack_current_schema" }

// InitializeSchema creates the current pre-release schema on an empty database,
// or reconciles a database already initialized this way. It never silently adopts
// a historical database; callers must explicitly preserve or reset that data.
func (s *Store) InitializeSchema() error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	return withMigrationLock(s.db, func() error {
		db := withMySQLTableOptions(s.db)
		if !db.Migrator().HasTable(&currentSchema{}) {
			tables, err := db.Migrator().GetTables()
			if err != nil {
				return fmt.Errorf("inspect existing tables: %w", err)
			}
			if len(tables) != 0 {
				return errors.New("direct schema initialization requires an empty database; existing tables were left unchanged")
			}
			// Create the marker first so interrupted MySQL DDL can be retried.
			if err := db.Migrator().CreateTable(&currentSchema{}); err != nil {
				return fmt.Errorf("create current schema marker: %w", err)
			}
		}
		models := append(schemaModels(), &GuildSettingsRecord{}, &GuildAppealSettingsRecord{}, &AppealNotificationRecord{}, &V4ImportBatchRecord{}, &V4ImportSourceRecord{})
		if err := db.AutoMigrate(models...); err != nil {
			return fmt.Errorf("initialize core schema: %w", err)
		}
		for _, module := range []modules.Migration{modules.RegistryMigration(), tickets.Migration(), honeypot.Migration()} {
			if err := module.Apply(db); err != nil {
				return fmt.Errorf("initialize %s schema: %w", module.Name, err)
			}
		}
		if err := applyFinalStorageConstraints(db); err != nil {
			return fmt.Errorf("initialize storage constraints: %w", err)
		}
		if err := s.db.FirstOrCreate(&currentSchema{ID: 1}).Error; err != nil {
			return fmt.Errorf("record current schema initialization: %w", err)
		}
		return nil
	})
}
