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

// Service owns ticket authorization, lifecycle, privacy, and audit behavior.
type Service struct {
	registry *modules.Registry
	store    *Store
	auditor  modules.Auditor
	now      func() time.Time
}

// NewService constructs the ticket boundary from explicit module dependencies.
func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor) *Service {
	return &Service{registry: registry, store: store, auditor: auditor, now: func() time.Time { return time.Now().UTC() }}
}

// Settings returns one guild's ticket settings to current managers.
func (s *Service) Settings(ctx context.Context, actor Actor) (Settings, bool, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.read", "", "denied", ErrPermissionDenied)
		return Settings{}, false, ErrPermissionDenied
	}
	return s.loadSettings(ctx, actor.GuildID)
}

// Status returns non-content ticket health to current staff.
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

// UpdateSettings validates and replaces one guild's ticket configuration.
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

// Open creates one ticket when the member has no active ticket.
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
	s.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	return ticket, nil
}

// Resolve closes a ticket for its owner or current moderators and preserves the
// captured transcript. Disabling new tickets does not prevent existing closure.
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
	ticket, err := s.store.transition(ctx, actor.GuildID, ticketID, []Status{StatusOpen}, StatusResolved, actor.DiscordUserID, EventResolved, "Ticket resolved", true, transcriptRecord, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.resolve", ticket.ID, "success", nil)
	return ticket, nil
}

// Cancel closes an open ticket at the owner's request or by current staff.
func (s *Service) Cancel(ctx context.Context, actor Actor, ticketID string) (*Ticket, error) {
	return s.cancel(ctx, actor, ticketID, nil)
}

func (s *Service) cancel(ctx context.Context, actor Actor, ticketID string, transcriptContent *string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, _, settingsErr := s.loadSettings(ctx, actor.GuildID)
	now := s.now()
	var transcript *Transcript
	if settingsErr == nil && transcriptContent != nil {
		transcript = &Transcript{TicketID: ticketID, GuildID: actor.GuildID, Content: *transcriptContent, CapturedAt: now, ExpiresAt: now.AddDate(0, 0, settings.TranscriptRetentionDays)}
	}
	ticket, err = s.store.transition(ctx, actor.GuildID, ticketID, []Status{StatusOpen}, StatusCancelled, actor.DiscordUserID, EventCancelled, "Ticket cancelled", false, transcript, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.cancel", ticket.ID, "success", nil)
	return ticket, nil
}

// Reply records a private reply timeline event after owner-or-staff authorization.
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

// Queue lists guild tickets for current staff.
func (s *Service) Queue(ctx context.Context, actor Actor, status Status, limit int) ([]Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.list(ctx, actor.GuildID, status, limit)
}

// Detail returns a private ticket and timeline to its owner or current staff.
func (s *Service) Detail(ctx context.Context, actor Actor, ticketID string) (*Ticket, []Event, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return nil, nil, ErrPermissionDenied
	}
	events, err := s.store.timeline(ctx, actor.GuildID, ticketID)
	return ticket, events, err
}

// Transcript returns retained private content to its owner or current staff.
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

// PurgeExpiredTranscripts enforces the configured upper retention boundary without deleting ticket timelines.
func (s *Service) PurgeExpiredTranscripts(ctx context.Context) (int64, error) {
	return s.store.purgeExpiredTranscripts(ctx, s.now())
}

// RecordPermissionsRepaired appends evidence after the Discord adapter restores the private ACL.
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
