# QI-2 P2 integration manifest

QI-2 composes the P2 capability packages on accepted QI-1 head `11650a5`.
It contains accepted QP-F head `fc6d82c`, accepted QP-E head `f7ace2f`, and
accepted post-review QP-D head `24f3e4d`.

## Installed QP-F contracts

- `honeypot.Descriptor()` joins the one process registry and logical migration
  0300 is frozen as checksum-bound physical migration 0008 immediately after
  existing 0007. A later QP-D merge must reconcile logical 0200 into the next
  contiguous physical ledger position without editing an applied definition.
- Manager-only settings, status, update, and repair routes are mounted beneath
  the authenticated live-guild module group and reuse the shared rate-limit,
  idempotency, and error-envelope boundaries.
- Channel and template validators read current Discord/core state. Gateway
  message projections reload the current member, roles, channel permissions,
  bot identity, and guild state instead of trusting message-supplied claims.
- `honeypotCaseApplier` preserves QP-F's request into the narrow
  `CaseService.CreateSystemHoneypot` entrypoint. The entrypoint reuses the
  ordinary template compatibility, escalation, evidence, snapshot,
  transaction, action, notification, scheduling, idempotency, and bot/target
  safety paths while persisting an empty moderator and explicit system
  event/audit attribution. QP-A had no callable system boundary; QI-2 added
  this narrow integration-owned contract and rejects staff or non-honeypot use.
- Template updates/archives and Discord channel/guild deletions forward drift
  to the honeypot adapter. Only the matching guild configuration disables and
  its selected references remain available for repair.
- Startup intents are derived from durable enablement. Honeypots add Guilds and
  Guild Messages without Message Content; general logging adds its member,
  moderation, message, and privileged content requirements only when enabled.
- The isolated bounded honeypot runtime starts during composition, receives
  projected message events, drains accepted work during reverse-order graceful
  shutdown, and cannot close ticket, logging, action, or notification workers.

## Installed QP-D and QP-E contracts

- The live authenticated member and staff routers install QP-D's complete case
  ownership, appeal form, queue, decision, and explicit reversal surface with
  QP-B rate-limit and idempotency primitives.
- Logical appeal migration 0200 is frozen at physical ledger version 0009,
  after already assigned honeypot migration 0008, without editing an applied
  migration definition.
- One bounded appeal outbox worker uses leased claims and the Discord adapter;
  staff events resolve only to the configured staff-only audit channel.
- The central component dispatcher registers ticket, case moderator, and
  accepted-appeal reversal controls. QP-D's exported handler retains live
  moderator, bot, target, permission, and hierarchy authorization.
- Appealable case notifications use the structured secure dashboard entry
  message whenever production supplies an HTTPS dashboard origin.
- The authenticated guild router installs QP-E audit/statistics routes, the
  process starts and joins the audit mirror worker, and module audit entries use
  the canonical API/Discord/honeypot/import source resolver.
- QP-E's three Codex findings were integration-owned and are closed here by
  production registration of case components and statistics plus the bounded
  audit mirror lifecycle.

Combined focused, migration/MySQL, race, full test, vet, and binary build gates
are recorded on the QI-2 pull request.

Real Discord permission-changing checks remain a release validation gate that
requires explicitly authorized guild credentials.
