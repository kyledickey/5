package tickets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrJournalIncomplete prevents destructive closure after an unretained gateway
// message exceeded the bounded retry buffer. It requires operator attention.
var ErrJournalIncomplete = errors.New("ticket message journal is incomplete")

// ErrJournalCutoff identifies delayed gateway delivery after the locked thread's
// final snapshot. Surviving messages are covered by final history; already deleted
// messages first delivered beyond that frontier cannot enter this transcript.
var ErrJournalCutoff = errors.New("ticket transcript snapshot cutoff has passed")

// TranscriptAttachment retains the existing transcript's descriptive attachment
// context. Original URLs may expire; this is not binary attachment archival.
type TranscriptAttachment struct {
	Name string
	Size int
	URL  string
}

// TranscriptMessage is one received or surviving native Discord message. The
// journal preserves first-received text; final capture supplies surviving files.
type TranscriptMessage struct {
	MessageID, AuthorID, AuthorName, Body string
	SentAt                                time.Time
	Attachments                           []TranscriptAttachment
}

// MessageJournal is an insert-once ticket-owned original text record. Closed
// entries expire with the retained transcript rather than becoming a second archive.
type MessageJournal struct {
	GuildID                string     `gorm:"type:char(26);primaryKey"`
	ThreadDiscordChannelID string     `gorm:"size:32;primaryKey"`
	MessageDiscordID       string     `gorm:"size:32;primaryKey"`
	TicketID               string     `gorm:"type:char(26);not null;index"`
	AuthorDiscordUserID    string     `gorm:"size:32;not null"`
	AuthorName             string     `gorm:"size:255"`
	Body                   string     `gorm:"type:text;not null"`
	SentAt                 time.Time  `gorm:"not null"`
	ExpiresAt              *time.Time `gorm:"index"`
}

// TableName isolates original ticket text from optional general-logging storage.
func (MessageJournal) TableName() string { return "ticket_message_journal" }

// pendingJournalMessage holds one bounded retry after a transient SQL failure.
type pendingJournalMessage struct {
	guildID, threadID string
	message           TranscriptMessage
}

// messageJournalGate keeps global bounded retry bookkeeping separate from
// refcounted per-thread processing gates; its mutex never covers database work.
// It cannot recover events never delivered by Discord or buffered writes lost in
// a hard crash while SQL was unavailable; no event outbox is claimed here.
type messageJournalGate struct {
	mu         sync.Mutex
	pending    map[string]pendingJournalMessage
	blocked    map[string]bool
	allBlocked bool
	locks      ticketCloseLocks
	threads    map[string]*journalThreadState
	known      map[string]string
}

// journalThreadState defines the admission frontier while a close snapshots one
// thread. References cover holders/waiters so idle admission entries are removed.
type journalThreadState struct {
	references int
	sealed     bool
}

// hydrateJournalThreads loads only active ticket identities, never message content.
// Startup lookup failure fails closure closed rather than pretending retention ran.
func (s *Service) hydrateJournalThreads() {
	var records []ticketRecord
	err := s.store.db.Where("status = ?", StatusOpen).Find(&records).Error
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	s.journal.known = make(map[string]string, len(records))
	if err != nil {
		s.journal.allBlocked = true
		slog.Error("Ticket journal identity hydration failed; restart after restoring storage", "error", err)
		return
	}
	for _, record := range records {
		s.journal.known[record.ThreadDiscordChannelID] = record.GuildID
	}
}

// KnownMessageThread returns a positively identified active native ticket thread.
// It performs no SQL so unrelated gateway traffic is never buffered during outages.
func (s *Service) KnownMessageThread(threadID string) (string, bool) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	guildID, ok := s.journal.known[threadID]
	return guildID, ok
}

// rememberJournalThread admits a newly persisted ticket before its first reply.
func (s *Service) rememberJournalThread(ticket *Ticket) {
	s.journal.mu.Lock()
	defer s.journal.mu.Unlock()
	if s.journal.known == nil {
		s.journal.known = map[string]string{}
	}
	s.journal.known[ticket.ThreadDiscordChannelID] = ticket.GuildID
}

// journalReference attaches one operation to bounded-lifetime thread state.
func (s *Service) journalReference(threadID string) (*journalThreadState, func()) {
	s.journal.mu.Lock()
	if s.journal.threads == nil {
		s.journal.threads = map[string]*journalThreadState{}
	}
	state := s.journal.threads[threadID]
	if state == nil {
		state = &journalThreadState{}
		s.journal.threads[threadID] = state
	}
	state.references++
	s.journal.mu.Unlock()
	return state, func() {
		s.journal.mu.Lock()
		defer s.journal.mu.Unlock()
		state.references--
		if state.references == 0 {
			delete(s.journal.threads, threadID)
		}
	}
}

// journalThreadKey scopes retry state to an internal guild and native thread.
func journalThreadKey(guildID, threadID string) string { return guildID + ":" + threadID }

// RecordMessage journals native ticket creates independently of logging settings.
// The first text wins across replays/edits. Failed writes remain bounded in memory
// and are retried before closure; overflow explicitly blocks destructive closure.
func (s *Service) RecordMessage(ctx context.Context, guildID, threadID string, message TranscriptMessage) error {
	if s == nil || s.store == nil {
		return errors.New("ticket journal is unavailable")
	}
	if guildID == "" || threadID == "" || message.MessageID == "" || message.AuthorID == "" {
		return errors.New("ticket message identity is incomplete")
	}
	if knownGuild, ok := s.KnownMessageThread(threadID); !ok || knownGuild != guildID {
		return nil
	}
	state, drop := s.journalReference(threadID)
	defer drop()
	s.journal.mu.Lock()
	if state.sealed {
		s.journal.mu.Unlock()
		return ErrJournalCutoff
	}
	key := journalThreadKey(guildID, threadID) + ":" + message.MessageID
	unresolvedKey := journalThreadKey("", threadID) + ":" + message.MessageID
	if prior, ok := s.journal.pending[unresolvedKey]; ok {
		message = prior.message
	}
	if err := s.bufferJournal(guildID, threadID, message); err != nil {
		s.journal.mu.Unlock()
		return err
	}
	s.journal.mu.Unlock()
	// Registration precedes waiting: closure can flush this exact original text
	// even if it wins the processing gate before this callback is scheduled.
	release, err := s.journal.locks.acquire(ctx, threadID)
	if err != nil {
		return err
	}
	defer release()
	s.journal.mu.Lock()
	pending, exists := s.journal.pending[key]
	s.journal.mu.Unlock()
	if !exists {
		return nil
	} // A closing snapshot already persisted this write.
	err = s.store.recordMessage(ctx, guildID, threadID, pending.message)
	if err == nil {
		s.journal.mu.Lock()
		delete(s.journal.pending, key)
		delete(s.journal.pending, unresolvedKey)
		s.journal.mu.Unlock()
	}
	return err
}

// bufferJournal keeps first-received text and records a sticky closure failure if
// bounded process memory cannot retain it. Callers hold the journal mutex.
func (s *Service) bufferJournal(guildID, threadID string, message TranscriptMessage) error {
	key := journalThreadKey(guildID, threadID) + ":" + message.MessageID
	if s.journal.pending == nil {
		s.journal.pending = map[string]pendingJournalMessage{}
	}
	if _, ok := s.journal.pending[key]; ok {
		return nil
	}
	if len(s.journal.pending) >= 1000 {
		if s.journal.blocked == nil {
			s.journal.blocked = map[string]bool{}
		}
		if len(s.journal.blocked) < 1000 {
			s.journal.blocked[journalThreadKey(guildID, threadID)] = true
		} else {
			s.journal.allBlocked = true
		}
		return ErrJournalIncomplete
	}
	message.Attachments = nil
	s.journal.pending[key] = pendingJournalMessage{guildID: guildID, threadID: threadID, message: message}
	return nil
}

// recordMessage accepts only a currently open ticket in the exact guild/thread.
// Unrelated channels do not create journal records or consume persistent storage.
func (s *Store) recordMessage(ctx context.Context, guildID, threadID string, message TranscriptMessage) error {
	var ticket ticketRecord
	result := s.db.WithContext(ctx).Where("guild_id = ? AND thread_discord_channel_id = ? AND status = ?", guildID, threadID, StatusOpen).Limit(1).Find(&ticket)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&MessageJournal{
		GuildID: guildID, ThreadDiscordChannelID: threadID, MessageDiscordID: message.MessageID, TicketID: ticket.ID,
		AuthorDiscordUserID: message.AuthorID, AuthorName: message.AuthorName, Body: message.Body, SentAt: message.SentAt,
	}).Error
}

// flushJournal retries every received write for this ticket before reading its
// durable original text. A failed read/write prevents Resolve and Discord deletion.
func (s *Service) flushJournal(ctx context.Context, ticket *Ticket) ([]MessageJournal, error) {
	s.journal.mu.Lock()
	blocked := s.journal.allBlocked || s.journal.blocked[journalThreadKey(ticket.GuildID, ticket.ThreadDiscordChannelID)] || s.journal.blocked[journalThreadKey("", ticket.ThreadDiscordChannelID)]
	pending := map[string]pendingJournalMessage{}
	for key, message := range s.journal.pending {
		if (message.guildID == "" || message.guildID == ticket.GuildID) && message.threadID == ticket.ThreadDiscordChannelID {
			pending[key] = message
		}
	}
	s.journal.mu.Unlock()
	if blocked {
		return nil, ErrJournalIncomplete
	}
	for key, message := range pending {
		if err := s.store.recordMessage(ctx, ticket.GuildID, message.threadID, message.message); err != nil {
			return nil, err
		}
		s.journal.mu.Lock()
		delete(s.journal.pending, key)
		s.journal.mu.Unlock()
	}
	var messages []MessageJournal
	err := s.store.db.WithContext(ctx).Where("guild_id = ? AND ticket_id = ?", ticket.GuildID, ticket.ID).Find(&messages).Error
	return messages, err
}

// closeJournalAdmission seals only this thread after final live capture. All
// previously admitted originals are already registered for the closing flush.
// Late arrivals receive an explicit error; they are never silently accepted.
func (s *Service) closeJournalAdmission(ctx context.Context, ticket *Ticket) (func(), error) {
	state, drop := s.journalReference(ticket.ThreadDiscordChannelID)
	release, err := s.journal.locks.acquire(ctx, ticket.ThreadDiscordChannelID)
	if err != nil {
		drop()
		return nil, err
	}
	s.journal.mu.Lock()
	state.sealed = true
	s.journal.mu.Unlock()
	return func() { s.journal.mu.Lock(); state.sealed = false; s.journal.mu.Unlock(); release(); drop() }, nil
}

// ResolveNativeTranscript merges original received text with final live history
// under the journal gate, then atomically saves closure/retention before deletion.
func (s *Service) ResolveNativeTranscript(ctx context.Context, actor Actor, ticketID string, surviving []TranscriptMessage) (*Ticket, error) {
	ticket, err := s.authorizedTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	release, err := s.closeJournalAdmission(ctx, ticket)
	if err != nil {
		return nil, err
	}
	defer release()
	journal, err := s.flushJournal(ctx, ticket)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]TranscriptMessage, len(surviving)+len(journal))
	for _, message := range surviving {
		if message.MessageID == "" {
			return nil, errors.New("ticket transcript contains a missing message identity")
		}
		if _, exists := merged[message.MessageID]; !exists {
			merged[message.MessageID] = message
		}
	}
	for _, original := range journal {
		message := merged[original.MessageDiscordID]
		message.MessageID, message.AuthorID, message.AuthorName, message.Body, message.SentAt = original.MessageDiscordID, original.AuthorDiscordUserID, original.AuthorName, original.Body, original.SentAt
		merged[message.MessageID] = message
	}
	messages := make([]TranscriptMessage, 0, len(merged))
	for _, message := range merged {
		messages = append(messages, message)
	}
	return s.Resolve(ctx, actor, ticketID, FormatTranscript(messages))
}

// resolveLegacyTranscript keeps compatibility with older client ports only when
// there is no journal text to merge; it must never silently discard original text.
func (s *Service) resolveLegacyTranscript(ctx context.Context, actor Actor, ticketID, content string) (*Ticket, error) {
	ticket, err := s.authorizedTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	release, err := s.closeJournalAdmission(ctx, ticket)
	if err != nil {
		return nil, err
	}
	defer release()
	journal, err := s.flushJournal(ctx, ticket)
	if err != nil {
		return nil, err
	}
	if len(journal) > 0 {
		return nil, errors.New("ticket client cannot merge journal messages")
	}
	return s.Resolve(ctx, actor, ticketID, content)
}

// FormatTranscript orders deduplicated native messages chronologically, using
// Discord snowflake order for equal timestamps, and preserves attachment context.
func FormatTranscript(messages []TranscriptMessage) string {
	messages = append([]TranscriptMessage(nil), messages...)
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].SentAt.Equal(messages[j].SentAt) {
			if len(messages[i].MessageID) != len(messages[j].MessageID) {
				return len(messages[i].MessageID) < len(messages[j].MessageID)
			}
			return messages[i].MessageID < messages[j].MessageID
		}
		return messages[i].SentAt.Before(messages[j].SentAt)
	})
	var transcript strings.Builder
	seen := map[string]bool{}
	for _, message := range messages {
		if seen[message.MessageID] {
			continue
		}
		seen[message.MessageID] = true
		author := "unknown"
		if message.AuthorID != "" {
			author = message.AuthorName + " (" + message.AuthorID + ")"
		}
		fmt.Fprintf(&transcript, "[%s] %s: %s\n", message.SentAt.UTC().Format(time.RFC3339), author, message.Body)
		for _, attachment := range message.Attachments {
			fmt.Fprintf(&transcript, "  attachment: %s (%d bytes)\n", attachment.Name, attachment.Size)
			if attachment.URL != "" {
				fmt.Fprintf(&transcript, "  original attachment URL (may expire): %s\n", attachment.URL)
			}
		}
	}
	return transcript.String()
}

// purgeJournal removes only text whose closed-ticket retention has elapsed.
func purgeJournal(tx *gorm.DB, now time.Time) error {
	return tx.Where("expires_at <= ?", now).Delete(&MessageJournal{}).Error
}
