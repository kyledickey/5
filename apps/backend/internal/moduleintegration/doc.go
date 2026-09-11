// Package moduleintegration wires the optional modules (tickets, general
// logging, honeypots) into the running bot. It is the only package that sees
// both sides: the core quack services and Discord session on one hand, and the
// small ports declared by the module packages on the other.
//
// It owns the Runtime that composes module services, stores and background
// workers; the Discord adapters implementing tickets.DiscordClient,
// generallogging.DeliveryClient and the honeypot CaseApplier/validators; gateway
// event routing; the /setup flows; ticket button and modal handlers; the HTTP
// mount for module routes; and the enablement validator used by core guild
// settings. Its own database access is limited to resolving guild identities
// (guildResolver) and appending core audit entries (moduleAuditor); module
// tables belong to the modules.
//
// internal/modules/* must not import this package (they receive its adapters
// through their own interfaces), and internal/quack must not either.
//
// File map (prefix = area):
//
//	runtime.go               Runtime, New, shutdown, background workers, moduleAuditor, guildResolver
//	runtime_guild_lock.go    per-guild serialization shared by setup and warning refresh
//	http.go                  RegisterHTTP: module routes, rate limit, idempotency, HTTP actor mapping
//	gateway_handlers.go      RegisterGatewayHandlers, RequiredGatewayIntents
//	gateway_messages.go      message create/update/delete routing to logging, tickets and honeypots
//	gateway_guilds.go        member, audit-log, guild and channel lifecycle events
//	discord_session.go       bot identity, permission computation, REST request options
//	settings_enablement.go   live validation before core settings flip a module's enabled flag
//	logging_client.go        generallogging.DeliveryClient over Discord
//	logging_setup.go         /setup logging handler
//	honeypot_adapters.go     case applier, template/channel validators, gateway message projection
//	honeypot_counter.go      warning-count presenter and deleted-warning repair
//	honeypot_warning.go      warning copy derived from live template policy
//	honeypot_presentation.go durable refresh loop and fenced replacement send
//	honeypot_recovery.go     saved-case lookup and live re-preflight for expired incidents
//	honeypot_setup.go        /setup honeypot handler
//	ticket_components.go     button/modal registration and ticket interaction handlers
//	ticket_discord_client.go tickets.DiscordClient: threads, replies, transcripts
//	ticket_setup.go          /setup tickets handler
//	ticket_setup_permissions.go  live bot permission preflight for entry and queue channels
//	ticket_entry_delivery.go entry panel publish and retire
//	ticket_queue_delivery.go staff queue publish and receipt verification
//	ticket_queue_reconcile.go    manual queue-post adoption checks
//	ticket_queue_recovery_ui.go  manager-only recovery buttons and modal
//	ticket_close_notice.go   member DM with transcript
//	ticket_close_progress.go closing feedback while the origin thread may vanish
//	ticket_detail.go         private ticket detail rendering
//	ticket_recovery.go       existing-ticket and close-failure copy
//	ticket_journal.go        gateway text retention for open threads
//	ticket_thread_members.go private-thread membership sync
//	ticket_thread_repair.go  gateway-triggered membership repair
package moduleintegration
