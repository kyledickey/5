// Package generallogging implements staff-only Discord event delivery without creating a permanent event archive.
package generallogging

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
)

// EventType is a configured Discord event category.
type EventType string

const (
	MessageEdit       EventType = "message_edit"
	MessageDelete     EventType = "message_delete"
	MessageBulkDelete EventType = "message_bulk_delete"
	MemberJoin        EventType = "member_join"
	MemberLeave       EventType = "member_leave"
	DiscordBan        EventType = "discord_ban"
	DiscordUnban      EventType = "discord_unban"
	GuildChange       EventType = "guild_change"
	ChannelChange     EventType = "channel_change"
)

var (
	// ErrDisabled reports a guild with general logging disabled.
	ErrDisabled = errors.New("general logging module is disabled")
	// ErrPermissionDenied reports a non-manager settings/status request.
	ErrPermissionDenied = errors.New("general logging permission denied")
	// ErrNoDestination reports a configured event without a live route.
	ErrNoDestination = errors.New("general logging destination is not configured")
)

// Settings fixes routing, privacy, cache, formatting, and retry bounds for one guild.
type Settings struct {
	Channels                  map[EventType]string `json:"channels"`
	IncludeMessageContent     bool                 `json:"include_message_content"`
	IncludeAttachmentMetadata bool                 `json:"include_attachment_metadata"`
	IncludeEmbedMetadata      bool                 `json:"include_embed_metadata"`
	CacheEntriesPerGuild      int                  `json:"cache_entries_per_guild"`
	MaxDeliveryAttempts       int                  `json:"max_delivery_attempts"`
}

// Defaults returns bounded, privacy-preserving logging behavior.
func Defaults() Settings {
	return Settings{Channels: map[EventType]string{}, CacheEntriesPerGuild: 1000, MaxDeliveryAttempts: 3}
}

// Actor identifies a current guild manager for configuration operations.
type Actor struct {
	GuildID, DiscordUserID string
	CanManage              bool
}

// AttachmentMetadata is non-content file context allowed by module privacy settings.
type AttachmentMetadata struct {
	DiscordID             string
	Filename, ContentType string
	Size                  int64
}

// Event is an ephemeral Discord event delivered to configured staff channels.
type Event struct {
	BeforeKnown       bool
	BeforeAttachments []AttachmentMetadata
	// SnapshotComplete prevents queued edits from consulting a newer cache version.
	SnapshotComplete                                                bool
	GuildID, ChannelDiscordID, MessageDiscordID, ActorDiscordUserID string
	Type                                                            EventType
	Before, After                                                   string
	Attachments                                                     []AttachmentMetadata
	EmbedTypes                                                      []string
	Metadata                                                        map[string]string
}

// Descriptor exposes logging settings validation to the shared registry.
func Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: modules.GeneralLogging, DisplayName: "General logging", Validate: validateSettingsJSON}
}

func validateSettingsJSON(raw string) error {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return err
	}
	return validateSettings(settings, false)
}
func validateSettings(settings Settings, enabled bool) error {
	if settings.CacheEntriesPerGuild < 1 || settings.CacheEntriesPerGuild > 10000 {
		return errors.New("message cache limit must be 1 to 10000 entries per guild")
	}
	if settings.MaxDeliveryAttempts < 1 || settings.MaxDeliveryAttempts > 5 {
		return errors.New("delivery attempts must be 1 to 5")
	}
	if enabled && len(settings.Channels) == 0 {
		return errors.New("at least one staff-only destination is required")
	}
	for eventType, channelID := range settings.Channels {
		if !validEventType(eventType) || strings.TrimSpace(channelID) == "" {
			return errors.New("invalid logging event route")
		}
	}
	return nil
}

func validEventType(t EventType) bool {
	switch t {
	case MessageEdit, MessageDelete, MessageBulkDelete, MemberJoin, MemberLeave, DiscordBan, DiscordUnban, GuildChange, ChannelChange:
		return true
	}
	return false
}

// RouteAllTo selects one destination for the complete supported event set.
// Native setup enables available message content and metadata to match the
// promised staff log detail. Cache and retry bounds survive channel changes.
func (s Settings) RouteAllTo(channelID string) Settings {
	s.Channels = make(map[EventType]string)
	for _, event := range []EventType{MessageEdit, MessageDelete, MessageBulkDelete, MemberJoin, MemberLeave, DiscordBan, DiscordUnban, GuildChange, ChannelChange} {
		s.Channels[event] = channelID
	}
	s.IncludeMessageContent = true
	s.IncludeAttachmentMetadata = true
	s.IncludeEmbedMetadata = true
	return s
}
