# Testing

## Current Test Harness

The current test suite is driven through `go test ./apps/backend/...`.

Many storage and route tests use in-memory SQLite through GORM and, when Redis
behavior matters, a `miniredis` server through `apps/backend/internal/testutil/storage.go`.
This keeps most tests fast and self-contained.

The final release harness is `apps/backend/scripts/v5-readiness.sh`. `--local` runs the
composition, focused, race, full, vet, and four-command build gates. `--final`
also requires disposable `QUACK_TEST_MYSQL_DSN` and `QUACK_TEST_REDIS_URL`
targets and fails rather than treating missing external-storage evidence as a
skip. Manual and real-guild steps are defined in `release/rehearsal.md` and recorded
in `release/readiness.md`.

`apps/backend/internal/testutil/config.go` also installs a minimal test config so auth and
cookie-dependent code can run without the full production environment.

## Scope Limits

SQLite-backed tests are intended to catch:

- schema creation and reconciliation
- repository wiring
- route behavior
- case/template business rules
- action execution control flow

They are not a replacement for MySQL coverage. Any behavior that depends on
MySQL-specific SQL, indexes, locking, JSON semantics, unsigned integers, or
transaction isolation uses a real MySQL-backed integration test when
`QUACK_TEST_MYSQL_DSN` is configured. Those tests create and drop their own
uniquely named database rather than altering the database named by that DSN.

## Current Coverage Pressure Points

The current backend has a few areas where targeted tests matter more than broad
end-to-end setup:

- template validation and level normalization in `apps/backend/internal/quack/templates.go`
- case level selection and snapshotting in `apps/backend/internal/quack/cases.go`
- action retries, notifications, and failure handling in `apps/backend/internal/quack/actions.go`
- route auth and guild-context wiring in `apps/backend/internal/httpapi/routes/router_test.go`
- interaction dispatch, deferred responses, and component/modal routing in
  `apps/backend/internal/discordbot/interactions/dispatcher_test.go`
- Discord response helper output shape in `apps/backend/internal/discordbot/ui/responses_test.go`

Relevant files:

- `apps/backend/internal/testutil/storage.go`
- `apps/backend/internal/testutil/config.go`
- `apps/backend/internal/httpapi/routes/router_test.go`
- `apps/backend/internal/quack/templates_test.go`
- `apps/backend/internal/quack/cases_test.go`
- `apps/backend/internal/quack/actions_test.go`
- `apps/backend/internal/discordbot/interactions/dispatcher_test.go`
- `apps/backend/internal/discordbot/ui/responses_test.go`
- `apps/backend/internal/store/schema_test.go`
- `apps/backend/internal/readiness/v5_rehearsal_test.go`
- `apps/backend/scripts/v5-readiness.sh`
