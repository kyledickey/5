# Quack v5 Readiness

Verdict: **NOT READY**
Combined implementation anchor: `final/qp-i-readiness` at `50d40de` plus this
review reconciliation
Last reconciled: 2026-07-12

The repository implementation and local strict validation are complete. Quack
cannot be declared READY because the required clean-install and Discord-action
rehearsal in a non-production guild has not been authorized or executed, and
no product owner has accepted that missing release evidence as a readiness
exception. `v5.md` requires exact new-guild bootstrap and Discord-authoritative
behavior, so adapter and isolated end-to-end tests do not prove the external
installation boundary. The QP-I Codex review-and-fix lifecycle is complete
with no second review request. Release-infrastructure changes
are explicitly deferred under the user's prohibition and are not represented
as passes.

[`v5.md`](../planning/v5.md) is authoritative. [`TODO.md`](../planning/TODO.md) and
[`v5-scope-drift.md`](../planning/v5-scope-drift.md) are supporting inventories. Missing
credentials, infrastructure, authorization, or execution is recorded as **NOT
EXECUTED**, never inferred from unit tests.

## Requirement matrix

| ID | Applicable `v5.md` requirements | Implementation and exact evidence | State |
| --- | --- | --- | --- |
| R01 | Main ideas and product principles: guild-owned rules, moderator-applied templates, Discord authority, understandable immutable history, guild isolation, no hard deletion | Canonical models and migrations 0001-0005; template/case/audit services; `core_moderation_test.go`, `migrations_test.go`, audit immutability tests | PASS |
| R02 | Owner/Admin, Manage Guild, Moderate Members, action-specific permission, live refresh, actor/bot hierarchy and all-or-nothing preflight | `authorization_test.go`, guild/Discord authorization tests, route permission tests, accepted V5-003/QP-A/QP-D/QP-E heads | PASS |
| R03 | Dashboard/Discord parity, member ownership after departure, no Discord template builder, internal dashboard/adapter HTTP boundary | Shared `quack.Services`, central route/command registrars, QP-B sessions/policies, QP-D member routes, `dashboard-api-policy-v5.md` | PASS |
| R04 | Template fields, identity/versioning, one default, archive/restore, policy-only confirmed import/export | Template service/store/routes, migration compatibility, logical 0410 constraints, template service/route/archive-only tests | PASS |
| R05 | All-time same-template non-voided escalation including the new case and exact starter policy | Guild bootstrap/lifecycle and case lock/selection tests, `TestMySQLConcurrentCaseCreationSelectsDistinctEscalation` | PASS in isolated adapters; real-guild starter rehearsal NOT EXECUTED |
| R06 | Unique immutable guild case number; valid/voided; void+replacement correction; privacy-safe member history | Case service/store/routes and target projection tests; QP-G recovery manifest and MySQL concurrency; `TestMySQLConcurrentCaseCreationAndVoidPreserveNumberingAndValidity` | PASS |
| R07 | Five structured context types; member-visible context; message/link evidence; managed copies and explicit partial capture | Context wizard/service, evidence parser/capture/store, managed-channel lifecycle; live/deleted/inaccessible/cross-guild/unsupported/oversized regressions in `evidence_test.go` | PASS in isolated adapters; real Discord copy NOT EXECUTED |
| R08 | Zero/one timeout, kick, ban; exact settings; target safety; safe retry and authorized manual recovery | Action adapters/services, migration 0410 action constraint, lease/fencing and recovery tests, Discord classification tests | PASS |
| R09 | At most one accurate post-outcome notification; no override; visible failure; retry/dismiss/void; limited public Discord result | Durable notification state machine, action/notification integration tests, moderator views/components | PASS in isolated adapters; real DM/action NOT EXECUTED |
| R10 | One case-linked appeal; snapshotted form; reopen/info; atomic accept+void; explicit authorized reversal | Migration 0200, appeal service/store/routes/outbox/components and full accepted/rejected/reopened/closed/concurrency tests | PASS; real-guild appeal/reversal NOT EXECUTED |
| R11 | Permanent complete audit, success/failure/denial/read events, all-moderator access, redacted mirror, derived statistics | QP-E audit vocabulary/store/service/mirror/statistics plus QI-2 runtime wiring and redaction/immutability tests | PASS |
| R12 | Tickets, general logging and honeypots remain isolated; honeypot alone applies a normal template; utilities do not shape core | QP-C/QP-F modules, QI-2 registrars/workers/migrations, module integration/isolation/privacy tests | PASS |
| R13 | V4 historical readable import with no escalation/action/notification; module-owned migrations; isolated coexistence and direct-command cutover | QP-G at `17f938b`, logical 0400/0410 registered as physical 10/11, importer/CLI/rollback/restore/command-scope tests and docs | PASS with sanitized fixtures; operator real-data import NOT EXECUTED |
| R14 | Every firm boundary: no cross-guild/template escalation, public automation API, moderator level/reason override, multi-action, severity/weight/window, notes, hard delete, Quack staff roles, Discord builder or audit/logging conflation | Canonical contracts, archive-only record, 0410 constraints, source/API policy scan, package isolation and security tests | PASS |
| R15 | Release quality: migrations, real storage, full test/vet/build/race, E2E, security, clean install/upgrade/restore/coexistence/shutdown and real-guild checklist | Strict `apps/backend/scripts/v5-readiness.sh --final` PASS; `apps/backend/internal/readiness/v5_rehearsal_test.go`; [`v5-rehearsal.md`](rehearsal.md); storage/ops runbooks | PASS local gates; real guild NOT EXECUTED, therefore release evidence incomplete |

## Supporting inventory reconciliation

The initial QI-2 audit found 122 unchecked TODO entries. No unchecked entry
remains. The external rehearsal is explicitly adjudicated, but not passed:

| TODO | State and owner |
| --- | --- |
| Clean install in a new Discord guild | DEFERRED / NOT EXECUTED; requires explicit user authorization and test guild/application credentials. `v5.md`'s join/bootstrap and Discord-authority rules make this missing release evidence, so the verdict remains NOT READY. |
| Final backend release checklist | COMPLETE in this document; local strict gates pass and external gaps are recorded without being promoted to passes. |

Infrastructure-only work is checked as explicitly deferred with its product and
authorization reason in `TODO.md`: CI jobs, scanners, coverage enforcement,
Docker/Compose mutation and Compose smoke. Exact proposed changes are in
[`release-infrastructure-proposal.md`](release-infrastructure-proposal.md).

The final scope-drift audit records no unresolved implementation mismatch.
Frozen legacy columns remain compatibility data only; logical 0410 converts
residual template deletion state to archive state and installs final database
constraints. V4 import and direct-command cutover are implemented and
documented. Optional modules remain separate.

## Pull-request and review audit

Every implementation PR received a Codex review lifecycle. PRs after the
single-review policy reset received exactly one request and no second request.
No unresolved actionable P0 remains.

| PR/package | Review result and disposition |
| --- | --- |
| #1 V5-001M | Legacy pre-policy multiple-round lifecycle; all migration checksum/rollback/frozen-schema findings fixed at `7608e1a` |
| #2 V5-001, #3 V5-001C, #4 V5-002, #5 V5-003 | Accepted canonical model/bootstrap/authorization heads with package gates green |
| #6 QP-C | Four P2 findings fixed/resolved at `4579d14` |
| #7 QP-B | P1/P2 findings fixed at `b2f3e0a` |
| #8 QP-A | Two P1/two P2 findings fixed or transferred; route-mount P1 closed in QI-1 |
| #9 QI-1 | Two P2 findings fixed/resolved at `11650a5` |
| #10 QP-F | One P2 fixed/resolved at `fc6d82c` |
| #11 QP-E | No package-local bug; registration/worker findings transferred and closed in QI-2 |
| #12 QP-D | Atomic-cancellation P1 and outbox-lease P2 fixed at `24f3e4d` |
| #13 QI-2 | Honeypot internal-guild identity P1 fixed at `6b999c1` |
| #14 QP-G | Exactly one review of `dd542c0`; recovery-manifest P2 fixed at `17f938b`; central-registry P1 transferred and closed by QP-I physical versions 10/11 |
| #15 QP-H | Exactly one review of `d66357a`; bearer-format replay identity P2 fixed by normalized session extraction, and stale Discord gateway readiness P2 fixed by transition handlers/tests; both threads resolved at `e3e01b6`, no second request. The later queue-fairness TODO audit gap is fixed at `44f18c9` and integrated at `73236be`. |
| #16 QP-I | Exactly one review of `fff053b`; three P2 findings were valid and fixed at `50d40de`: strict one-object-per-JSONL-line parsing, side-effect-free failed dry-runs, and exact case-create route classification. Focused/race/vet/diff validation passes; no second request. |

## Validation evidence

Passed evidence:

- Accepted QP-G: focused/race/full/real-MySQL/real-Redis/vet/build/diff;
  review-fix SQLite/MySQL/vet/build.
- Accepted QP-H: focused/race/full/vet/build/diff; review-fix targeted/race.
- QP-I after QP-G integration: central 0400 then 0410 registry, store and
  composition tests PASS; real MySQL `go test ./apps/backend/internal/store -count=1` PASS
  in 27.175 seconds.
- QP-H fairness follow-up at `44f18c9`: guild-rotating bounded selection,
  within-guild action priority, SQLite/MySQL targeted tests and race evidence
  PASS; integrated at `73236be`.
- QP-I combined-head focused config/HTTP/Discord/runtime/workqueue/store/
  readiness tests PASS after isolating the first pre-0410 route fixture.
- Final combined strict gate PASS:
  `QUACK_TEST_MYSQL_DSN=... QUACK_TEST_REDIS_URL=...
  GOCACHE=/tmp/quack-v5-qp-i-gocache ./apps/backend/scripts/v5-readiness.sh --final`.
  It passed focused readiness/quack/store/HTTP/Discord/module tests, targeted
  race tests, repository-wide `go test ./apps/backend/...`, `go vet ./apps/backend/...`, builds for
  `quack`, `quack-migrate`, `quack-v4-import`, and `quack-storage-verify`, the
  full MySQL/Redis-enabled repository suite, the repository-native two-process
  Redis write/verify persistence probe, and `git diff --check`.
- QP-I pre-review final static gates:
  `go vet ./apps/backend/...` PASS, `go build -buildvcs=false ./...` PASS,
  `bash -n apps/backend/scripts/v5-readiness.sh` PASS, `git diff --check` PASS.
- QP-I Codex fixes: `go test ./apps/backend/internal/v4import
  ./apps/backend/internal/httpapi/platform` PASS; the same packages under `go test -race`
  PASS; targeted `go vet` and `git diff --check` PASS.
- Tracked-file credential pattern scan and committed `.env` scan: PASS with no
  matches outside examples.
- Firm-boundary source scan: no live severity/weight/window/reason-override/
  multi-action controls, direct punishment command definitions, or live
  `Unscoped` template access. `gorm.DeletedAt` exists only in frozen migrations;
  the live record uses a compatibility pointer with no GORM delete semantics.

## External evidence and accepted exceptions

| Evidence | State | Dependency / adjudication |
| --- | --- | --- |
| Disposable MySQL/Redis final gate | PASS | Combined strict harness passed full MySQL/Redis-enabled tests and the native two-process Redis persistence probe |
| Sanitized representative v4 fixtures | PASS | Repository-owned non-sensitive fixtures; operator real-data run remains NOT EXECUTED |
| Clean install and actions in a real Discord guild | NOT EXECUTED | Requires explicit user authorization, non-production guild/application, safe test members and managed channels |
| Timeout/kick/ban/DM/evidence copy/audit mirror/reversal against Discord | NOT EXECUTED | Same external authorization and credentials |
| CI/scanners/coverage/Docker/Compose mutation and smoke | ACCEPTED DEFERRED SCOPE, NOT EXECUTED | User explicitly prohibited release-infrastructure changes; exact proposal documented |
| Production rollout/rollback | NOT EXECUTED | Requires release-owner authorization and production target; prohibited here |

## User completion conditions

| Gate | Current result |
| --- | --- |
| TODO complete or product-reason adjudicated | PASS: all entries complete or explicitly deferred with the authorization/product reason; deferral is not represented as release evidence |
| Scope drift resolved or accepted | PASS for implemented repository behavior |
| Every applicable `v5.md` requirement checked | PASS inventory R01-R15; R15 evidence incomplete |
| Every slice validation passed | PASS, including QP-I strict final gate |
| Every slice review/fix lifecycle complete | PASS; PR #16 received one review and all three P2 findings were fixed and revalidated without a second request |
| No actionable P0/P1 review finding | PASS on accepted heads; QP-G transferred P1 closed centrally |
| Newly discovered work completed/adjudicated | PASS: queue fairness integrated and validated |
| Repository-wide applicable local gates pass | PASS, including MySQL/Redis, race, vet, builds, migrations and isolated E2E |
| Final readiness matrix exists | PASS, this document |
| Final READY/NOT READY report with evidence | **NOT READY**: real-guild install/actions are NOT EXECUTED without an accepted exception |

## Publication lifecycle

PR #16 is open from `final/qp-i-readiness` into `integration/qi-2-p2`. Its one
Codex review-and-fix lifecycle is complete. Do not merge the PR or change
release infrastructure/settings without explicit authorization.
