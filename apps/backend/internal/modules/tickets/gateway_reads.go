package tickets

import "context"

// ThreadRepairPageSize bounds each gateway permission repair database read.
const ThreadRepairPageSize = 100

// ThreadRepairTarget carries only the identities needed by the Discord adapter
// to reconcile an open ticket's private-thread membership.
type ThreadRepairTarget struct {
	ID                     string
	ThreadDiscordChannelID string
	OwnerDiscordUserID     string
}

// DeletedChannelTicketID looks up a deleted thread within its internal guild.
// This trusted gateway read includes resolved tickets so the existing adapter
// can repair their references too; missing channels return ErrNotFound.
func (s *Service) DeletedChannelTicketID(ctx context.Context, guildID, channelID string) (string, error) {
	return s.store.deletedChannelTicketID(ctx, guildID, channelID)
}

// OpenThreadRepairPage returns at most ThreadRepairPageSize open tickets after
// the exclusive ID cursor, ordered by ID. This trusted gateway read deliberately
// works when new tickets are disabled; Discord membership repair stays in the adapter.
func (s *Service) OpenThreadRepairPage(ctx context.Context, guildID, afterID string) ([]ThreadRepairTarget, error) {
	return s.store.openThreadRepairPage(ctx, guildID, afterID)
}

// deletedChannelTicketID owns the guild-scoped channel lookup without loading
// ticket content or deciding how a deleted Discord thread should be repaired.
func (s *Store) deletedChannelTicketID(ctx context.Context, guildID, channelID string) (string, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).Select("id").
		Where("guild_id = ? AND thread_discord_channel_id = ?", guildID, channelID).Limit(1).Find(&record)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", ErrNotFound
	}
	return record.ID, nil
}

// openThreadRepairPage keeps repair selection and its fixed bound inside the
// ticket persistence boundary, avoiding offsets as earlier tickets are closed.
func (s *Store) openThreadRepairPage(ctx context.Context, guildID, afterID string) ([]ThreadRepairTarget, error) {
	var records []ThreadRepairTarget
	err := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Select("id, thread_discord_channel_id, owner_discord_user_id").
		Where("guild_id = ? AND status = ? AND id > ?", guildID, StatusOpen, afterID).
		Order("id ASC").Limit(ThreadRepairPageSize).Find(&records).Error
	return records, err
}
