# Quack v5 backend docs

Maintainer documentation for the Go backend in `apps/backend`. Everything in
this directory describes the code as it exists in this checkout. Product
definition, plans, and historical decision records live under
[`planning/`](planning/README.md) and are not kept in sync with the code.

## Start here

| If you want to… | Read |
|---|---|
| Understand how the process is assembled and how a request flows | [`architecture.md`](architecture.md) |
| Find the package that owns a behaviour | [`codebase-map.md`](codebase-map.md) |
| Run it locally | [`development.md`](development.md) |
| Configure it | [`configuration.md`](configuration.md) |
| Understand the tests and how to run the gated ones | [`testing.md`](testing.md) |

## Reference

- [`architecture.md`](architecture.md): composition root, layering rules, startup and shutdown order, HTTP and Discord request flows, the durable action queue.
- [`codebase-map.md`](codebase-map.md): every package, what it owns, what it may import, and the files to open first.
- [`http-api-platform.md`](http-api-platform.md): OAuth and session lifecycle, browser security, the stable error envelope, rate limits, HTTP idempotency.
- [`dashboard-api-policy.md`](dashboard-api-policy.md): endpoint policy matrix for the dashboard and internal adapters.
- [`appeals-and-member-access.md`](appeals-and-member-access.md): member-owned reads and the appeal state machine.
- [`audit-statistics-discord.md`](audit-statistics-discord.md): audit log contract, staff statistics, and the Discord audit mirror.
- [`migrations.md`](migrations.md): the two schema mechanisms (startup reconciliation and the frozen migration ledger), adoption, and rollback.
- [`v4-historical-import.md`](v4-historical-import.md): importing v4 history without letting it affect escalation.
- [`modules/README.md`](modules/README.md): focused notes on the case pipeline, action engine, queue, command registry, interactions, guild setup, and the optional modules.

## Operations

- [`operations-security.md`](operations-security.md): health endpoints, metrics, outage and recovery behaviour, shutdown.
- [`storage-recovery.md`](storage-recovery.md): MySQL backup and restore manifests, Redis recovery.
- [`release/README.md`](release/README.md): release readiness evidence, rehearsal protocol, and the infrastructure proposal.

## Planning and history

[`planning/`](planning/README.md) holds the product definition (`v5.md`), the
backlog, scope-drift audits, product interviews and reviews, execution plans,
and integration notes from the v5 build-out. They are kept for provenance.
When they disagree with the code, the code and the reference docs above win.
