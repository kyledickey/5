package discordbot

import (
	"context"
	"errors"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

// singleAttempt returns the request options every adapter call uses: the
// caller's context, no automatic REST retries and no rate-limit sleeping, so
// retry policy stays with Quack's workers and a lost response is never repeated
// blindly. Extra options (audit-log reasons) are appended.
func singleAttempt(ctx context.Context, extra ...discordgo.RequestOption) []discordgo.RequestOption {
	return append([]discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}, extra...)
}

// classifyDiscordError is classifyDiscordOperation for reversible operations.
func classifyDiscordError(code string, err error) error {
	return classifyDiscordOperation(code, err, false)
}

// classifyDiscordOperation turns a discordgo error into an actionmods.DiscordError
// carrying only a stable code, a generic message, and retry/uncertainty flags.
// The raw response text is dropped because it may echo member content. For an
// irreversible operation (ban, kick, DM send) a 5xx or network error is marked
// OutcomeUncertain and not Retryable, because the request may have succeeded.
func classifyDiscordOperation(operation string, err error, irreversible bool) error {
	var rateLimit *discordgo.RateLimitError
	if errors.As(err, &rateLimit) {
		return actionmods.DiscordError{Code: operation + "_rate_limited", Message: "Discord rate limit reached", Retryable: true}
	}
	var restError *discordgo.RESTError
	if errors.As(err, &restError) && restError.Response != nil {
		status := restError.Response.StatusCode
		code := "discord_failure"
		retryable := false
		uncertain := false
		switch {
		case status == http.StatusBadRequest:
			code = "validation_failed"
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			code = "permission_or_hierarchy_denied"
		case status == http.StatusNotFound:
			code = "unknown_member_or_resource"
		case status == http.StatusTooManyRequests:
			code = "rate_limited"
			retryable = true
		case status >= 500:
			code = "discord_server_error"
			retryable = !irreversible
			uncertain = irreversible
		}
		return actionmods.DiscordError{
			Code:             operation + "_" + code,
			Message:          "Discord rejected the moderation request",
			Retryable:        retryable,
			OutcomeUncertain: uncertain,
		}
	}
	return actionmods.DiscordError{
		Code:             operation + "_network_error",
		Message:          "Discord request failed",
		Retryable:        !irreversible,
		OutcomeUncertain: irreversible,
	}
}
