package store

import (
	"time"

	"gorm.io/gorm"
)

const migration0007Definition = `ticket-lifecycle-v1
logical migration: 0110 ticket_lifecycle
schema: preserve and upgrade existing tickets and ticket_events; add separately retained ticket_transcripts and abuse-control ticket_member_states
boundary: tickets remain separate from moderation cases and appeals
rollback: forward-only because ticket timelines, transcripts, and import-compatible rows are operator data`

// migration0007TicketLifecycle reconciles logical module migration 0110 into
// the central contiguous production ledger.
func migration0007TicketLifecycle() migration {
	return migration{
		Version: 7, Name: "ticket_lifecycle_0110",
		Definition: migration0007Definition, Source: migration0007Source,
		Up: applyTicketLifecycle,
	}
}

// migration0007Ticket freezes the existing ticket row during logical 0110.
type migration0007Ticket struct {
	ID                      string     `gorm:"type:char(26);primaryKey"`
	GuildID                 string     `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string     `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string     `gorm:"size:32;uniqueIndex"`
	Status                  string     `gorm:"size:32;not null;index:idx_ticket_guild_status,priority:2"`
	LogChannelDiscordID     string     `gorm:"size:32"`
	LogMessageDiscordID     string     `gorm:"size:32"`
	ResolvedByDiscordUserID string     `gorm:"size:32"`
	ResolvedAt              *time.Time `gorm:"index"`
	TranscriptURL           string     `gorm:"size:1024"`
	MetadataJSON            string     `gorm:"type:json;not null"`
	CreatedAt               time.Time  `gorm:"not null;index"`
	UpdatedAt               time.Time  `gorm:"not null"`
}

// TableName preserves the existing tickets table.
func (migration0007Ticket) TableName() string { return "tickets" }

// migration0007TicketEvent freezes immutable ticket timeline rows.
type migration0007TicketEvent struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	TicketID             string    `gorm:"type:char(26);not null;index"`
	GuildID              string    `gorm:"type:char(26);not null;index"`
	EventType            string    `gorm:"size:64;not null;index"`
	ActorDiscordUserID   string    `gorm:"size:32;index"`
	Body                 string    `gorm:"type:text;not null"`
	MetadataJSON         string    `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt time.Time `gorm:"not null"`
}

// TableName preserves the existing ticket_events table.
func (migration0007TicketEvent) TableName() string { return "ticket_events" }

// migration0007Transcript freezes separately retained private transcript data.
type migration0007Transcript struct {
	TicketID   string    `gorm:"type:char(26);primaryKey"`
	GuildID    string    `gorm:"type:char(26);not null;index"`
	Content    string    `gorm:"type:longtext;not null"`
	CapturedAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
}

// TableName identifies the separately retained transcript table.
func (migration0007Transcript) TableName() string { return "ticket_transcripts" }

// migration0007MemberState freezes duplicate-open and rolling-limit state.
type migration0007MemberState struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_ticket_member_state,priority:1"`
	OwnerDiscordUserID   string    `gorm:"size:32;not null;uniqueIndex:idx_ticket_member_state,priority:2"`
	OpenTicketID         string    `gorm:"type:char(26);not null"`
	WindowStartedAt      time.Time `gorm:"not null"`
	OpenCount            int       `gorm:"not null"`
	CreatedAt, UpdatedAt time.Time `gorm:"not null"`
}

// TableName identifies the ticket abuse-control state table.
func (migration0007MemberState) TableName() string { return "ticket_member_states" }

// applyTicketLifecycle upgrades preserved rows and creates logical 0110 tables.
func applyTicketLifecycle(db *gorm.DB) error {
	return withMySQLTableOptions(db).AutoMigrate(
		&migration0007Ticket{}, &migration0007TicketEvent{},
		&migration0007Transcript{}, &migration0007MemberState{},
	)
}
