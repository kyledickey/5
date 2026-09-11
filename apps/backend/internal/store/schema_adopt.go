package store

import (
	"context"
	"errors"
	"fmt"
)

// AdoptCurrentSchema marks a fully migrated pre-release database for the current
// schema path without rewriting application tables or deleting its old ledger.
// Unknown, edited, incomplete, or dirty migration histories must be resolved first.
func (s *Store) AdoptCurrentSchema() error {
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
