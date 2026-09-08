package tickets

import (
	"context"
	"time"
)

// finishClosure releases the member slot only after Discord cleanup succeeds.
// The persisted transcript receipt is required; retries cannot release a newer
// ticket's reservation or an unrelated member's slot.
func (s *Store) finishClosure(ctx context.Context, guildID, ticketID string) error {
	ticket, err := s.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusResolved || ticket.TranscriptURL == "" {
		return ErrInvalidTransition
	}
	return s.db.WithContext(ctx).Model(&memberStateRecord{}).
		Where("guild_id = ? AND owner_discord_user_id = ? AND open_ticket_id = ?", guildID, ticket.OwnerDiscordUserID, ticketID).
		Updates(map[string]any{"open_ticket_id": "", "updated_at": time.Now().UTC()}).Error
}
