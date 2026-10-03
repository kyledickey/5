package discordbot

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
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

// RemoveOwnedTimeout checks the current expiry against the original successful
// execution. Legacy receipts recorded seconds only; new receipts retain Discord's
// millisecond precision. GET and PATCH cannot be made atomic by Discord's API.
func (b *Bot) RemoveOwnedTimeout(ctx context.Context, guildID, userID, expected, auditReason string) (map[string]any, error) {
	until, err := time.Parse(time.RFC3339Nano, expected)
	if err != nil {
		return nil, reversalOwnershipError("reversal_provenance_unavailable", "The original timeout expiry is invalid. Review it manually.")
	}
	member, err := b.Session.GuildMember(guildID, userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("inspect_timeout", err, false)
	}
	if member == nil {
		return nil, reversalOwnershipError("reversal_provenance_unavailable", "Could not inspect the member's timeout.")
	}
	live := member.CommunicationDisabledUntil
	if live == nil || !live.After(time.Now().UTC()) {
		return map[string]any{"result": "timeout_already_absent", "reversal_noop": true}, nil
	}
	matches := live.Equal(until)
	if !strings.Contains(expected, ".") {
		matches = live.Unix() == until.Unix()
	}
	if !matches {
		return nil, reversalOwnershipError("reversal_ownership_conflict", "The current timeout differs from this case's punishment. Nothing was removed; review it manually.")
	}
	return b.RemoveMemberTimeout(ctx, guildID, userID, auditReason)
}

// RemoveOwnedBan requires the current ban's reason to match the unique original
// Quack case reason. A copied reason is not immutable proof, and an external ban
// replacement between GET and DELETE cannot be excluded by Discord's API.
func (b *Bot) RemoveOwnedBan(ctx context.Context, guildID, userID, expected, auditReason string) (map[string]any, error) {
	if expected == "" {
		return nil, reversalOwnershipError("reversal_provenance_unavailable", "The original ban reason is unavailable. Review it manually.")
	}
	ban, err := b.Session.GuildBan(guildID, userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == discordgo.ErrCodeUnknownBan {
		return map[string]any{"result": "ban_already_absent", "reversal_noop": true}, nil
	}
	if err != nil {
		return nil, classifyDiscordOperation("inspect_ban", err, false)
	}
	if ban == nil || ban.Reason == "" {
		return nil, reversalOwnershipError("reversal_provenance_unavailable", "The current ban has no verifiable case reason. Review it manually.")
	}
	if ban.Reason != expected {
		return nil, reversalOwnershipError("reversal_ownership_conflict", "The current ban differs from this case's punishment. Nothing was removed; review it manually.")
	}
	return b.UnbanMember(ctx, guildID, userID, auditReason)
}

// reversalOwnershipError is a non-retryable review outcome, never a removal success.
func reversalOwnershipError(code, message string) error {
	return actionmods.DiscordError{Code: code, Message: message}
}
