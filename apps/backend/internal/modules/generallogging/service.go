package generallogging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

var secretPattern = regexp.MustCompile(`(?i)(bot\s+[A-Za-z0-9._-]{20,}|https://(?:discord(?:app)?\.com/api/)?webhooks/[^\s]+|(?:token|secret|authorization)\s*[:=]\s*[^\s]+)`)

type DeliveryClient interface {
	SendStaffLog(context.Context, string, string, string) error
	ValidateStaffOnlyChannel(context.Context, string, string) error
}

type RetryAfterError interface {
	error
	RetryAfter() time.Duration
}

// Status is in-memory delivery health; it is not an event archive.
type Status struct {
	Delivered      uint64     `json:"delivered"`
	Failed         uint64     `json:"failed"`
	LastError      string     `json:"last_error,omitempty"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
	CachedMessages int        `json:"cached_messages"`
}

type Service struct {
	registry *modules.Registry
	auditor  modules.Auditor
	client   DeliveryClient
	cache    *MessageCache
	sleep    func(context.Context, time.Duration) error
	mu       sync.Mutex
	status   map[string]Status
}

func NewService(registry *modules.Registry, auditor modules.Auditor, client DeliveryClient, cache *MessageCache) *Service {
	if cache == nil {
		cache = NewMessageCache(1000)
	}
	return &Service{registry: registry, auditor: auditor, client: client, cache: cache, sleep: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}, status: map[string]Status{}}
}

func (s *Service) Settings(ctx context.Context, actor Actor) (Settings, bool, Status, error) {
	if !actor.CanManage {
		return Settings{}, false, Status{}, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	return settings, enabled, s.Status(actor.GuildID), err
}

// UpdateSettings validates every staff-only destination before replacing this
// module's configuration.
func (s *Service) UpdateSettings(ctx context.Context, actor Actor, enabled bool, settings Settings) (Settings, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "general_logging.settings.update", "denied", ErrPermissionDenied)
		return Settings{}, ErrPermissionDenied
	}
	if err := validateSettings(settings, enabled); err != nil {
		return Settings{}, err
	}
	if s.client == nil {
		return Settings{}, errors.New("general logging Discord client is not configured")
	}
	for _, channelID := range uniqueChannels(settings.Channels) {
		if err := s.client.ValidateStaffOnlyChannel(ctx, actor.GuildID, channelID); err != nil {
			s.audit(ctx, actor, "general_logging.settings.update", "failure", err)
			return Settings{}, err
		}
	}
	payload, _ := json.Marshal(settings)
	_, err := s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: actor.GuildID, ModuleID: modules.GeneralLogging, Enabled: enabled, ConfigJSON: string(payload)})
	if err != nil {
		return Settings{}, err
	}
	s.cache.SetGuildLimit(actor.GuildID, settings.CacheEntriesPerGuild)
	s.audit(ctx, actor, "general_logging.settings.update", "success", nil)
	return settings, nil
}

func (s *Service) CacheMessage(ctx context.Context, message CachedMessage) error {
	settings, enabled, err := s.loadSettings(ctx, message.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	s.cache.SetGuildLimit(message.GuildID, settings.CacheEntriesPerGuild)
	s.cache.Put(message)
	return nil
}

func (s *Service) Handle(ctx context.Context, event Event) error {
	if s == nil || s.client == nil {
		return errors.New("general logging service is not configured")
	}
	settings, enabled, err := s.loadSettings(ctx, event.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	channelID := settings.Channels[event.Type]
	if channelID == "" {
		return ErrNoDestination
	}
	s.enrichFromCache(&event)
	payload := formatEvent(event, settings)
	var last error
	for attempt := 1; attempt <= settings.MaxDeliveryAttempts; attempt++ {
		last = s.client.SendStaffLog(ctx, event.GuildID, channelID, payload)
		if last == nil {
			if event.Type == MessageDelete {
				s.cache.Delete(event.GuildID, event.MessageDiscordID)
			}
			s.recordSuccess(event.GuildID)
			return nil
		}
		if attempt < settings.MaxDeliveryAttempts {
			delay := time.Duration(attempt) * 100 * time.Millisecond
			var retry RetryAfterError
			if errors.As(last, &retry) && retry.RetryAfter() > delay {
				delay = retry.RetryAfter()
			}
			if err := s.sleep(ctx, delay); err != nil {
				last = err
				break
			}
		}
	}
	s.recordFailure(event.GuildID, last)
	return fmt.Errorf("deliver general log after %d attempts: %w", settings.MaxDeliveryAttempts, last)
}

func (s *Service) HandleBulkDelete(ctx context.Context, guildID, channelID string, messageIDs []string) error {
	if len(messageIDs) > 100 {
		return errors.New("bulk logging accepts at most 100 Discord message IDs")
	}
	var messages []CachedMessage
	parts := make([]string, 0, len(messageIDs))
	var attachments []AttachmentMetadata
	var embedTypes []string
	for _, id := range messageIDs {
		if cached, ok := s.cache.Get(guildID, id); ok {
			messages = append(messages, cached)
			parts = append(parts, cached.Content)
			attachments = append(attachments, cached.Attachments...)
			embedTypes = append(embedTypes, cached.EmbedTypes...)
		}
	}
	err := s.Handle(ctx, Event{BulkMessages: messages, GuildID: guildID, ChannelDiscordID: channelID, Type: MessageBulkDelete, Before: strings.Join(parts, "\n---\n"), Attachments: attachments, EmbedTypes: embedTypes, Metadata: map[string]string{"message_count": fmt.Sprint(len(messageIDs)), "cached_count": fmt.Sprint(len(parts))}})
	if err != nil {
		return err
	}
	for _, id := range messageIDs {
		s.cache.Delete(guildID, id)
	}
	return nil
}

// RepairDeletedChannel removes every route to a deleted channel and disables the
// module when no routes remain.
func (s *Service) RepairDeletedChannel(ctx context.Context, actor Actor, channelID string) (Settings, bool, error) {
	if !actor.CanManage {
		return Settings{}, false, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return Settings{}, false, err
	}
	for eventType, destination := range settings.Channels {
		if destination == channelID {
			delete(settings.Channels, eventType)
		}
	}
	if len(settings.Channels) == 0 {
		enabled = false
	}
	updated, err := s.UpdateSettings(ctx, actor, enabled, settings)
	if err != nil {
		return Settings{}, false, err
	}
	s.audit(ctx, actor, "general_logging.channel_repair", "success", nil)
	return updated, enabled, nil
}

func (s *Service) Status(guildID string) Status {
	s.mu.Lock()
	status := s.status[guildID]
	s.mu.Unlock()
	status.CachedMessages = s.cache.Len(guildID)
	return status
}

func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	configuration, err := s.registry.Configuration(ctx, guildID, modules.GeneralLogging)
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
	if settings.Channels == nil {
		settings.Channels = map[EventType]string{}
	}
	return settings, configuration.Enabled, nil
}
func (s *Service) enrichFromCache(event *Event) {
	if event.SnapshotComplete || event.Type != MessageEdit && event.Type != MessageDelete {
		return
	}
	cached, ok := s.cache.Get(event.GuildID, event.MessageDiscordID)
	if !ok {
		return
	}
	if event.ActorDiscordUserID == "" {
		event.ActorDiscordUserID = cached.AuthorDiscordUserID
	}
	if event.Before == "" {
		event.Before = cached.Content
	}
	if len(event.Attachments) == 0 {
		event.Attachments = cached.Attachments
	}
	if len(event.EmbedTypes) == 0 {
		event.EmbedTypes = cached.EmbedTypes
	}
}
func formatEvent(event Event, settings Settings) string {
	redact := func(value string) string { return secretPattern.ReplaceAllString(value, "[REDACTED]") }
	payload := map[string]any{"event": event.Type, "channel_id": event.ChannelDiscordID, "message_id": event.MessageDiscordID, "actor_id": event.ActorDiscordUserID}
	if settings.IncludeMessageContent {
		payload["before"] = redact(event.Before)
		if event.SnapshotComplete {
			payload["before_known"] = event.BeforeKnown
		}
		payload["after"] = redact(event.After)
	}
	if settings.IncludeAttachmentMetadata {
		payload["attachments"] = event.Attachments
		payload["before_attachments"] = event.BeforeAttachments
	}
	if settings.IncludeEmbedMetadata {
		payload["embed_types"] = event.EmbedTypes
	}
	if len(event.BulkMessages) > 0 {
		records := make([]map[string]any, 0, len(event.BulkMessages))
		for _, message := range event.BulkMessages {
			record := map[string]any{"message_id": message.MessageDiscordID, "actor_id": message.AuthorDiscordUserID}
			if settings.IncludeMessageContent {
				record["content"] = redact(message.Content)
			}
			if settings.IncludeAttachmentMetadata {
				record["attachments"] = message.Attachments
			}
			if settings.IncludeEmbedMetadata {
				record["embed_types"] = message.EmbedTypes
			}
			records = append(records, record)
		}
		payload["messages"] = records
	}
	metadata := map[string]string{}
	for key, value := range event.Metadata {
		metadata[key] = redact(value)
	}
	payload["metadata"] = metadata
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}
func uniqueChannels(routes map[EventType]string) []string {
	set := map[string]struct{}{}
	for _, channelID := range routes {
		set[channelID] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func (s *Service) recordSuccess(guildID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[guildID]
	status.Delivered++
	status.LastError = ""
	status.LastFailureAt = nil
	s.status[guildID] = status
}
func (s *Service) recordFailure(guildID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[guildID]
	status.Failed++
	status.LastError = err.Error()
	now := time.Now().UTC()
	status.LastFailureAt = &now
	s.status[guildID] = status
}
func (s *Service) audit(ctx context.Context, actor Actor, action, result string, err error) {
	level := slog.LevelInfo
	if result != "success" {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Module operation completed", "module", "generallogging", "guild_id", actor.GuildID, "action", action, "result", result)

	if s.auditor == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if auditErr := s.auditor.RecordModuleAudit(ctx, modules.AuditEvent{GuildID: actor.GuildID, ActorDiscordUserID: actor.DiscordUserID, Action: action, ResourceType: "general_logging_settings", Result: result, FailureReason: reason, MetadataJSON: "{}"}); auditErr != nil {
		slog.ErrorContext(ctx, "Module audit could not be recorded", "module", "generallogging", "guild_id", actor.GuildID, "action", action)
	}
}

// PrepareMessageEdit captures an immutable edit event before updating the cache.
// A supplied before snapshot comes from Discord; otherwise the bounded cache is used.
func (s *Service) PrepareMessageEdit(ctx context.Context, current CachedMessage, before *CachedMessage) (*Event, error) {
	settings, enabled, err := s.loadSettings(ctx, current.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	s.cache.SetGuildLimit(current.GuildID, settings.CacheEntriesPerGuild)
	previous, known := s.cache.Replace(current)
	if before != nil {
		previous = *before
		known = true
	}
	if known && previous.Content == current.Content && slices.EqualFunc(previous.Attachments, current.Attachments, sameAttachmentContent) {
		return nil, nil
	}
	actor := current.AuthorDiscordUserID
	if actor == "" {
		actor = previous.AuthorDiscordUserID
	}
	return &Event{BeforeKnown: known, BeforeAttachments: previous.Attachments, GuildID: current.GuildID, ChannelDiscordID: current.ChannelDiscordID, MessageDiscordID: current.MessageDiscordID, ActorDiscordUserID: actor, Type: MessageEdit, Before: previous.Content, After: current.Content, Attachments: current.Attachments, EmbedTypes: current.EmbedTypes, SnapshotComplete: true}, nil
}

// sameAttachmentContent ignores the expiring download coordinate when deciding
// whether a gateway update changed a file. Cache replacement still happens first
// so a later deletion log uses the newest available signed URL.
func sameAttachmentContent(left, right AttachmentMetadata) bool {
	left.URL, right.URL = "", ""
	return left == right
}
