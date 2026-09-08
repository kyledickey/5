package store

import (
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

const migration0001Definition = `initial-v5-schema-v1
mode: additive-only
tables: guilds,staff_members,case_templates,case_template_levels,case_template_level_actions,cases,case_action_executions,case_action_attempts,case_events,appeals,appeal_events,tickets,ticket_events,audit_log_entries
existing tables: add missing current-v5 columns and indexes; never rename, drop, or rewrite
clean databases: create the registered current-v5 tables with InnoDB/utf8mb4 on MySQL
rollback: forward-only because dropping the baseline would destroy moderation and audit history`

// migration0001InitialV5Schema adopts an existing AutoMigrate schema or creates the same schema additively on a clean database.
func migration0001InitialV5Schema() migration {
	return migration{
		Version:    1,
		Name:       "initial_v5_schema",
		Definition: migration0001Definition,
		Source:     migration0001Source,
		Up:         applyInitialV5Schema,
		Down:       nil,
	}
}

// applyInitialV5Schema creates missing tables, columns, and indexes without altering or deleting stored data.
func applyInitialV5Schema(db *gorm.DB) error {
	models := migration0001SchemaModels()
	if len(models) == 0 {
		return errorsNoSchemaModels
	}

	for _, model := range models {
		migrator := withMySQLTableOptions(db).Migrator()
		if !migrator.HasTable(model) {
			if err := migrator.CreateTable(model); err != nil {
				return fmt.Errorf("create current v5 table: %w", err)
			}
			continue
		}

		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(model); err != nil {
			return fmt.Errorf("parse current v5 schema model: %w", err)
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName == "" || migrator.HasColumn(model, field.DBName) {
				continue
			}
			if err := migrator.AddColumn(model, field.Name); err != nil {
				return fmt.Errorf("add %s.%s: %w", statement.Schema.Table, field.DBName, err)
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			if migrator.HasIndex(model, index.Name) {
				continue
			}
			if err := migrator.CreateIndex(model, index.Name); err != nil {
				return fmt.Errorf("create index %s on %s: %w", index.Name, statement.Schema.Table, err)
			}
		}
	}
	return nil
}

// errorsNoSchemaModels guards against an accidentally empty baseline registry.
var errorsNoSchemaModels = errors.New("no schema models registered")

// Migration0001ULIDModel is the immutable identifier and timestamp shape used by the baseline migration.
type Migration0001ULIDModel struct {
	ID        string    `gorm:"type:char(26);primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

// migration0001Guild freezes the guild table shape adopted by migration 0001.
type migration0001Guild struct {
	Migration0001ULIDModel
	DiscordGuildID     string `gorm:"size:32;not null;uniqueIndex"`
	Name               string `gorm:"size:191;not null"`
	IconURL            string `gorm:"size:1024"`
	OwnerDiscordUserID string `gorm:"size:32;not null;index"`
	IsActive           bool   `gorm:"not null;default:true;index"`
}

// migration0001StaffMember freezes the staff attribution cache shape adopted by migration 0001.
type migration0001StaffMember struct {
	Migration0001ULIDModel
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_staff_member_guild_user,priority:1;index"`
	DiscordUserID          string     `gorm:"size:32;not null;uniqueIndex:idx_staff_member_guild_user,priority:2;index"`
	LastSeenPermissionBits uint64     `gorm:"type:bigint unsigned;not null;default:0"`
	LastKnownDisplayName   string     `gorm:"size:191"`
	LastActiveAt           *time.Time `gorm:"index"`
}

// migration0001CaseTemplate freezes the template table shape adopted by migration 0001.
type migration0001CaseTemplate struct {
	Migration0001ULIDModel
	GuildID                string         `gorm:"type:char(26);not null;uniqueIndex:idx_case_template_guild_slug,priority:1;index:idx_case_template_guild_enabled,priority:1"`
	Slug                   string         `gorm:"size:64;not null;uniqueIndex:idx_case_template_guild_slug,priority:2"`
	Name                   string         `gorm:"size:191;not null"`
	Description            string         `gorm:"type:text;not null"`
	ReasonTemplate         string         `gorm:"type:text;not null"`
	DefaultSeverity        string         `gorm:"size:32;not null;default:'medium'"`
	Appealable             bool           `gorm:"not null;default:false"`
	Enabled                bool           `gorm:"not null;default:true;index:idx_case_template_guild_enabled,priority:2"`
	Version                uint           `gorm:"not null;default:1"`
	CreatedByDiscordUserID string         `gorm:"size:32;not null"`
	UpdatedByDiscordUserID string         `gorm:"size:32;not null"`
	ArchivedAt             *time.Time     `gorm:"index"`
	DeletedAt              gorm.DeletedAt `gorm:"index"`
}

// migration0001CaseTemplateLevel freezes the escalation level shape adopted by migration 0001.
type migration0001CaseTemplateLevel struct {
	Migration0001ULIDModel
	TemplateID       string `gorm:"type:char(26);not null;uniqueIndex:idx_template_level_position,priority:1;index"`
	Position         int    `gorm:"not null;uniqueIndex:idx_template_level_position,priority:2"`
	Name             string `gorm:"size:191;not null"`
	IsDefault        bool   `gorm:"not null;default:false;index"`
	TriggerCaseCount int    `gorm:"not null;default:0"`
	WindowMinutes    int    `gorm:"not null;default:0"`
	NotifyUser       bool   `gorm:"not null;default:false"`
	NotificationType string `gorm:"size:64"`
	Enabled          bool   `gorm:"not null;default:true"`
}

// migration0001CaseTemplateLevelAction freezes the configured action shape adopted by migration 0001.
type migration0001CaseTemplateLevelAction struct {
	Migration0001ULIDModel
	LevelID          string `gorm:"type:char(26);not null;uniqueIndex:idx_level_action_position,priority:1;index"`
	Position         int    `gorm:"not null;uniqueIndex:idx_level_action_position,priority:2"`
	ActionType       string `gorm:"size:64;not null;index"`
	ConfigJSON       string `gorm:"type:json;not null"`
	NotifyUser       bool   `gorm:"not null;default:false"`
	NotificationType string `gorm:"size:64"`
	ContinueOnError  bool   `gorm:"not null;default:false"`
	MaxRetries       uint8  `gorm:"not null;default:0"`
	RetryBackoffMS   int    `gorm:"not null;default:0"`
	TimeoutMS        int    `gorm:"not null;default:0"`
	IdempotencyScope string `gorm:"size:32;not null;default:'case'"`
	Enabled          bool   `gorm:"not null;default:true"`
}

// migration0001Case freezes the case history shape adopted by migration 0001.
type migration0001Case struct {
	Migration0001ULIDModel
	GuildID                 string     `gorm:"type:char(26);not null;index:idx_case_guild_case_number,priority:1,unique;index:idx_case_guild_target,priority:1;index:idx_case_guild_mod,priority:1;index:idx_case_guild_status,priority:1"`
	CaseNumber              uint64     `gorm:"type:bigint unsigned;not null;index:idx_case_guild_case_number,priority:2,unique"`
	TemplateID              *string    `gorm:"type:char(26);index"`
	TemplateVersion         uint       `gorm:"not null;default:1"`
	TemplateSnapshotJSON    string     `gorm:"type:json;not null"`
	TargetDiscordUserID     string     `gorm:"size:32;not null;index:idx_case_guild_target,priority:2"`
	ModeratorDiscordUserID  string     `gorm:"size:32;not null;index:idx_case_guild_mod,priority:2"`
	Reason                  string     `gorm:"type:text;not null"`
	Severity                string     `gorm:"size:32;not null;default:'medium'"`
	Weight                  int        `gorm:"not null;default:1"`
	Status                  string     `gorm:"size:32;not null;default:'open';index:idx_case_guild_status,priority:2"`
	Source                  string     `gorm:"size:32;not null;default:'discord_command';index"`
	CorrelationID           string     `gorm:"size:128;index"`
	ContextChannelDiscordID string     `gorm:"size:32"`
	ContextMessageDiscordID string     `gorm:"size:32"`
	ContextURL              string     `gorm:"size:1024"`
	ResolvedAt              *time.Time `gorm:"index"`
	ResolvedByDiscordUserID string     `gorm:"size:32"`
	MetadataJSON            string     `gorm:"type:json;not null"`
}

// migration0001CaseActionExecution freezes the durable action shape adopted by migration 0001.
type migration0001CaseActionExecution struct {
	Migration0001ULIDModel
	CaseID             string  `gorm:"type:char(26);not null;index:idx_action_execution_case_position,priority:1;index"`
	TemplateActionID   *string `gorm:"type:char(26);index"`
	Position           int     `gorm:"not null;index:idx_action_execution_case_position,priority:2"`
	ActionType         string  `gorm:"size:64;not null;index"`
	Status             string  `gorm:"size:32;not null;default:'pending';index:idx_action_execution_status_retry,priority:1"`
	IdempotencyKey     string  `gorm:"size:191;not null;uniqueIndex"`
	ConfigSnapshotJSON string  `gorm:"type:json;not null"`
	NotifyUser         bool    `gorm:"not null;default:false"`
	NotificationType   string  `gorm:"size:64"`
	AttemptCount       uint8   `gorm:"not null;default:0"`
	MaxRetries         uint8   `gorm:"not null;default:0"`
	RetryBackoffMS     int     `gorm:"not null;default:0"`
	SafeForRetry       bool    `gorm:"not null;default:true"`
	Irreversible       bool    `gorm:"not null;default:false"`
	LastErrorCode      string  `gorm:"size:64"`
	LastError          string  `gorm:"type:text"`
	StartedAt          *time.Time
	FinishedAt         *time.Time
	NextRetryAt        *time.Time `gorm:"index:idx_action_execution_status_retry,priority:2"`
	CorrelationID      string     `gorm:"size:128;index"`
}

// migration0001CaseActionAttempt freezes the action-attempt history shape adopted by migration 0001.
type migration0001CaseActionAttempt struct {
	Migration0001ULIDModel
	ExecutionID         string    `gorm:"type:char(26);not null;uniqueIndex:idx_action_attempt_execution_number,priority:1;index"`
	AttemptNumber       uint8     `gorm:"not null;uniqueIndex:idx_action_attempt_execution_number,priority:2"`
	Status              string    `gorm:"size:32;not null;index"`
	WorkerID            string    `gorm:"size:64"`
	StartedAt           time.Time `gorm:"not null"`
	FinishedAt          *time.Time
	DurationMS          int64  `gorm:"not null;default:0"`
	ErrorCode           string `gorm:"size:64"`
	ErrorMessage        string `gorm:"type:text"`
	RequestPayloadJSON  string `gorm:"type:json;not null"`
	ResponsePayloadJSON string `gorm:"type:json;not null"`
}

// migration0001CaseEvent freezes the case-event history shape adopted by migration 0001.
type migration0001CaseEvent struct {
	Migration0001ULIDModel
	CaseID             string `gorm:"type:char(26);not null;index:idx_case_event_case_created,priority:1"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	ActorType          string `gorm:"size:32;not null;default:'system'"`
	Visibility         string `gorm:"size:32;not null;default:'staff'"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
	EditedAt           *time.Time
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

// migration0001Appeal freezes the appeal table shape adopted by migration 0001.
type migration0001Appeal struct {
	Migration0001ULIDModel
	GuildID                 string     `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string    `gorm:"type:char(26);index"`
	TargetDiscordUserID     string     `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  string     `gorm:"size:32;not null;default:'pending';index:idx_appeal_guild_status,priority:2"`
	Content                 string     `gorm:"type:text;not null"`
	DecisionReason          string     `gorm:"type:text"`
	ReviewedByDiscordUserID string     `gorm:"size:32"`
	ReviewedAt              *time.Time `gorm:"index"`
	ReviewMessageDiscordID  string     `gorm:"size:32"`
	MetadataJSON            string     `gorm:"type:json;not null"`
}

// migration0001AppealEvent freezes the appeal-event history shape adopted by migration 0001.
type migration0001AppealEvent struct {
	Migration0001ULIDModel
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// migration0001Ticket freezes the existing optional ticket shape adopted by migration 0001.
type migration0001Ticket struct {
	Migration0001ULIDModel
	GuildID                 string     `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string     `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string     `gorm:"size:32;uniqueIndex"`
	Status                  string     `gorm:"size:32;not null;default:'open';index:idx_ticket_guild_status,priority:2"`
	LogMessageDiscordID     string     `gorm:"size:32"`
	ResolvedByDiscordUserID string     `gorm:"size:32"`
	ResolvedAt              *time.Time `gorm:"index"`
	TranscriptURL           string     `gorm:"size:1024"`
	MetadataJSON            string     `gorm:"type:json;not null"`
}

// migration0001TicketEvent freezes the existing ticket-event shape adopted by migration 0001.
type migration0001TicketEvent struct {
	Migration0001ULIDModel
	TicketID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// migration0001AuditLogEntry freezes the audit-history shape adopted by migration 0001.
type migration0001AuditLogEntry struct {
	Migration0001ULIDModel
	GuildID             string `gorm:"type:char(26);not null;index:idx_audit_guild_action,priority:1;index"`
	ActorDiscordUserID  string `gorm:"size:32;index"`
	ActorPermissionBits uint64 `gorm:"type:bigint unsigned;not null;default:0"`
	Source              string `gorm:"size:32;not null;index"`
	Action              string `gorm:"size:96;not null;index:idx_audit_guild_action,priority:2"`
	ResourceType        string `gorm:"size:64;not null;index:idx_audit_resource,priority:1"`
	ResourceID          string `gorm:"size:64;not null;index:idx_audit_resource,priority:2"`
	Result              string `gorm:"size:32;not null;index"`
	FailureReason       string `gorm:"type:text"`
	CorrelationID       string `gorm:"size:128;index"`
	RequestID           string `gorm:"size:128;index"`
	MetadataJSON        string `gorm:"type:json;not null"`
}

// migration0001SchemaModels returns the immutable storage snapshot owned by the baseline migration.
func migration0001SchemaModels() []any {
	return []any{
		&migration0001Guild{}, &migration0001StaffMember{}, &migration0001CaseTemplate{},
		&migration0001CaseTemplateLevel{}, &migration0001CaseTemplateLevelAction{}, &migration0001Case{},
		&migration0001CaseActionExecution{}, &migration0001CaseActionAttempt{}, &migration0001CaseEvent{},
		&migration0001Appeal{}, &migration0001AppealEvent{}, &migration0001Ticket{},
		&migration0001TicketEvent{}, &migration0001AuditLogEntry{}, &auditMirrorDelivery{},
	}
}

// TableName preserves the guilds table name in the baseline snapshot.
func (migration0001Guild) TableName() string { return "guilds" }

// TableName preserves the staff_members table name in the baseline snapshot.
func (migration0001StaffMember) TableName() string { return "staff_members" }

// TableName preserves the case_templates table name in the baseline snapshot.
func (migration0001CaseTemplate) TableName() string { return "case_templates" }

// TableName preserves the case_template_levels table name in the baseline snapshot.
func (migration0001CaseTemplateLevel) TableName() string { return "case_template_levels" }

// TableName preserves the case_template_level_actions table name in the baseline snapshot.
func (migration0001CaseTemplateLevelAction) TableName() string { return "case_template_level_actions" }

// TableName preserves the cases table name in the baseline snapshot.
func (migration0001Case) TableName() string { return "cases" }

// TableName preserves the case_action_executions table name in the baseline snapshot.
func (migration0001CaseActionExecution) TableName() string { return "case_action_executions" }

// TableName preserves the case_action_attempts table name in the baseline snapshot.
func (migration0001CaseActionAttempt) TableName() string { return "case_action_attempts" }

// TableName preserves the case_events table name in the baseline snapshot.
func (migration0001CaseEvent) TableName() string { return "case_events" }

// TableName preserves the appeals table name in the baseline snapshot.
func (migration0001Appeal) TableName() string { return "appeals" }

// TableName preserves the appeal_events table name in the baseline snapshot.
func (migration0001AppealEvent) TableName() string { return "appeal_events" }

// TableName preserves the tickets table name in the baseline snapshot.
func (migration0001Ticket) TableName() string { return "tickets" }

// TableName preserves the ticket_events table name in the baseline snapshot.
func (migration0001TicketEvent) TableName() string { return "ticket_events" }

// TableName preserves the audit_log_entries table name in the baseline snapshot.
func (migration0001AuditLogEntry) TableName() string { return "audit_log_entries" }
