package tickets

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ticketRecord struct {
	ID                      string `gorm:"type:char(26);primaryKey"`
	GuildID                 string `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string `gorm:"size:32;uniqueIndex"`
	Status                  Status `gorm:"size:32;not null;index:idx_ticket_guild_status,priority:2"`
	LogChannelDiscordID     string `gorm:"size:32"`
	LogMessageDiscordID     string `gorm:"size:32"`
	ResolvedByDiscordUserID string `gorm:"size:32"`
	ResolvedAt              *time.Time
	TranscriptURL           string `gorm:"size:1024"`
	MetadataJSON            string `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt    time.Time
}

func (ticketRecord) TableName() string { return "tickets" }

type eventRecord struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	TicketID             string    `gorm:"type:char(26);not null;index"`
	GuildID              string    `gorm:"type:char(26);not null;index"`
	EventType            EventType `gorm:"size:64;not null;index"`
	ActorDiscordUserID   string    `gorm:"size:32;index"`
	Body                 string    `gorm:"type:text;not null"`
	MetadataJSON         string    `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt time.Time
}

func (eventRecord) TableName() string { return "ticket_events" }

type transcriptRecord struct {
	TicketID   string    `gorm:"type:char(26);primaryKey"`
	GuildID    string    `gorm:"type:char(26);not null;index"`
	Content    string    `gorm:"type:longtext;not null"`
	CapturedAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
}

func (transcriptRecord) TableName() string { return "ticket_transcripts" }

type memberStateRecord struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_ticket_member_state,priority:1"`
	OwnerDiscordUserID   string    `gorm:"size:32;not null;uniqueIndex:idx_ticket_member_state,priority:2"`
	OpenTicketID         string    `gorm:"type:char(26);not null"`
	WindowStartedAt      time.Time `gorm:"not null"`
	OpenCount            int       `gorm:"not null"`
	CreatedAt, UpdatedAt time.Time
}

func (memberStateRecord) TableName() string { return "ticket_member_states" }

// Store persists tickets, their immutable timelines, transcripts, and import identities.
type Store struct{ db *gorm.DB }

// NewStore constructs ticket persistence from an adapter-owned database handle.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Migration exposes ticket schema changes without editing the central migration registry.
func Migration() modules.Migration {
	return modules.Migration{Version: 110, Name: "ticket_lifecycle", Apply: func(db *gorm.DB) error {
		return db.AutoMigrate(&ticketRecord{}, &eventRecord{}, &transcriptRecord{}, &memberStateRecord{})
	}}
}

func (s *Store) create(ctx context.Context, guildID, ownerID, threadID string, now time.Time) (*Ticket, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("ticket database is not connected")
	}
	var out Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockMemberState(tx, guildID, ownerID, now)
		if err != nil {
			return err
		}
		if state.OpenTicketID != "" {
			return ErrDuplicateOpen
		}

		record := ticketRecord{ID: ulid.Make().String(), GuildID: guildID, OwnerDiscordUserID: ownerID, ThreadDiscordChannelID: threadID, Status: StatusOpen, MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := appendEvent(tx, record, EventOpened, ownerID, "Ticket opened", "{}", now); err != nil {
			return err
		}
		state.OpenTicketID = record.ID
		state.UpdatedAt = now
		if err := tx.Save(state).Error; err != nil {
			return err
		}
		out = ticketFromRecord(record)
		return nil
	})
	return &out, err
}

func (s *Store) get(ctx context.Context, guildID, ticketID string) (*Ticket, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).Where("guild_id = ? AND id = ?", guildID, ticketID).Limit(1).Find(&record)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	ticket := ticketFromRecord(record)
	return &ticket, nil
}

func (s *Store) transition(ctx context.Context, guildID, ticketID string, from []Status, to Status, actorID string, eventType EventType, body string, resolved bool, transcript *Transcript, now time.Time) (*Ticket, error) {
	var out Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ticketRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND id = ?", guildID, ticketID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		allowed := false
		for _, status := range from {
			if record.Status == status {
				allowed = true
			}
		}
		if !allowed {
			return ErrInvalidTransition
		}
		record.Status, record.UpdatedAt = to, now
		state, err := lockMemberState(tx, record.GuildID, record.OwnerDiscordUserID, now)
		if err != nil {
			return err
		}
		if to == StatusOpen {
			if state.OpenTicketID != "" && state.OpenTicketID != record.ID {
				return ErrDuplicateOpen
			}
			state.OpenTicketID = record.ID
		} else if to != StatusResolved && state.OpenTicketID == record.ID {
			state.OpenTicketID = ""
		}
		state.UpdatedAt = now
		if resolved {
			record.ResolvedByDiscordUserID, record.ResolvedAt = actorID, &now
		} else {
			record.ResolvedByDiscordUserID, record.ResolvedAt = "", nil
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		if err := tx.Save(state).Error; err != nil {
			return err
		}
		if err := appendEvent(tx, record, eventType, actorID, body, "{}", now); err != nil {
			return err
		}
		if transcript != nil {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&transcriptRecord{TicketID: record.ID, GuildID: record.GuildID, Content: transcript.Content, CapturedAt: transcript.CapturedAt, ExpiresAt: transcript.ExpiresAt}).Error; err != nil {
				return err
			}
		}
		out = ticketFromRecord(record)
		return nil
	})
	return &out, err
}

func lockMemberState(tx *gorm.DB, guildID, ownerID string, now time.Time) (*memberStateRecord, error) {
	seed := memberStateRecord{ID: ulid.Make().String(), GuildID: guildID, OwnerDiscordUserID: ownerID, WindowStartedAt: now, CreatedAt: now, UpdatedAt: now}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "guild_id"}, {Name: "owner_discord_user_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
		return nil, err
	}
	var state memberStateRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND owner_discord_user_id = ?", guildID, ownerID).First(&state).Error; err != nil {
		return nil, err
	}
	return &state, nil
}

func (s *Store) append(ctx context.Context, ticket Ticket, eventType EventType, actorID, body, metadata string, now time.Time) error {
	record := ticketRecord{ID: ticket.ID, GuildID: ticket.GuildID}
	return appendEvent(s.db.WithContext(ctx), record, eventType, actorID, body, metadata, now)
}

func appendEvent(db *gorm.DB, ticket ticketRecord, eventType EventType, actorID, body, metadata string, now time.Time) error {
	return db.Create(&eventRecord{ID: ulid.Make().String(), TicketID: ticket.ID, GuildID: ticket.GuildID, EventType: eventType, ActorDiscordUserID: actorID, Body: body, MetadataJSON: metadata, CreatedAt: now, UpdatedAt: now}).Error
}

func (s *Store) list(ctx context.Context, guildID string, status Status, limit int) ([]Ticket, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var records []ticketRecord
	if err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]Ticket, len(records))
	for i := range records {
		out[i] = ticketFromRecord(records[i])
	}
	return out, nil
}

func (s *Store) count(ctx context.Context, guildID string, status Status) (int64, error) {
	var count int64
	query := s.db.WithContext(ctx).Model(&ticketRecord{}).Where("guild_id = ?", guildID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	return count, query.Count(&count).Error
}

func (s *Store) timeline(ctx context.Context, guildID, ticketID string) ([]Event, error) {
	if _, err := s.get(ctx, guildID, ticketID); err != nil {
		return nil, err
	}
	var records []eventRecord
	if err := s.db.WithContext(ctx).Where("guild_id = ? AND ticket_id = ?", guildID, ticketID).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	out := make([]Event, len(records))
	for i, r := range records {
		out[i] = Event{ID: r.ID, TicketID: r.TicketID, GuildID: r.GuildID, Type: r.EventType, ActorDiscordUserID: r.ActorDiscordUserID, Body: r.Body, MetadataJSON: r.MetadataJSON, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

func (s *Store) transcript(ctx context.Context, guildID, ticketID string, now time.Time) (*Transcript, error) {
	var record transcriptRecord
	result := s.db.WithContext(ctx).Where("guild_id = ? AND ticket_id = ? AND expires_at > ?", guildID, ticketID, now).Limit(1).Find(&record)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &Transcript{TicketID: record.TicketID, GuildID: record.GuildID, Content: record.Content, CapturedAt: record.CapturedAt, ExpiresAt: record.ExpiresAt}, nil
}

func (s *Store) purgeExpiredTranscripts(ctx context.Context, now time.Time) (int64, error) {
	result := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&transcriptRecord{})
	return result.RowsAffected, result.Error
}

func (s *Store) importTicket(ctx context.Context, source LegacyTicket, now time.Time) (string, bool, error) {
	var targetID string
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var prior modules.ImportRecord
		q := tx.Where("guild_id = ? AND module_id = ? AND source_id = ?", source.GuildID, modules.Tickets, source.SourceID).Limit(1).Find(&prior)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected > 0 {
			targetID = prior.TargetID
			return nil
		}
		threadID := source.ThreadDiscordChannelID
		if threadID == "" {
			sum := sha256.Sum256([]byte(source.GuildID + ":" + source.SourceID))
			threadID = fmt.Sprintf("legacy-%x", sum[:12])
		}
		record := ticketRecord{ID: ulid.Make().String(), GuildID: source.GuildID, OwnerDiscordUserID: source.OwnerDiscordUserID, ThreadDiscordChannelID: threadID, Status: source.Status, MetadataJSON: `{"imported_from":"v4"}`, CreatedAt: source.CreatedAt, UpdatedAt: now}
		if record.CreatedAt.IsZero() {
			record.CreatedAt = now
		}
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("create imported ticket: %w", err)
		}
		if err := appendEvent(tx, record, EventOpened, "quack-v4-import", "Imported from v4", `{"source":"v4"}`, now); err != nil {
			return err
		}
		if source.Status == StatusOpen {
			state, err := lockMemberState(tx, source.GuildID, source.OwnerDiscordUserID, now)
			if err != nil {
				return err
			}
			if state.OpenTicketID != "" {
				return ErrDuplicateOpen
			}
			state.OpenTicketID = record.ID
			state.UpdatedAt = now
			if err := tx.Save(state).Error; err != nil {
				return err
			}
		}
		ledger := modules.ImportRecord{ID: ulid.Make().String(), GuildID: source.GuildID, ModuleID: modules.Tickets, SourceID: source.SourceID, TargetID: record.ID, CreatedAt: now}
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
		targetID, created = record.ID, true
		return nil
	})
	return targetID, created, err
}

func (s *Store) importTarget(ctx context.Context, guildID, sourceID string) (string, bool, error) {
	var record modules.ImportRecord
	result := s.db.WithContext(ctx).Where("guild_id = ? AND module_id = ? AND source_id = ?", guildID, modules.Tickets, sourceID).Limit(1).Find(&record)
	if result.Error != nil {
		return "", false, result.Error
	}
	return record.TargetID, result.RowsAffected > 0, nil
}

func ticketFromRecord(r ticketRecord) Ticket {
	return Ticket{LogMessageDiscordID: r.LogMessageDiscordID, LogChannelDiscordID: r.LogChannelDiscordID, TranscriptURL: r.TranscriptURL, ID: r.ID, GuildID: r.GuildID, OwnerDiscordUserID: r.OwnerDiscordUserID, ThreadDiscordChannelID: r.ThreadDiscordChannelID, Status: r.Status, ResolvedByDiscordUserID: r.ResolvedByDiscordUserID, ResolvedAt: r.ResolvedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// saveQueueReceipt records the Discord destination only after successful delivery.
// A saved transcript URL is the durable fence before deleting the source thread.
func (s *Store) saveQueueReceipt(ctx context.Context, ticket *Ticket, channelID, messageID, transcriptURL string) error {
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID).Updates(map[string]any{"log_channel_discord_id": channelID, "log_message_discord_id": messageID, "transcript_url": transcriptURL})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrNotFound
	}
	ticket.LogChannelDiscordID, ticket.LogMessageDiscordID, ticket.TranscriptURL = channelID, messageID, transcriptURL
	return nil
}

// saveEntryPanel updates only the receipt in the current configuration. The row
// lock prevents an old setup request from overwriting a newer channel selection.
func (s *Store) saveEntryPanel(ctx context.Context, guildID, channelID, messageID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ? AND module_id = ?", guildID, modules.Tickets).First(&config).Error; err != nil {
			return err
		}
		var settings Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return err
		}
		if settings.EntryChannelDiscordID != channelID {
			return errors.New("ticket entry channel changed during setup")
		}
		settings.EntryPanelChannelID, settings.EntryPanelMessageID = channelID, messageID
		payload, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		return tx.Model(&config).Update("config_json", string(payload)).Error
	})
}
