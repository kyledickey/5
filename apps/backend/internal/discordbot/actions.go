package discordbot

import (
	"context"
	"net/http"
	"time"

	"github.com/bwmarrin/discordgo"
)

// TimeoutMember applies the exact template-defined timeout duration and returns
// the millisecond-precision expiry Discord will report, which later ownership
// checks compare against.
func (b *Bot) TimeoutMember(
	ctx context.Context,
	guildID, userID string,
	durationSeconds int,
	auditReason string,
) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	until := time.Now().UTC().Add(time.Duration(durationSeconds) * time.Second).Truncate(time.Millisecond)
	options := singleAttempt(ctx, discordgo.WithAuditLogReason(auditReason))
	if err := b.Session.GuildMemberTimeout(guildID, userID, &until, options...); err != nil {
		return nil, classifyDiscordOperation("timeout", err, false)
	}
	return map[string]any{"timeout_until": until.Format("2006-01-02T15:04:05.000Z07:00")}, nil
}

// KickMember removes the immutable case target using a bounded audit reason.
// A lost response is reported as outcome-uncertain because the kick may have happened.
func (b *Bot) KickMember(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildMemberDeleteWithReason(guildID, userID, auditReason, singleAttempt(ctx)...); err != nil {
		return nil, classifyDiscordOperation("kick", err, true)
	}
	return map[string]any{"result": "kicked"}, nil
}

// BanMember uses Discord's seconds-based deletion setting without rounding. It
// bypasses discordgo's ban helper because that only accepts whole days.
func (b *Bot) BanMember(
	ctx context.Context,
	guildID, userID string,
	deleteMessageSeconds int,
	auditReason string,
) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint := discordgo.EndpointGuildBan(guildID, userID)
	body := map[string]any{"delete_message_seconds": deleteMessageSeconds}
	options := singleAttempt(ctx, discordgo.WithAuditLogReason(auditReason))
	if _, err := b.Session.RequestWithBucketID(http.MethodPut, endpoint, body, discordgo.EndpointGuildBan(guildID, ""), options...); err != nil {
		return nil, classifyDiscordOperation("ban", err, true)
	}
	return map[string]any{"result": "banned", "delete_message_seconds": deleteMessageSeconds}, nil
}

// RemoveMemberTimeout executes an explicit staff-confirmed timeout reversal.
func (b *Bot) RemoveMemberTimeout(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := singleAttempt(ctx, discordgo.WithAuditLogReason(auditReason))
	if err := b.Session.GuildMemberTimeout(guildID, userID, nil, options...); err != nil {
		return nil, classifyDiscordOperation("remove_timeout", err, true)
	}
	return map[string]any{"result": "timeout_removed"}, nil
}

// UnbanMember executes an explicit staff-confirmed ban reversal.
func (b *Bot) UnbanMember(ctx context.Context, guildID, userID, auditReason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := singleAttempt(ctx, discordgo.WithAuditLogReason(auditReason))
	if err := b.Session.GuildBanDelete(guildID, userID, options...); err != nil {
		return nil, classifyDiscordOperation("unban", err, true)
	}
	return map[string]any{"result": "unbanned"}, nil
}
