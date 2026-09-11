// Package quack holds Quack's moderation use cases: the services that HTTP
// routes, Discord command handlers, and background workers call to create and
// read cases, evidence, appeals, templates, guild settings, audit history, and
// operational status.
//
// Everything here is transport-neutral. Services accept small consumer-defined
// repository ports (ports.go) and return plain response structs, so the package
// must not import discordgo, gorm, or gin. internal/store implements the ports;
// internal/discordbot and internal/httpapi map the responses. Domain records and
// storage parameter structs live in the model subpackage.
//
// File map by area:
//   - app.go, ports.go: Services composition root and repository ports.
//   - guilds*.go, guild_lifecycle.go, authorization.go, discord.go: live Discord
//     staff context, capability checks, case preflight, install and leave.
//   - template*.go, templates*.go: versioned policy validation and persistence.
//   - case*.go, cases.go: case creation, escalation, reads, notifications.
//   - evidence*.go: managed evidence channel and message or upload capture.
//   - action*.go, actions.go: leased enforcement execution and staff recovery.
//   - honeypot_*.go: system-triggered honeypot cases and their template.
//   - appeal*.go, appeals.go: member appeals, decisions, and outbox delivery.
//   - audit*.go, statistics.go: audit reads, staff-channel mirror, derived stats.
//   - settings.go: guild configuration writes and the starter-policy notice.
//   - ops.go: queue and action health for operators.
package quack
