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
