package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

type Service struct {
	registry *modules.Registry
	store    *Store
	auditor  modules.Auditor
	now      func() time.Time
	journal  messageJournalGate
}

func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor) *Service {
	service := &Service{registry: registry, store: store, auditor: auditor, now: func() time.Time { return time.Now().UTC() }}
	service.hydrateJournalThreads()
	return service
}

func (s *Service) Settings(ctx context.Context, actor Actor) (Settings, bool, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.read", "", "denied", ErrPermissionDenied)
		return Settings{}, false, ErrPermissionDenied
	}
	return s.loadSettings(ctx, actor.GuildID)
}

func (s *Service) Status(ctx context.Context, actor Actor) (ModuleStatus, error) {
	if !actor.CanManage && !actor.CanModerate {
		return ModuleStatus{}, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return ModuleStatus{}, err
	}
	open, err := s.store.count(ctx, actor.GuildID, StatusOpen)
	if err != nil {
		return ModuleStatus{}, err
	}
	return ModuleStatus{Enabled: enabled, EntryConfigured: strings.TrimSpace(settings.EntryChannelDiscordID) != "", OpenTickets: open}, nil
}

func (s *Service) UpdateSettings(ctx context.Context, actor Actor, enabled bool, settings Settings) (Settings, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.update", "", "denied", ErrPermissionDenied)
		return Settings{}, ErrPermissionDenied
	}
	if err := validateSettings(settings, enabled); err != nil {
		s.audit(ctx, actor, "ticket.settings.update", "", "failure", err)
		return Settings{}, err
	}
	payload, _ := json.Marshal(settings)
	configuration, err := s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: actor.GuildID, ModuleID: modules.Tickets, Enabled: enabled, ConfigJSON: string(payload)})
	if err != nil {
		s.audit(ctx, actor, "ticket.settings.update", "", "failure", err)
		return Settings{}, err
	}
	s.audit(ctx, actor, "ticket.settings.update", configuration.ID, "success", nil)
	return settings, nil
}

func (s *Service) Open(ctx context.Context, actor Actor, threadDiscordChannelID string) (*Ticket, error) {
	_, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(actor.DiscordUserID) == "" || strings.TrimSpace(threadDiscordChannelID) == "" {
		return nil, errors.New("member and private channel are required")
	}
	ticket, err := s.store.create(ctx, actor.GuildID, actor.DiscordUserID, threadDiscordChannelID, s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.open", "", "failure", err)
		return nil, err
	}
	s.rememberJournalThread(ticket)
	s.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	return ticket, nil
}

// Resolve records the captured transcript for an authorized close. The member
// reservation remains held until the adapter publishes it and deletes the thread.
// Disabling new tickets does not prevent existing closure.
func (s *Service) Resolve(ctx context.Context, actor Actor, ticketID, transcript string) (*Ticket, error) {
	current, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != current.OwnerDiscordUserID && !actor.CanModerate {
		s.audit(ctx, actor, "ticket.close", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, _, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	transcriptRecord := &Transcript{TicketID: ticketID, GuildID: actor.GuildID, Content: transcript, CapturedAt: now, ExpiresAt: now.AddDate(0, 0, settings.TranscriptRetentionDays)}
	ticket, err := s.store.captureClosure(ctx, actor.GuildID, ticketID, actor.DiscordUserID, transcriptRecord, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "failure", err)
		return nil, err
	}
	s.journal.mu.Lock()
	delete(s.journal.known, ticket.ThreadDiscordChannelID)
	s.journal.mu.Unlock()
	s.audit(ctx, actor, "ticket.resolve", ticket.ID, "success", nil)
	return ticket, nil
}

func (s *Service) Reply(ctx context.Context, actor Actor, ticketID, body string) error {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusOpen {
		return ErrInvalidTransition
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return ErrPermissionDenied
	}
	if err := validateReply(body); err != nil {
		return err
	}
	err = s.store.append(ctx, *ticket, EventReplied, actor.DiscordUserID, body, "{}", s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.reply", ticketID, "failure", err)
		return err
	}
	s.audit(ctx, actor, "ticket.reply", ticketID, "success", nil)
	return nil
}

func validateReply(body string) error {
	if len(strings.TrimSpace(body)) == 0 || len(body) > 4000 {
		return errors.New("ticket reply must contain 1 to 4000 characters")
	}
	return nil
}

func (s *Service) Queue(ctx context.Context, actor Actor, status Status, limit int) ([]Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.list(ctx, actor.GuildID, status, limit)
}

// authorizedTicket checks the current guild-scoped record and owner-or-staff
// authority without loading private timeline content. Every access rechecks these
// conditions; callers needing history must explicitly use Detail.
func (s *Service) authorizedTicket(ctx context.Context, actor Actor, ticketID string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return ticket, nil
}

func (s *Service) Detail(ctx context.Context, actor Actor, ticketID string) (*Ticket, []Event, error) {
	ticket, err := s.authorizedTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, nil, err
	}
	events, err := s.store.timeline(ctx, actor.GuildID, ticketID)
	return ticket, events, err
}

func (s *Service) Transcript(ctx context.Context, actor Actor, ticketID string) (*Transcript, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.transcript(ctx, actor.GuildID, ticketID, s.now())
}

// RecordChannelMissing closes no ticket automatically; it records repair-needed state for staff visibility.
func (s *Service) RecordChannelMissing(ctx context.Context, guildID, ticketID, channelID string) error {
	ticket, err := s.store.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	return s.store.append(ctx, *ticket, EventChannelMissing, "quack-system", "Private ticket channel was deleted", fmt.Sprintf(`{"channel_id":%q}`, channelID), s.now())
}

// RepairDeletedEntryChannel disables ticket creation and clears a deleted entry-channel reference.
func (s *Service) RepairDeletedEntryChannel(ctx context.Context, guildID, channelID string) error {
	settings, _, err := s.loadSettings(ctx, guildID)
	if err != nil {
		return err
	}
	if settings.EntryChannelDiscordID != channelID {
		return nil
	}
	settings.EntryChannelDiscordID = ""
	payload, _ := json.Marshal(settings)
	configuration, err := s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: guildID, ModuleID: modules.Tickets, Enabled: false, ConfigJSON: string(payload)})
	if err != nil {
		return err
	}
	s.audit(ctx, Actor{GuildID: guildID, DiscordUserID: "quack-system"}, "ticket.entry_channel_repair", configuration.ID, "success", nil)
	return nil
}

// PurgeExpiredTranscripts enforces the retention boundary without deleting
// ticket timelines.
func (s *Service) PurgeExpiredTranscripts(ctx context.Context) (int64, error) {
	return s.store.purgeExpiredTranscripts(ctx, s.now())
}

func (s *Service) RecordPermissionsRepaired(ctx context.Context, guildID, ticketID string) error {
	ticket, err := s.store.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	return s.store.append(ctx, *ticket, EventPermissionsRepaired, "quack-system", "Private ticket permissions repaired", "{}", s.now())
}

func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	configuration, err := s.registry.Configuration(ctx, guildID, modules.Tickets)
	if err != nil {
		return Settings{}, false, err
	}
	if configuration == nil {
		return Defaults(), false, nil
	}
	settings := Defaults()
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil {
		return Settings{}, false, err
	}
	return settings, configuration.Enabled, nil
}

func validateSettingsJSON(raw string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	return validateSettings(settings, false)
}
func validateSettings(settings Settings, enabled bool) error {
	if enabled && strings.TrimSpace(settings.EntryChannelDiscordID) == "" {
		return errors.New("entry channel is required when tickets are enabled")
	}
	if enabled && strings.TrimSpace(settings.QueueChannelDiscordID) == "" {
		return errors.New("staff queue channel is required when tickets are enabled")
	}
	if settings.TranscriptRetentionDays < 1 || settings.TranscriptRetentionDays > 365 {
		return errors.New("transcript retention must be 1 to 365 days")
	}
	return nil
}

func (s *Service) audit(ctx context.Context, actor Actor, action, resourceID, result string, operationErr error) {
	level := slog.LevelInfo
	if result != "success" {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Module operation completed", "module", "tickets", "guild_id", actor.GuildID, "action", action, "result", result)

	if s == nil || s.auditor == nil {
		return
	}
	reason := ""
	if operationErr != nil {
		reason = operationErr.Error()
	}
	if auditErr := s.auditor.RecordModuleAudit(ctx, modules.AuditEvent{GuildID: actor.GuildID, ActorDiscordUserID: actor.DiscordUserID, Action: action, ResourceType: "ticket", ResourceID: resourceID, Result: result, FailureReason: reason, MetadataJSON: "{}"}); auditErr != nil {
		slog.ErrorContext(ctx, "Module audit could not be recorded", "module", "tickets", "guild_id", actor.GuildID, "action", action)
	}
}

// RecordEntryPanel saves delivery bookkeeping without another staff audit event.
func (s *Service) RecordEntryPanel(ctx context.Context, actor Actor, channelID, messageID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	if channelID == "" || messageID == "" {
		return errors.New("entry panel receipt is incomplete")
	}
	return s.store.saveEntryPanel(ctx, actor.GuildID, channelID, messageID)
}

// ActiveForMember returns only the caller's reserved ticket, including one
// awaiting close cleanup. A provisional opening without a record returns nil.
func (s *Service) ActiveForMember(ctx context.Context, actor Actor) (*Ticket, error) {
	if strings.TrimSpace(actor.GuildID) == "" || strings.TrimSpace(actor.DiscordUserID) == "" {
		return nil, ErrPermissionDenied
	}
	return s.store.activeForMember(ctx, actor.GuildID, actor.DiscordUserID)
}

// ClosurePending reports whether a resolved ticket still holds its owner's slot.
// Only a successful transcript publication and Discord cleanup release it.
func (s *Service) ClosurePending(ctx context.Context, actor Actor, ticketID string) (bool, error) {
	ticket, err := s.authorizedTicket(ctx, actor, ticketID)
	if err != nil {
		return false, err
	}
	if ticket.Status != StatusResolved {
		return false, nil
	}
	active, err := s.store.activeForMember(ctx, ticket.GuildID, ticket.OwnerDiscordUserID)
	return active != nil && active.ID == ticket.ID, err
}

// ThreadRepairPageSize bounds each gateway permission repair database read.
const ThreadRepairPageSize = 100

type ThreadRepairTarget struct {
	ID                     string
	ThreadDiscordChannelID string
	OwnerDiscordUserID     string
}

// DeletedChannelTicketID is a trusted gateway read that includes resolved tickets
// so the adapter can repair their references too; unknown channels give ErrNotFound.
func (s *Service) DeletedChannelTicketID(ctx context.Context, guildID, channelID string) (string, error) {
	return s.store.deletedChannelTicketID(ctx, guildID, channelID)
}

// OpenThreadRepairPage returns at most ThreadRepairPageSize open tickets after the
// exclusive ID cursor. This trusted gateway read deliberately still works when new
// tickets are disabled.
func (s *Service) OpenThreadRepairPage(ctx context.Context, guildID, afterID string) ([]ThreadRepairTarget, error) {
	return s.store.openThreadRepairPage(ctx, guildID, afterID)
}
