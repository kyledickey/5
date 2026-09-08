package discordbot

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

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
