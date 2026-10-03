// Package quack holds Quack's moderation use cases: the services that HTTP
// routes, Discord command handlers, and background workers call to create and
// read cases, evidence, appeals, templates, guild settings, audit history, and
// operational status.
//
// Everything here is transport-neutral. Each service declares the narrow
// repository port it needs next to itself and returns plain response structs,
// so the package must not import discordgo, gorm, or gin. internal/store
// implements the ports; internal/discordbot and internal/httpapi map the
// responses. Domain records and storage parameter structs live in the model
// subpackage.
package quack

import (
	"context"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack/model"
)

// Services is the application service boundary shared by API routes and
// Discord command adapters. Business use cases should be added here
// instead of being implemented directly in handlers.
type Services struct {
	Config     config.Config
	Store      Repository
	Guilds     *GuildService
	Settings   *GuildSettingsService
	Templates  *TemplateService
	Cases      *CaseService
	Appeals    *AppealService
	Audits     *AuditService
	Statistics *StaffStatisticsService
	Actions    *ActionService
	Evidence   *EvidenceService
	Ops        *OpsService
}

// New assembles every service over one repository. discord, actions, and
// scheduler may be nil; services then skip the live Discord checks, evidence
// capture, channel validation, and queue statistics those adapters provide. It
// starts no workers: process composition owns delivery lifetimes.
//
// actions carries the whole Discord write surface behind its minimum contract:
// production passes one adapter that also validates staff channels and captures
// evidence, so those two richer capabilities are recovered by type assertion
// rather than by repeating the same value in three more parameters.
func New(
	cfg config.Config, store Repository, discord DiscordClient, actions DiscordActionClient, scheduler CaseWorkScheduler,
) *Services {
	services := &Services{Config: cfg, Store: store}
	services.Appeals = NewAppealService(store)
	services.Guilds = NewGuildService(store, discord)
	services.Settings = NewGuildSettingsService(store)
	if channels, ok := actions.(StaffChannelValidator); ok {
		services.Settings.channels = channels
	}
	services.Templates = NewTemplateService(store)
	services.Cases = NewCaseService(store, scheduler)
	if evidenceClient, ok := actions.(DiscordEvidenceClient); ok {
		services.Evidence = NewEvidenceService(evidenceClient, store)
		services.Cases.evidence = services.Evidence
	} else {
		services.Evidence = NewEvidenceService(nil, store)
	}
	if discord != nil {
		services.Cases.authorizer = services.Guilds
	}
	services.Audits = NewAuditService(store)
	services.Statistics = NewStaffStatisticsService(store)
	services.Actions = NewActionService(store, actions, services.Guilds, scheduler, cfg.ApplicationBaseURL)
	services.Ops = NewOpsService(store, scheduler)
	return services
}

// Repository is the composition root contract: one adapter satisfying every
// service's narrow port plus the session, OAuth-state, and liveness operations
// the HTTP layer reads straight off Services.Store.
type Repository interface {
	AppealRepository
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
	PingDatabase(context.Context) error
	PingRedis(context.Context) error
}

// CaseWorkScheduler hands case IDs to the in-process worker pool. Submit
// returns false when the work was not accepted (queue full or stopped); that is
// only a latency loss because persisted action rows remain the work queue.
type CaseWorkScheduler interface {
	Submit(context.Context, string) bool
	Stats() QueueStats
}

// QueueStats is a point-in-time view of the in-process case work queue as
// reported by CaseWorkScheduler.Stats. Totals are cumulative since process start.
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
