package quack

import (
	"github.com/quackdiscord/bot/internal/config"
)

// Services is the application service boundary shared by API routes and
// Discord command adapters. Business use cases should be added here
// instead of being implemented directly in handlers.
type Services struct {
	Config    config.Config
	Store     Repository
	Guilds    *GuildService
	Settings  *GuildSettingsService
	Templates *TemplateService
	Cases     *CaseService
	Appeals   *AppealService
	Audits    *AuditService
	Actions   *ActionService
	Evidence  *EvidenceService
	Ops       *OpsService
}

// New assembles storage-backed use cases without live Discord or worker adapters.
func New(store Repository) *Services {
	return NewWithConfigDependencies(config.Default(), store, nil, nil, nil)
}

// NewWithDiscordClient adds live guild authorization to storage-backed use cases.
func NewWithDiscordClient(store Repository, discord DiscordClient) *Services {
	return NewWithConfigDependencies(config.Default(), store, discord, nil, nil)
}

// NewWithConfigDependencies assembles core use cases without starting workers.
// Process composition owns delivery lifetimes; optional modules reuse these services.
func NewWithConfigDependencies(cfg config.Config, store Repository, discord DiscordClient, actions DiscordActionClient, scheduler CaseWorkScheduler) *Services {
	services := &Services{Config: cfg, Store: store}
	services.Appeals = NewAppealService(store)
	services.Guilds = NewGuildService(store, discord)
	services.Settings = NewGuildSettingsService(store)
	if channels, ok := actions.(StaffChannelValidator); ok {
		services.Settings.WithStaffChannelValidator(channels)
	}
	services.Templates = NewTemplateService(store)
	services.Cases = NewCaseService(store, scheduler)
	if evidenceClient, ok := actions.(DiscordEvidenceClient); ok {
		services.Evidence = NewEvidenceService(evidenceClient, store)
		services.Cases.WithEvidenceCapture(services.Evidence)
	} else {
		services.Evidence = NewEvidenceService(nil, store)
	}
	if discord != nil {
		services.Cases.authorizer = services.Guilds
	}
	services.Audits = NewAuditService(store)
	services.Actions = NewActionService(store, actions).WithRecoveryControls(services.Guilds, scheduler).WithDashboardBaseURL(cfg.ApplicationBaseURL)
	services.Ops = NewOpsService(store, scheduler)
	return services
}
