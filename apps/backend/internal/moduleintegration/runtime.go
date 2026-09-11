package moduleintegration

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/store"
	"gorm.io/gorm"
)

const (
	transcriptSweepInterval = time.Hour
	loggingQueueCapacity    = 1000
	loggingQueueWorkers     = 2
	honeypotQueueCapacity   = 256
	honeypotQueueWorkers    = 2
)

// Runtime composes the optional-module services with their Discord adapters and
// owns the process-scoped workers behind them: the general-logging delivery
// queue, the bulk-delete drain, the honeypot worker pool and the hourly
// transcript sweep. Each queue has its own capacity and shutdown so optional
// traffic can never occupy the moderation action queue.
//
// New builds a complete Runtime. Tests assemble partial ones with only the
// module components they exercise, which is why gateway handlers check each
// exported component before routing to it; everything set by New (db, session,
// registry, resolver, services) is assumed present elsewhere.
type Runtime struct {
	honeypotWarningLocks sync.Map
	ticketSetupLocks     sync.Map
	honeypotCounter      *honeypotCounter
	Tickets              *tickets.Service
	TicketDiscord        *tickets.DiscordAdapter
	Logging              *generallogging.Service
	LoggingQueue         *generallogging.DeliveryQueue
	Honeypot             *honeypot.Service
	HoneypotDiscord      *honeypot.DiscordAdapter
	HoneypotRuntime      *honeypot.Runtime

	db                  *gorm.DB
	registry            *modules.Registry
	session             *discordgo.Session
	resolver            guildResolver
	honeypotTemplates   honeypotTemplateValidator
	services            *quack.Services
	cancel              context.CancelFunc
	bulk                chan bulkDeleteEvent
	bulkMu              sync.RWMutex
	bulkWG              sync.WaitGroup
	sweepWG             sync.WaitGroup
	ticketRepairMu      sync.Mutex
	ticketRepairPending map[string]struct{}
	ticketRepairRunning bool
	closed              bool
	closeOnce           sync.Once
	closeDone           chan struct{}
}

// bulkDeleteEvent is one gateway bulk deletion waiting for the cache-aware
// logging drain.
type bulkDeleteEvent struct {
	guildID, channelID string
	messageIDs         []string
}

// New composes the module registry, stores, services, Discord adapters and
// workers on top of the core repositories and services. It validates every
// required dependency here so the methods on Runtime can assume them, and it
// registers the Runtime as the core settings service's enablement validator.
// The returned Runtime must be closed to stop its workers.
func New(ctx context.Context, repositories *store.Store, session *discordgo.Session, services *quack.Services) (*Runtime, error) {
	if repositories == nil || repositories.DB() == nil {
		return nil, errors.New("optional module database is not configured")
	}
	if session == nil {
		return nil, errors.New("optional module Discord session is not configured")
	}
	if services == nil || services.Cases == nil || services.Templates == nil || services.Guilds == nil {
		return nil, errors.New("optional module core services are not configured")
	}

	registry, err := modules.NewRegistry(
		modules.NewSQLSettingsStore(repositories.DB()),
		tickets.Descriptor(),
		generallogging.Descriptor(),
		honeypot.Descriptor(),
	)
	if err != nil {
		return nil, err
	}
	auditor := moduleAuditor{repository: repositories}
	ticketService := tickets.NewService(registry, tickets.NewStore(repositories.DB()), auditor)
	resolver := guildResolver{db: repositories.DB()}
	ticketClient := ticketDiscordClient{session: session, resolver: resolver}
	loggingClient := loggingDiscordClient{session: session, resolver: resolver}
	loggingService := generallogging.NewService(registry, auditor, loggingClient, nil)
	honeypotTemplates := honeypotTemplateValidator{templates: services.Templates}
	honeypotChannels := honeypotChannelValidator{session: session, resolver: resolver}
	honeypotService := honeypot.NewService(
		registry,
		honeypot.NewStore(repositories.DB()),
		auditor,
		honeypotChannels,
		honeypotTemplates,
		honeypotCaseApplier{cases: services.Cases, session: session},
	)
	honeypotDiscord := honeypot.NewDiscordAdapter(honeypotService)
	workerCtx, cancel := context.WithCancel(ctx)

	runtime := &Runtime{
		Tickets:           ticketService,
		TicketDiscord:     tickets.NewDiscordAdapter(ticketService, ticketClient),
		Logging:           loggingService,
		LoggingQueue:      generallogging.NewDeliveryQueue(workerCtx, loggingService, loggingQueueCapacity, loggingQueueWorkers),
		Honeypot:          honeypotService,
		HoneypotDiscord:   honeypotDiscord,
		db:                repositories.DB(),
		registry:          registry,
		session:           session,
		resolver:          resolver,
		honeypotTemplates: honeypotTemplates,
		services:          services,
		cancel:            cancel,
		bulk:              make(chan bulkDeleteEvent, loggingQueueCapacity),
		closeDone:         make(chan struct{}),
	}
	runtime.honeypotCounter = &honeypotCounter{
		templates:   services.Templates,
		session:     session,
		service:     honeypotService,
		resolver:    resolver,
		sharedLocks: &runtime.honeypotWarningLocks,
	}
	runtime.HoneypotRuntime = honeypot.NewRuntime(
		workerCtx,
		honeypotDiscord,
		honeypotQueueCapacity,
		honeypotQueueWorkers,
		runtime.honeypotCounter,
	)
	for range loggingQueueWorkers {
		runtime.bulkWG.Add(1)
		go runtime.runBulkDeletes(workerCtx)
	}
	runtime.sweepWG.Add(1)
	go func() {
		defer runtime.sweepWG.Done()
		runtime.runTranscriptSweep(workerCtx)
	}()
	if services.Settings != nil {
		services.Settings.WithModuleEnablementValidator(runtime)
	}
	return runtime, nil
}

// Close stops the workers and waits for accepted work to drain with no deadline.
func (r *Runtime) Close() {
	_ = r.CloseContext(context.Background()) // best-effort: only a cancelled ctx can fail, and Background never is
}

// CloseContext stops accepting bulk deletions, drains the bulk and honeypot
// workers, closes the logging queue, cancels the transcript sweep and waits
// until everything has exited or ctx expires. It is safe to call repeatedly and
// on a zero-value Runtime; optional workers that were never started are
// skipped. When ctx expires the workers are cancelled and ctx.Err is returned.
func (r *Runtime) CloseContext(ctx context.Context) error {
	r.bulkMu.Lock()
	if r.closeDone == nil {
		r.closeDone = make(chan struct{})
	}
	r.bulkMu.Unlock()
	r.closeOnce.Do(func() {
		go func() {
			r.bulkMu.Lock()
			r.closed = true
			if r.bulk != nil {
				close(r.bulk)
			}
			r.bulkMu.Unlock()
			r.bulkWG.Wait()
			if r.HoneypotRuntime != nil {
				r.HoneypotRuntime.Close()
			}
			if r.LoggingQueue != nil {
				r.LoggingQueue.Close()
			}
			if r.cancel != nil {
				r.cancel()
			}
			r.sweepWG.Wait()
			close(r.closeDone)
		}()
	})
	select {
	case <-r.closeDone:
		return nil
	case <-ctx.Done():
		if r.cancel != nil {
			r.cancel()
		}
		return ctx.Err()
	}
}

// runBulkDeletes drains bulk deletions on a worker that is independent of case
// actions. Delivery failures are already recorded in the logging service's
// status counters.
func (r *Runtime) runBulkDeletes(ctx context.Context) {
	defer r.bulkWG.Done()
	for event := range r.bulk {
		_ = r.Logging.HandleBulkDelete(ctx, event.guildID, event.channelID, event.messageIDs) // best-effort: failures are visible in logging status
	}
}

// submitBulkDelete queues one bulk deletion, dropping it when the queue is
// saturated or closing. Shedding general logging is deliberate: it must never
// delay moderation.
func (r *Runtime) submitBulkDelete(event bulkDeleteEvent) {
	r.bulkMu.RLock()
	defer r.bulkMu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.bulk <- event:
	default:
	}
}

// runTranscriptSweep purges expired private content once at startup and then
// every transcriptSweepInterval until ctx is cancelled. Ticket timelines are
// never touched.
func (r *Runtime) runTranscriptSweep(ctx context.Context) {
	r.purgeTranscripts(ctx)
	ticker := time.NewTicker(transcriptSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.purgeTranscripts(ctx)
		}
	}
}

// purgeTranscripts logs a failed sweep instead of stopping unrelated workers.
func (r *Runtime) purgeTranscripts(ctx context.Context) {
	if _, err := r.Tickets.PurgeExpiredTranscripts(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Failed to purge expired ticket transcripts", "error", err)
	}
}

// moduleAuditor implements modules.Auditor on the core append-only audit store.
type moduleAuditor struct{ repository quack.Repository }

// RecordModuleAudit appends one module audit entry, attributing the source from
// the context (API, Discord or honeypot automation) and rejecting results the
// core audit model does not define. Log payloads never pass through here.
func (a moduleAuditor) RecordModuleAudit(ctx context.Context, event modules.AuditEvent) error {
	result := model.AuditResult(event.Result)
	switch result {
	case model.AuditResultSuccess, model.AuditResultFailure, model.AuditResultDenied:
	default:
		return errors.New("module audit result is invalid")
	}
	requestID, correlationID := quack.TraceIDsFromContext(ctx)
	return a.repository.CreateAuditLogEntry(ctx, &model.AuditLogEntry{
		GuildID:            event.GuildID,
		ActorDiscordUserID: event.ActorDiscordUserID,
		Source:             quack.AuditSourceForModuleAction(ctx, event.Action),
		Action:             event.Action,
		ResourceType:       event.ResourceType,
		ResourceID:         event.ResourceID,
		Result:             result,
		FailureReason:      event.FailureReason,
		RequestID:          requestID,
		CorrelationID:      correlationID,
		MetadataJSON:       event.MetadataJSON,
	})
}

// guildResolver maps between Discord guild snowflakes and Quack's internal
// guild IDs by reading the core guilds table directly. Modules only ever see
// internal IDs.
type guildResolver struct{ db *gorm.DB }

// internalID returns the internal ID of an active guild, or an error when the
// guild is unknown or inactive.
func (r guildResolver) internalID(ctx context.Context, discordGuildID string) (string, error) {
	var guild model.Guild
	result := r.db.WithContext(ctx).
		Where("discord_guild_id = ? AND is_active = ?", discordGuildID, true).
		Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("active guild is not registered")
	}
	return guild.ID, nil
}

// internalIDAny resolves an internal ID regardless of the active flag so
// departure cleanup still works after another handler marked the guild inactive.
func (r guildResolver) internalIDAny(ctx context.Context, discordGuildID string) (string, error) {
	var guild model.Guild
	result := r.db.WithContext(ctx).Where("discord_guild_id = ?", discordGuildID).Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("guild is not registered")
	}
	return guild.ID, nil
}

// discordID returns the Discord guild ID for an active internal guild.
func (r guildResolver) discordID(ctx context.Context, guildID string) (string, error) {
	var guild model.Guild
	result := r.db.WithContext(ctx).Where("id = ? AND is_active = ?", guildID, true).Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("active guild is not registered")
	}
	return guild.DiscordGuildID, nil
}
