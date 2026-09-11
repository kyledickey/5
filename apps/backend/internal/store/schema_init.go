package store

import (
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// ErrSchemaAdoptionRequired reports an existing database that needs explicit
// operator adoption before normal startup may reconcile its schema.
var ErrSchemaAdoptionRequired = errors.New("schema adoption required")

// currentSchema identifies databases created directly from the pre-release model
// definitions. It is separate from the historical migration checksum ledger.
type currentSchema struct {
	ID uint `gorm:"primaryKey;autoIncrement:false"`
	// AuditMirrorQueueReady records the completed idempotent historical backfill.
	AuditMirrorQueueReady bool `gorm:"not null;default:false"`
}

// TableName identifies the direct-initialization marker.
func (currentSchema) TableName() string { return "quack_current_schema" }

// InitializeSchema creates the current pre-release schema on an empty database,
// or reconciles a database already initialized this way. It never silently adopts
// a historical database; callers must explicitly preserve or reset that data.
func (s *Store) InitializeSchema() error {
	return withMigrationLock(s.db, func() error {
		db := withMySQLTableOptions(s.db)
		if !db.Migrator().HasTable(&currentSchema{}) {
			tables, err := db.Migrator().GetTables()
			if err != nil {
				return fmt.Errorf("inspect existing tables: %w", err)
			}
			if len(tables) != 0 {
				return fmt.Errorf("%w: existing tables were left unchanged; run quack-migrate adopt for a fully migrated pre-release database, or explicitly run quack-migrate legacy-up before adopt for older history", ErrSchemaAdoptionRequired)
			}
			// Create the marker first so interrupted MySQL DDL can be retried.
			if err := db.Migrator().CreateTable(&currentSchema{}); err != nil {
				return fmt.Errorf("create current schema marker: %w", err)
			}
		}
		// A ready queue cannot be reconstructed from history without resending
		// completed events. Refuse reconciliation if its delivery ledger is lost.
		if db.Migrator().HasColumn(&currentSchema{}, "AuditMirrorQueueReady") && !db.Migrator().HasTable(&auditMirrorDelivery{}) {
			var ready int64
			if err := s.db.Model(&currentSchema{}).Where("audit_mirror_queue_ready = ?", true).Count(&ready).Error; err != nil {
				return fmt.Errorf("inspect audit mirror queue readiness: %w", err)
			}
			if ready != 0 {
				return errors.New("audit mirror delivery ledger is missing from a ready schema; restore the ledger before startup")
			}
		}
		// Upgrade only the readiness column; the marker's historical primary key
		// is already established and must not be rewritten during reconciliation.
		if !db.Migrator().HasColumn(&currentSchema{}, "AuditMirrorQueueReady") {
			if err := db.Migrator().AddColumn(&currentSchema{}, "AuditMirrorQueueReady"); err != nil {
				return fmt.Errorf("initialize audit mirror queue marker: %w", err)
			}
		}
		models := append(schemaModels(),
			&GuildSettingsRecord{},
			&GuildAppealSettingsRecord{},
			&AppealNotificationRecord{},
			&V4ImportBatchRecord{},
			&V4ImportSourceRecord{},
			&auditMirrorDelivery{},
			&ActionManualReviewRecord{},
			&model.CasePublication{},
		)
		if err := db.AutoMigrate(models...); err != nil {
			return fmt.Errorf("initialize core schema: %w", err)
		}
		for _, module := range []modules.Migration{modules.RegistryMigration(), tickets.Migration(), honeypot.Migration()} {
			if err := module.Apply(db); err != nil {
				return fmt.Errorf("initialize %s schema: %w", module.Name, err)
			}
		}
		if err := applyCurrentStorageConstraints(db); err != nil {
			return fmt.Errorf("initialize storage constraints: %w", err)
		}
		if err := s.db.FirstOrCreate(&currentSchema{ID: 1}).Error; err != nil {
			return fmt.Errorf("record current schema initialization: %w", err)
		}
		return initializeAuditMirrorQueue(s.db)
	})
}
