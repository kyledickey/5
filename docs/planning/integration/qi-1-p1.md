# QI-1 P1 integration manifest

QI-1 combines the accepted QP-A core moderation, QP-B HTTP/auth platform, and
QP-C tickets/logging module heads on the accepted V5-003 anchor.

## Installed contracts

- `httpapi.NewPlatformRegistrar` owns trusted proxies and the global request,
  error, recovery, logging, security-header, CORS, body-limit, and CSRF order.
- Optional-module HTTP routes are mounted beneath authenticated, live guild
  context and reuse QP-B's Redis rate-limit and idempotency primitives.
- One module registry constructs ticket and general-logging services from the
  process database; the core repository is adapted only as an append-only audit
  sink.
- Ticket components share the command dispatcher. Gateway handlers feed a
  separate bounded logging queue and perform deleted-channel repair.
- Ticket transcript cleanup starts promptly, runs hourly, and stops with the
  process. Shutdown drains accepted logging work without entering the moderation
  action queue.
- Logical module migrations 0100 and 0110 are represented as frozen,
  checksum-bound central-ledger migrations at physical positions 0006 and 0007,
  immediately after QP-A's core moderation migration 0005.

## QP-A merge resolution

1. The accepted QP-A head is incorporated without rewriting any package head.
2. `apps/backend/internal/quack/app.go` preserves QP-A core services; optional
   modules stay process-composed and do not enter the moderation core.
3. QP-A's Discord moderation client additions coexist with QI-1's ticket and
   logging transport remains in `apps/backend/internal/moduleintegration`, so only command
   component registration should require registry reconciliation.
4. QP-A migration 0005 is followed by QI-1 physical migrations 0006
   and 0007 retaining logical identities 0100 and 0110. Filenames, embedded
   sources, tests, and checksum evidence move together.
5. `runtime.go`, `router.go`, and command registration are reconciled once;
   production routing mounts both QP-A registrars and module registrars before
   the wave-wide focused, race, MySQL, full test, vet, and binary build gates.

No QP-B or QP-C feature implementation is duplicated in this integration
package. Real Discord permission-changing checks remain a release validation
gate requiring authorized credentials.
