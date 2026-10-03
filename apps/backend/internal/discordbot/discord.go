// Package discordbot is the Discord adapter for Quack's core services. It owns
// the gateway session (Bot), the REST calls that enforce moderation actions,
// deliver member DMs, mirror audit events and appeal queue entries, preserve
// evidence, and validate staff channels, plus the gateway lifecycle handlers
// that keep guild records in step with Discord. Bot's methods satisfy the ports
// declared in internal/quack (DiscordClient, notification and mirror senders,
// evidence capture); they never contain moderation policy.
//
// Every REST call carries the caller's context and disables discordgo's own
// retries and rate-limit waits (singleAttempt) so retry policy belongs to
// Quack's workers, and irreversible operations are classified as
// outcome-uncertain rather than retried. Live REST reads establish current
// authorization; the gateway cache is used for display only.
//
// Sub-packages: interactions dispatches InteractionCreate events, commands
// defines the slash/context commands and their handlers, ui holds the response
// model, and ui/views renders domain responses into messages. This package
// registers the appeal components and is imported by commands; it must not
// import commands or views' callers back.
package discordbot

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack/actionmods"
)

const discordUserGuildsURL = "https://discord.com/api/v10/users/@me/guilds"

// Bot owns the Discord gateway session and adapts Discord operations to the
// ports in internal/quack. Session is required and is set by New; callers that
// build a Bot literal (module integrations) must supply one. HTTPClient is used
// only for OAuth user requests and evidence downloads and defaults to
// http.DefaultClient when nil. connected tracks gateway readiness for health checks.
type Bot struct {
	Session    *discordgo.Session
	HTTPClient *http.Client
	connected  atomic.Bool
}

// New creates a session for token with the Guilds intent and a bounded message
// cache, and registers the readiness handlers. It does not open the gateway;
// the runtime adds module intents first and then calls Open.
func New(token string) (*Bot, error) {
	session, err := discordgo.New(token)
	if err != nil {
		return nil, err
	}
	// Runtime adds only the intents required by currently enabled optional
	// modules before opening the gateway.
	session.Identify.Intents = discordgo.IntentGuilds
	session.StateEnabled = true
	session.State.MaxMessageCount = 5000
	bot := &Bot{Session: session, HTTPClient: http.DefaultClient}
	session.AddHandler(bot.gatewayReady)
	session.AddHandler(bot.gatewayResumed)
	session.AddHandler(bot.gatewayConnected)
	session.AddHandler(bot.gatewayDisconnected)
	return bot, nil
}

// gatewayReady marks the authenticated initial gateway session ready.
func (b *Bot) gatewayReady(_ *discordgo.Session, _ *discordgo.Ready) { b.connected.Store(true) }

// gatewayResumed marks a successfully resumed gateway session ready.
func (b *Bot) gatewayResumed(_ *discordgo.Session, _ *discordgo.Resumed) { b.connected.Store(true) }

// gatewayConnected marks DiscordGo's post-handshake synthetic connect event ready.
func (b *Bot) gatewayConnected(_ *discordgo.Session, _ *discordgo.Connect) { b.connected.Store(true) }

// gatewayDisconnected immediately removes Discord from process readiness while reconnecting.
func (b *Bot) gatewayDisconnected(_ *discordgo.Session, _ *discordgo.Disconnect) {
	b.connected.Store(false)
}

// Open connects to the gateway and marks the bot ready. Startup fails before
// serving traffic when Discord rejects the token or is unreachable.
func (b *Bot) Open() error {
	if err := b.Session.Open(); err != nil {
		return err
	}
	b.connected.Store(true)
	return nil
}

// Close disconnects the gateway once. It tolerates a nil receiver and a bot
// that was never opened so reverse-order shutdown can call it unconditionally.
func (b *Bot) Close() error {
	if b == nil || b.Session == nil {
		return nil
	}
	if !b.connected.Swap(false) {
		return nil
	}
	return b.Session.Close()
}

// Status reports gateway readiness, the bot's username and heartbeat latency
// for health endpoints. It is not ready until the session state has a user.
func (b *Bot) Status() (bool, string, int64) {
	if !b.connected.Load() || b.Session.State == nil || b.Session.State.User == nil {
		return false, "", 0
	}
	return true, b.Session.State.User.Username, b.Session.HeartbeatLatency().Milliseconds()
}

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
