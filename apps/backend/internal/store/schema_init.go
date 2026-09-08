package store

import (
	"context"
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
		models := append(schemaModels(), &GuildSettingsRecord{}, &GuildAppealSettingsRecord{}, &AppealNotificationRecord{}, &V4ImportBatchRecord{}, &V4ImportSourceRecord{}, &auditMirrorDelivery{})
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

// AdoptCurrentSchema marks a fully migrated pre-release database for the current
// schema path without rewriting application tables or deleting its old ledger.
// Unknown, edited, incomplete, or dirty migration histories must be resolved first.
func (s *Store) AdoptCurrentSchema() error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	return withMigrationLock(s.db, func() error {
		applied, err := loadAppliedMigrations(s.db)
		if err != nil {
			return err
		}
		registry := registeredMigrations()
		if err := validateAppliedMigrations(applied, registry); err != nil {
			return err
		}
		if len(applied) != len(registry) {
			return errors.New("schema adoption requires the complete pre-release migration history")
		}
		// Verify application-table presence before installing the marker. This also
		// refuses a restored ledger whose corresponding data tables were omitted.
		if _, err := s.BuildRecoveryManifest(context.Background()); err != nil {
			return fmt.Errorf("verify schema before adoption: %w", err)
		}
		if !s.db.Migrator().HasTable(&currentSchema{}) {
			if err := withMySQLTableOptions(s.db).Migrator().CreateTable(&currentSchema{}); err != nil {
				return err
			}
		}
		return s.db.FirstOrCreate(&currentSchema{ID: 1}).Error
	})
}
