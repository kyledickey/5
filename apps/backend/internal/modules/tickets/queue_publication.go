package tickets

import (
	"context"
	"errors"
)

// ErrQueueNotSent marks a transport failure known to precede delivery. Only this
// explicit result permits another initial send; network uncertainty does not.
var ErrQueueNotSent = errors.New("ticket queue message was not sent")

// ErrQueueDeliveryUnknown prevents duplicate posts after a send without a durable
// receipt. An administrator must inspect the destination before reconciling it.
var ErrQueueDeliveryUnknown = errors.New("ticket queue delivery is unconfirmed; check the staff queue before retrying")

// ErrQueueMessageMissing reports definite absence of a previously saved message.
// The module must fence its replacement before the transport sends a new post.
var ErrQueueMessageMissing = errors.New("ticket queue message is missing")

// clearQueueReceipt conditionally retires a missing or superseded receipt before
// replacement admission. Clearing its transcript link prevents cleanup from
// trusting a delivery that is known to be unavailable.
func (s *Store) clearQueueReceipt(ctx context.Context, ticket *Ticket) error {
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("id = ? AND guild_id = ? AND log_channel_discord_id = ? AND log_message_discord_id = ?", ticket.ID, ticket.GuildID, ticket.LogChannelDiscordID, ticket.LogMessageDiscordID).
		Updates(map[string]any{"log_channel_discord_id": "", "log_message_discord_id": "", "transcript_url": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID, ticket.LogMessageDiscordID, ticket.TranscriptURL = "", "", ""
	return nil
}

// reserveQueueSend uses an empty message receipt with a destination as a durable
// initial-send fence. Conditional admission also excludes overlapping repairs.
func (s *Store) reserveQueueSend(ctx context.Context, ticket *Ticket, channelID string) error {
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).Where("id = ? AND guild_id = ? AND COALESCE(log_channel_discord_id, '') = '' AND COALESCE(log_message_discord_id, '') = ''", ticket.ID, ticket.GuildID).Update("log_channel_discord_id", channelID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID = channelID
	return nil
}

// releaseQueueSend clears admission only after definite nondelivery. If saving
// this result fails, the conservative fence remains across process restarts.
func (s *Store) releaseQueueSend(ctx context.Context, ticket *Ticket, channelID string) error {
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).Where("id = ? AND guild_id = ? AND log_channel_discord_id = ? AND COALESCE(log_message_discord_id, '') = ''", ticket.ID, ticket.GuildID, channelID).Update("log_channel_discord_id", "")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID = ""
	return nil
}
