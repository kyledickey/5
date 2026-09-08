// Package tickets implements the optional private Discord support-ticket module.
package tickets

import (
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// Status is the durable ticket lifecycle state.
type Status string

const (
	// StatusOpen accepts member and staff replies.
	StatusOpen Status = "open"
	// StatusResolved is a completed ticket; closed tickets cannot be reopened.
	StatusResolved Status = "resolved"
	// StatusCancelled is an owner- or staff-cancelled ticket.
	StatusCancelled Status = "cancelled"
)

// EventType identifies immutable ticket timeline entries.
type EventType string

const (
	EventOpened              EventType = "opened"
	EventReplied             EventType = "replied"
	EventResolved            EventType = "resolved"
	EventCancelled           EventType = "cancelled"
	EventReopened            EventType = "reopened"
	EventChannelMissing      EventType = "channel_missing"
	EventPermissionsRepaired EventType = "permissions_repaired"
)

var (
	// ErrDisabled reports that the guild has not enabled tickets.
	ErrDisabled = errors.New("ticket module is disabled")
	// ErrPermissionDenied reports an unauthorized ticket operation.
	ErrPermissionDenied = errors.New("ticket permission denied")
	// ErrNotFound reports a ticket outside the requested guild or absent ticket.
	ErrNotFound = errors.New("ticket not found")
	// ErrDuplicateOpen reports the one-open-ticket-per-member invariant.
	ErrDuplicateOpen = errors.New("member already has an open ticket")
	// ErrRateLimited reports the configured daily open threshold.
	ErrRateLimited = errors.New("ticket open rate limit exceeded")
	// ErrInvalidTransition reports a lifecycle operation that is not valid from the current state.
	ErrInvalidTransition = errors.New("invalid ticket transition")
)

// Settings fixes the module's Discord, privacy, retention, and abuse-control policy for one guild.
type Settings struct {
	QueueChannelDiscordID   string `json:"queue_channel_discord_id"`
	EntryChannelDiscordID   string `json:"entry_channel_discord_id"`
	TranscriptRetentionDays int    `json:"transcript_retention_days"`
}

// Defaults returns privacy-preserving settings for a newly enabled guild.
func Defaults() Settings {
	return Settings{TranscriptRetentionDays: 90}
}

// Actor is the transport-neutral identity and current Discord authority for a ticket operation.
type Actor struct {
	GuildID, DiscordUserID string
	CanManage, CanModerate bool
}

// Ticket is the module-owned ticket state; it does not reference cases or appeals.
type Ticket struct {
	LogMessageDiscordID     string     `json:"log_message_discord_id,omitempty"`
	LogChannelDiscordID     string     `json:"log_channel_discord_id,omitempty"`
	TranscriptURL           string     `json:"transcript_url,omitempty"`
	ID                      string     `json:"id"`
	GuildID                 string     `json:"guild_id"`
	OwnerDiscordUserID      string     `json:"owner_discord_user_id"`
	ThreadDiscordChannelID  string     `json:"thread_discord_channel_id"`
	Status                  Status     `json:"status"`
	ResolvedByDiscordUserID string     `json:"resolved_by_discord_user_id,omitempty"`
	ResolvedAt              *time.Time `json:"resolved_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// Event is an immutable ticket timeline entry.
type Event struct {
	ID                 string    `json:"id"`
	TicketID           string    `json:"ticket_id"`
	GuildID            string    `json:"guild_id"`
	Type               EventType `json:"type"`
	ActorDiscordUserID string    `json:"actor_discord_user_id"`
	Body               string    `json:"body"`
	MetadataJSON       string    `json:"metadata_json"`
	CreatedAt          time.Time `json:"created_at"`
}

// Transcript is private ticket content retained for a bounded period.
type Transcript struct {
	TicketID   string    `json:"ticket_id"`
	GuildID    string    `json:"guild_id"`
	Content    string    `json:"content"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ModuleStatus is the non-sensitive ticket configuration and queue health contract.
type ModuleStatus struct {
	Enabled         bool  `json:"enabled"`
	EntryConfigured bool  `json:"entry_configured"`
	OpenTickets     int64 `json:"open_tickets"`
}

// Descriptor exposes ticket configuration validation to the shared registry.
func Descriptor() modules.Descriptor {
	return modules.Descriptor{ID: modules.Tickets, DisplayName: "Tickets", Validate: validateSettingsJSON}
}

// QueueReceipt identifies the Discord message that safely holds ticket history.
type QueueReceipt struct {
	MessageID string
	URL       string
}
