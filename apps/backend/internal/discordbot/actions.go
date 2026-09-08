package discordbot

import (
	"context"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TimeoutMember applies the exact template-defined timeout duration.
func (b *Bot) TimeoutMember(ctx context.Context, guildID, userID string, durationSeconds int, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	until := time.Now().UTC().Add(time.Duration(durationSeconds) * time.Second).Truncate(time.Millisecond)
	if err := b.Session.GuildMemberTimeout(guildID, userID, &until, discordgo.WithAuditLogReason(auditReason), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
		return nil, classifyDiscordOperation("timeout", err, false)
	}
	return map[string]any{"timeout_until": until.Format("2006-01-02T15:04:05.000Z07:00")}, nil
}

// KickMember removes the immutable case target using a bounded audit reason.
func (b *Bot) KickMember(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildMemberDeleteWithReason(guildID, userID, auditReason, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
		return nil, classifyDiscordOperation("kick", err, true)
	}
	return map[string]any{"result": "kicked"}, nil
}

// BanMember uses Discord's seconds-based deletion setting without rounding.
func (b *Bot) BanMember(ctx context.Context, guildID, userID string, deleteMessageSeconds int, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint := discordgo.EndpointGuildBan(guildID, userID)
	_, err := b.Session.RequestWithBucketID(http.MethodPut, endpoint, map[string]any{"delete_message_seconds": deleteMessageSeconds}, discordgo.EndpointGuildBan(guildID, ""), discordgo.WithAuditLogReason(auditReason), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("ban", err, true)
	}
	return map[string]any{"result": "banned", "delete_message_seconds": deleteMessageSeconds}, nil
}

// RemoveMemberTimeout executes an explicit staff-confirmed timeout reversal.
func (b *Bot) RemoveMemberTimeout(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildMemberTimeout(guildID, userID, nil, discordgo.WithAuditLogReason(auditReason), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
		return nil, classifyDiscordOperation("remove_timeout", err, true)
	}
	return map[string]any{"result": "timeout_removed"}, nil
}

// UnbanMember executes an explicit staff-confirmed ban reversal.
func (b *Bot) UnbanMember(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildBanDelete(guildID, userID, discordgo.WithAuditLogReason(auditReason), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false)); err != nil {
		return nil, classifyDiscordOperation("unban", err, true)
	}
	return map[string]any{"result": "unbanned"}, nil
}
