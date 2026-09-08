package tickets

import (
	"context"
	"strings"
)

// ActiveForMember returns only the caller's reserved ticket, including a ticket
// awaiting close cleanup. A provisional opening without a record returns nil.
func (s *Service) ActiveForMember(ctx context.Context, actor Actor) (*Ticket, error) {
	if strings.TrimSpace(actor.GuildID) == "" || strings.TrimSpace(actor.DiscordUserID) == "" {
		return nil, ErrPermissionDenied
	}
	return s.store.activeForMember(ctx, actor.GuildID, actor.DiscordUserID)
}

// activeForMember joins the durable reservation to a ticket owned by that member.
func (s *Store) activeForMember(ctx context.Context, guildID, memberID string) (*Ticket, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("guild_id = ? AND owner_discord_user_id = ?", guildID, memberID).
		Where("id IN (SELECT open_ticket_id FROM ticket_member_states WHERE guild_id = ? AND owner_discord_user_id = ?)", guildID, memberID).
		Limit(1).Find(&record)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	ticket := ticketFromRecord(record)
	return &ticket, nil
}
