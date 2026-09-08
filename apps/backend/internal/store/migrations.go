package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const (
	migrationLockName = "quack_v5_schema_migrations"
	mysqlTableOptions = "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci"
)

var (
	// ErrMigrationChecksumMismatch reports that an applied migration no longer matches its reviewed definition.
	ErrMigrationChecksumMismatch = errors.New("migration checksum mismatch")
	// ErrMigrationDirty reports that a migration requires explicit operator recovery before startup can proceed.
	ErrMigrationDirty = errors.New("migration requires recovery")
	// ErrMigrationNotReversible reports that an operator requested an unsafe schema downgrade.
	ErrMigrationNotReversible = errors.New("migration is forward-only")
)

const (
	migrationStateApplied     = "applied"
	migrationStateRollingBack = "rolling_back"
)

// schemaMigration records one successfully applied, immutable migration definition.
type schemaMigration struct {
	Version   uint64    `gorm:"primaryKey;autoIncrement:false"`
	Name      string    `gorm:"size:191;not null;uniqueIndex"`
	Checksum  string    `gorm:"type:char(64);not null"`
	State     string    `gorm:"size:32;not null;default:'applied';index"`
	AppliedAt time.Time `gorm:"not null"`
	// RollbackStartedAt remains set while an idempotent Down operation needs operator recovery.
	RollbackStartedAt *time.Time `gorm:"index"`
}

// TableName keeps the v5 ledger separate from the obsolete unversioned schema_migrations table.
func (schemaMigration) TableName() string { return "quack_schema_migrations" }

// migration is one ordered schema transition and its optional inverse.
type migration struct {
	Version    uint64
	Name       string
	Definition string
	Source     string
	Up         func(*gorm.DB) error
	Down       func(*gorm.DB) error
}

// checksum returns the content identity stored in the migration ledger.
func (m migration) checksum() string {
	material := fmt.Sprintf("%d\n%s\n%s\n%s", m.Version, m.Name, m.Definition, m.Source)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(material)))
}

// Migrate recognizes directly initialized pre-release databases and reconciles
// their current models. Older databases still use the historical migration ledger
// while the transition to direct initialization is being completed.
func (s *Store) Migrate() error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	if s.db.Migrator().HasTable(&currentSchema{}) {
		return s.InitializeSchema()
	}

	return runMigrations(s.db, registeredMigrations())
}

// RollbackLastMigration reverses the newest applied migration when that migration declares a safe Down operation.
func (s *Store) RollbackLastMigration() error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	if s.db.Migrator().HasTable(&currentSchema{}) {
		return ErrMigrationNotReversible
	}

	return rollbackLastMigration(s.db, registeredMigrations())
}

// runMigrations validates the registry and applies its unapplied suffix under the database migration lock.
func runMigrations(db *gorm.DB, migrations []migration) error {
	if err := validateMigrationRegistry(migrations); err != nil {
		return err
	}

	return withMigrationLock(db, func() error {
		if err := ensureMigrationLedger(db); err != nil {
			return err
		}
		applied, err := loadAppliedMigrations(db)
		if err != nil {
			return err
		}
		if err := validateAppliedMigrations(applied, migrations); err != nil {
			return err
		}

		for index := len(applied); index < len(migrations); index++ {
			candidate := migrations[index]
			if err := db.Transaction(func(tx *gorm.DB) error {
				if err := candidate.Up(tx); err != nil {
					return fmt.Errorf("apply migration %d %s: %w", candidate.Version, candidate.Name, err)
				}
				entry := schemaMigration{
					Version: candidate.Version, Name: candidate.Name,
					Checksum: candidate.checksum(), State: migrationStateApplied, AppliedAt: time.Now().UTC(),
				}
				if err := tx.Create(&entry).Error; err != nil {
					return fmt.Errorf("record migration %d %s: %w", candidate.Version, candidate.Name, err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// rollbackLastMigration runs the newest migration's reviewed inverse and removes only its ledger row.
func rollbackLastMigration(db *gorm.DB, migrations []migration) error {
	if err := validateMigrationRegistry(migrations); err != nil {
		return err
	}

	return withMigrationLock(db, func() error {
		if err := ensureMigrationLedger(db); err != nil {
			return err
		}
		applied, err := loadAppliedMigrations(db)
		if err != nil {
			return err
		}
		if err := validateAppliedMigrationIdentities(applied, migrations); err != nil {
			return err
		}
		if len(applied) == 0 {
			return nil
		}
		for _, entry := range applied[:len(applied)-1] {
			if entry.State != migrationStateApplied {
				return dirtyMigrationError(entry)
			}
		}

		candidate := migrations[len(applied)-1]
		if candidate.Down == nil {
			return fmt.Errorf("%w: %d %s", ErrMigrationNotReversible, candidate.Version, candidate.Name)
		}
		entry := applied[len(applied)-1]
		if entry.State == migrationStateApplied {
			startedAt := time.Now().UTC()
			result := db.Model(&schemaMigration{}).
				Where("version = ? AND state = ?", candidate.Version, migrationStateApplied).
				Updates(map[string]any{"state": migrationStateRollingBack, "rollback_started_at": startedAt})
			if result.Error != nil {
				return fmt.Errorf("mark migration %d rollback in progress: %w", candidate.Version, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("mark migration %d rollback in progress: expected one row, updated %d", candidate.Version, result.RowsAffected)
			}
		} else if entry.State != migrationStateRollingBack {
			return dirtyMigrationError(entry)
		}

		if err := candidate.Down(db); err != nil {
			return fmt.Errorf("roll back migration %d %s: %w", candidate.Version, candidate.Name, err)
		}
		result := db.Where("version = ? AND state = ?", candidate.Version, migrationStateRollingBack).Delete(&schemaMigration{})
		if result.Error != nil {
			return fmt.Errorf("remove migration %d ledger row: %w", candidate.Version, result.Error)
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("remove migration %d ledger row: expected one rolling-back row, removed %d", candidate.Version, result.RowsAffected)
		}
		return nil
	})
}

// validateMigrationRegistry ensures versions are a contiguous ordered sequence and definitions are complete.
func validateMigrationRegistry(migrations []migration) error {
	for index, candidate := range migrations {
		expectedVersion := uint64(index + 1)
		if candidate.Version != expectedVersion {
			return fmt.Errorf("migration registry version %d: expected %d", candidate.Version, expectedVersion)
		}
		if candidate.Name == "" || candidate.Definition == "" || candidate.Source == "" || candidate.Up == nil {
			return fmt.Errorf("migration %d is incomplete", candidate.Version)
		}
	}
	return nil
}

// ensureMigrationLedger creates the ledger without changing any application table.
func ensureMigrationLedger(db *gorm.DB) error {
	migrator := withMySQLTableOptions(db).Migrator()
	if !migrator.HasTable(&schemaMigration{}) {
		if err := migrator.CreateTable(&schemaMigration{}); err != nil {
			return fmt.Errorf("create migration ledger: %w", err)
		}
		return nil
	}
	for _, field := range []string{"State", "RollbackStartedAt"} {
		if migrator.HasColumn(&schemaMigration{}, field) {
			continue
		}
		if err := migrator.AddColumn(&schemaMigration{}, field); err != nil {
			return fmt.Errorf("upgrade migration ledger column %s: %w", field, err)
		}
	}
	return nil
}

// loadAppliedMigrations reads the complete ordered ledger for prefix validation.
func loadAppliedMigrations(db *gorm.DB) ([]schemaMigration, error) {
	var applied []schemaMigration
	if err := db.Order("version ASC").Find(&applied).Error; err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	return applied, nil
}

// validateAppliedMigrationIdentities rejects unknown, reordered, renamed, or edited migrations before schema work begins.
func validateAppliedMigrationIdentities(applied []schemaMigration, migrations []migration) error {
	if len(applied) > len(migrations) {
		return fmt.Errorf("migration ledger has %d entries but registry has %d", len(applied), len(migrations))
	}
	for index, entry := range applied {
		expected := migrations[index]
		if entry.Version != expected.Version {
			return fmt.Errorf("migration ledger version %d: expected %d", entry.Version, expected.Version)
		}
		if entry.Name != expected.Name {
			return fmt.Errorf("migration %d name %q: expected %q", entry.Version, entry.Name, expected.Name)
		}
		if entry.Checksum != expected.checksum() {
			return fmt.Errorf("%w: migration %d %s", ErrMigrationChecksumMismatch, entry.Version, entry.Name)
		}
	}
	return nil
}

// validateAppliedMigrations also refuses normal forward startup while a rollback requires recovery.
func validateAppliedMigrations(applied []schemaMigration, migrations []migration) error {
	if err := validateAppliedMigrationIdentities(applied, migrations); err != nil {
		return err
	}
	for _, entry := range applied {
		if entry.State != migrationStateApplied {
			return dirtyMigrationError(entry)
		}
	}
	return nil
}

// dirtyMigrationError identifies the exact ledger entry that blocks automatic forward startup.
func dirtyMigrationError(entry schemaMigration) error {
	return fmt.Errorf("%w: migration %d %s is %s; rerun the reviewed down operation", ErrMigrationDirty, entry.Version, entry.Name, entry.State)
}

// withMigrationLock serializes migration runners in MySQL and relies on SQLite's database lock in local tests.
func withMigrationLock(db *gorm.DB, fn func() error) error {
	if db.Dialector.Name() != "mysql" {
		return fn()
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle for migration lock: %w", err)
	}
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		return fmt.Errorf("reserve database connection for migration lock: %w", err)
	}
	defer conn.Close()

	var acquired int
	if err := conn.QueryRowContext(context.Background(), "SELECT GET_LOCK(?, 30)", migrationLockName).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if acquired != 1 {
		return errors.New("acquire migration lock: timed out")
	}
	defer func() {
		var released any
		_ = conn.QueryRowContext(context.Background(), "SELECT RELEASE_LOCK(?)", migrationLockName).Scan(&released)
	}()

	return fn()
}

// withMySQLTableOptions applies the common InnoDB and utf8mb4 options to newly created MySQL tables.
func withMySQLTableOptions(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() != "mysql" {
		return db
	}

	return db.Set("gorm:table_options", mysqlTableOptions)
}
