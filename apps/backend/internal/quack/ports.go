package quack

import (
	"context"
	"time"

	"github.com/quackdiscord/bot/internal/quack/model"
)

// Repository is the composition root contract. Individual services accept the
// smaller consumer ports below; adapters implement their combined method set.
type Repository interface {
	StatisticsRepository
	AuditMirrorRepository
	GuildRepository
	SettingsRepository
	TemplateRepository
	CaseRepository
	ActionRepository
	EvidenceRepository
	AuditRepository
	OpsRepository
	SaveOAuthState(context.Context, string, *model.OAuthState, time.Duration) error
	ConsumeOAuthState(context.Context, string) (*model.OAuthState, error)
	SaveSession(context.Context, *model.AuthSession, time.Duration) error
	RefreshSession(context.Context, *model.AuthSession, time.Duration) (bool, error)
	GetSession(context.Context, string) (*model.AuthSession, error)
	DeleteSession(context.Context, string) error
	SkipCaseActions(context.Context, model.SkipCaseActionsParams) error
	ListExecutableCaseIDs(context.Context, int) ([]string, error)
	PingDatabase(context.Context) error
	PingRedis(context.Context) error
	HashGet(context.Context, string, string) ([]byte, error)
	HashSet(context.Context, string, string, []byte) error
}

// CaseWorkScheduler schedules case work while hiding the queue implementation from the application core.
type CaseWorkScheduler interface {
	Submit(context.Context, string) bool
	Stats() QueueStats
}

// QueueStats groups the queue stats state used to keep this package's responsibilities explicit.
type QueueStats struct {
	BufferSize        int    `json:"buffer_size"`
	Workers           int    `json:"workers"`
	Active            bool   `json:"active"`
	QueueSize         int    `json:"queue_size"`
	EnqueuedTotal     uint64 `json:"enqueued_total"`
	DroppedTotal      uint64 `json:"dropped_total"`
	ProcessedTotal    uint64 `json:"processed_total"`
	FailedTotal       uint64 `json:"failed_total"`
	PanickedTotal     uint64 `json:"panicked_total"`
	LastProcessedID   string `json:"last_processed_id,omitempty"`
	LastProcessedType string `json:"last_processed_type,omitempty"`
}

// GuildRepository supplies guild membership, bootstrap, and live staff authorization.
// Consumers depend only on the persistence operations their use cases need.
type GuildRepository interface {
	BootstrapGuild(context.Context, model.BootstrapGuildParams) (*model.BootstrapGuildResult, error)
	ClearGuildChannelReferences(context.Context, string, string, *model.AuditLogEntry) (*model.GuildSettings, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	DeactivateGuild(context.Context, string, *model.AuditLogEntry) (*model.Guild, error)
	GetGuildByDiscordID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	GetStaffMember(context.Context, string, string) (*model.StaffMember, error)
	UpsertGuild(context.Context, model.UpsertGuildParams) (*model.Guild, error)
	UpsertStaffMember(context.Context, model.UpsertStaffMemberParams) (*model.StaffMember, error)
}

// SettingsRepository supplies guild settings and their audit evidence.
// Consumers depend only on the persistence operations their use cases need.
type SettingsRepository interface {
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	UpdateGuildSettings(context.Context, model.UpdateGuildSettingsParams) (*model.GuildSettings, error)
}

// TemplateRepository supplies versioned templates and their audit evidence.
// Consumers depend only on the persistence operations their use cases need.
type TemplateRepository interface {
	ArchiveCaseTemplate(context.Context, string, string, *model.AuditLogEntry) (*model.ExpandedCaseTemplate, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	CreateCaseTemplate(context.Context, model.CreateCaseTemplateParams) (*model.ExpandedCaseTemplate, error)
	GetCaseTemplateBySlug(context.Context, string, string) (*model.CaseTemplate, error)
	GetCaseTemplateExpanded(context.Context, string, string) (*model.ExpandedCaseTemplate, error)
	ListCaseTemplates(context.Context, string) ([]model.ExpandedCaseTemplate, error)
	RestoreCaseTemplate(context.Context, string, string, *model.AuditLogEntry) (*model.ExpandedCaseTemplate, error)
	UpdateCaseTemplate(context.Context, model.UpdateCaseTemplateParams) (*model.ExpandedCaseTemplate, error)
}

// CaseRepository supplies case creation, authorized reads, and immutable corrections.
// Consumers depend only on the persistence operations their use cases need.
type CaseRepository interface {
	UpdateCaseContext(context.Context, string, string, string, *model.AuditLogEntry) (*model.Case, error)
	ListCaseActionsForCases(context.Context, []string) ([]model.CaseActionExecution, error)
	CountTemplateCasesForTarget(context.Context, model.CountTemplateCasesForTargetParams) (int64, error)
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	CreateCase(context.Context, model.CreateCaseParams) (*model.CreatedCase, error)
	GetAppealByCaseID(context.Context, string) (*model.Appeal, error)
	GetCaseByID(context.Context, string) (*model.Case, error)
	GetCaseByIDOrNumber(context.Context, string, string) (*model.Case, error)
	GetCaseByIdempotencyKey(context.Context, string, string) (*model.Case, error)
	GetCaseNotification(context.Context, string) (*model.CaseNotification, error)
	GetCaseTemplateExpanded(context.Context, string, string) (*model.ExpandedCaseTemplate, error)
	GetGuildByID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	ListCaseActionAttempts(context.Context, []string) ([]model.CaseActionAttempt, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	ListCaseEvents(context.Context, string) ([]model.CaseEvent, error)
	ListCaseEvidence(context.Context, string) ([]model.CaseEvidenceSnapshot, []model.CaseEvidenceAttachment, error)
	ListCasesFiltered(context.Context, model.ListCasesParams) (*model.ListCasesResult, error)
	TargetCaseSummary(context.Context, string, string) (*model.TargetCaseSummary, error)
	VoidCase(context.Context, model.VoidCaseParams) (*model.Case, error)
	WithGuildCaseLock(context.Context, string, func(CaseRepository) error) error
}

// ActionRepository supplies leased enforcement, notifications, and staff recovery.
// Consumers depend only on the persistence operations their use cases need.
type ActionRepository interface {
	BeginCaseNotificationDelivery(context.Context, string, string) error
	ClaimCaseNotification(context.Context, model.ClaimCaseNotificationParams) (*model.CaseNotification, error)
	ClaimNextCaseAction(context.Context, model.ClaimCaseActionParams) (*model.ClaimedCaseAction, error)
	CompleteCaseAction(context.Context, model.CompleteCaseActionParams) error
	CompleteCaseNotification(context.Context, model.CompleteCaseNotificationParams) error
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	DismissCaseAction(context.Context, model.DismissCaseActionParams) (*model.CaseActionExecution, error)
	GetCaseActionExecution(context.Context, string, string) (*model.CaseActionExecution, error)
	GetCaseByID(context.Context, string) (*model.Case, error)
	GetCaseByIDOrNumber(context.Context, string, string) (*model.Case, error)
	GetCaseNotification(context.Context, string) (*model.CaseNotification, error)
	GetGuildByID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	ListCaseActionExecutions(context.Context, string) ([]model.CaseActionExecution, error)
	ListFailedCaseActions(context.Context, model.FailedCaseActionFilter) (*model.FailedCaseActionResult, error)
	PrepareCaseNotification(context.Context, string, string, string) error
	QueueCaseReversal(context.Context, model.QueueCaseReversalParams) (*model.CaseActionExecution, error)
	RetryCaseAction(context.Context, model.RetryCaseActionParams) (*model.CaseActionExecution, error)
}

// EvidenceRepository supplies the managed evidence channel reference.
// Consumers depend only on the persistence operations their use cases need.
type EvidenceRepository interface {
	GetGuildByDiscordID(context.Context, string) (*model.Guild, error)
	GetGuildSettings(context.Context, string) (*model.GuildSettings, error)
	UpdateGuildSettings(context.Context, model.UpdateGuildSettingsParams) (*model.GuildSettings, error)
}

// AuditRepository supplies append-only audit writes and filtered reads.
// Consumers depend only on the persistence operations their use cases need.
type AuditRepository interface {
	CreateAuditLogEntry(context.Context, *model.AuditLogEntry) error
	ListAuditLogEntriesFiltered(context.Context, model.ListAuditLogEntriesParams) (*model.ListAuditLogEntriesResult, error)
}

// OpsRepository supplies durable worker health snapshots.
// Consumers depend only on the persistence operations their use cases need.
type OpsRepository interface {
	ActionQueueSnapshot(context.Context, string, int) (*model.ActionQueueSnapshot, error)
}
