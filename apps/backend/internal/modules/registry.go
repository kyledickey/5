// Package modules defines the isolation boundary shared by optional Quack modules.
package modules

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

// ID is the stable identifier used for independently configured modules.
type ID string

const (
	// Tickets identifies the private support-ticket module.
	Tickets ID = "tickets"
	// GeneralLogging identifies the staff-only Discord event logging module.
	GeneralLogging ID = "general_logging"
	// Honeypots identifies the separately implemented automated moderation module.
	Honeypots ID = "honeypots"
)

// Configuration is one guild's enablement flag plus the module's opaque settings
// JSON, which the registry never interprets.
type Configuration struct {
	ID         string    `gorm:"type:char(26);primaryKey" json:"id"`
	GuildID    string    `gorm:"type:char(26);not null;uniqueIndex:idx_module_configuration,priority:1" json:"guild_id"`
	ModuleID   ID        `gorm:"size:64;not null;uniqueIndex:idx_module_configuration,priority:2" json:"module_id"`
	Enabled    bool      `gorm:"not null;default:false" json:"enabled"`
	ConfigJSON string    `gorm:"type:json;not null" json:"-"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

// TableName keeps optional-module configuration out of core guild settings.
func (Configuration) TableName() string { return "module_configurations" }

type SettingsStore interface {
	GetModuleConfiguration(context.Context, string, ID) (*Configuration, error)
	PutModuleConfiguration(context.Context, Configuration) (*Configuration, error)
}

type Descriptor struct {
	ID          ID
	DisplayName string
	Validate    func(string) error
}

type Registry struct {
	store       SettingsStore
	descriptors map[ID]Descriptor
}

// NewRegistry rejects duplicate module ownership.
func NewRegistry(store SettingsStore, descriptors ...Descriptor) (*Registry, error) {
	if store == nil {
		return nil, errors.New("module settings store is required")
	}
	r := &Registry{store: store, descriptors: make(map[ID]Descriptor, len(descriptors))}
	for _, descriptor := range descriptors {
		if strings.TrimSpace(string(descriptor.ID)) == "" || strings.TrimSpace(descriptor.DisplayName) == "" {
			return nil, errors.New("module descriptor id and display name are required")
		}
		if _, exists := r.descriptors[descriptor.ID]; exists {
			return nil, fmt.Errorf("module %q is already registered", descriptor.ID)
		}
		r.descriptors[descriptor.ID] = descriptor
	}
	return r, nil
}

func (r *Registry) IDs() []ID {
	ids := make([]ID, 0, len(r.descriptors))
	for id := range r.descriptors {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// Configuration returns one guild/module envelope and never falls back across
// guilds. A guild that has never configured the module yields (nil, nil).
func (r *Registry) Configuration(ctx context.Context, guildID string, moduleID ID) (*Configuration, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("module registry is not configured")
	}
	if _, ok := r.descriptors[moduleID]; !ok {
		return nil, fmt.Errorf("unknown module %q", moduleID)
	}
	return r.store.GetModuleConfiguration(ctx, strings.TrimSpace(guildID), moduleID)
}

// SetConfiguration validates and replaces only the addressed guild/module envelope.
func (r *Registry) SetConfiguration(ctx context.Context, configuration Configuration) (*Configuration, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("module registry is not configured")
	}
	descriptor, ok := r.descriptors[configuration.ModuleID]
	if !ok {
		return nil, fmt.Errorf("unknown module %q", configuration.ModuleID)
	}
	configuration.GuildID = strings.TrimSpace(configuration.GuildID)
	if configuration.GuildID == "" {
		return nil, errors.New("guild id is required")
	}
	if strings.TrimSpace(configuration.ConfigJSON) == "" {
		configuration.ConfigJSON = "{}"
	}
	if descriptor.Validate != nil {
		if err := descriptor.Validate(configuration.ConfigJSON); err != nil {
			return nil, fmt.Errorf("validate %s configuration: %w", configuration.ModuleID, err)
		}
	}
	return r.store.PutModuleConfiguration(ctx, configuration)
}

type SQLSettingsStore struct{ db *gorm.DB }

func NewSQLSettingsStore(db *gorm.DB) *SQLSettingsStore { return &SQLSettingsStore{db: db} }

func (s *SQLSettingsStore) GetModuleConfiguration(ctx context.Context, guildID string, moduleID ID) (*Configuration, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("module database is not connected")
	}
	var configuration Configuration
	result := s.db.WithContext(ctx).Where("guild_id = ? AND module_id = ?", guildID, moduleID).Limit(1).Find(&configuration)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &configuration, nil
}

// PutModuleConfiguration upserts one guild/module row, preserving the existing
// row's ID and CreatedAt so audit references stay stable across edits.
func (s *SQLSettingsStore) PutModuleConfiguration(ctx context.Context, configuration Configuration) (*Configuration, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("module database is not connected")
	}
	now := time.Now().UTC()
	var existing Configuration
	query := s.db.WithContext(ctx).Where("guild_id = ? AND module_id = ?", configuration.GuildID, configuration.ModuleID).Limit(1).Find(&existing)
	if query.Error != nil {
		return nil, query.Error
	}
	if query.RowsAffected == 0 {
		configuration.ID = ulid.Make().String()
		configuration.CreatedAt = now
	} else {
		configuration.ID = existing.ID
		configuration.CreatedAt = existing.CreatedAt
	}
	configuration.UpdatedAt = now
	if err := s.db.WithContext(ctx).Save(&configuration).Error; err != nil {
		return nil, err
	}
	return &configuration, nil
}

// AuditEvent is one optional-module operation outcome for the core audit sink.
type AuditEvent struct {
	GuildID, ActorDiscordUserID, Action, ResourceType, ResourceID, Result, FailureReason, MetadataJSON string
}

type Auditor interface {
	RecordModuleAudit(context.Context, AuditEvent) error
}

// ImportRecord makes each legacy source identity idempotent within one guild and module.
type ImportRecord struct {
	ID        string    `gorm:"type:char(26);primaryKey"`
	GuildID   string    `gorm:"type:char(26);not null;uniqueIndex:idx_module_import,priority:1"`
	ModuleID  ID        `gorm:"size:64;not null;uniqueIndex:idx_module_import,priority:2"`
	SourceID  string    `gorm:"size:191;not null;uniqueIndex:idx_module_import,priority:3"`
	TargetID  string    `gorm:"size:191;not null"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName provides one cross-module ledger without mixing imported data into
// core history.
func (ImportRecord) TableName() string { return "module_import_records" }

// SchemaTypes returns the shared optional-module tables for schema creation.
func SchemaTypes() []any { return []any{&Configuration{}, &ImportRecord{}} }
