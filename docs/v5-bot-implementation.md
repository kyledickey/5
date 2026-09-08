# Bot rewrite implementation tracker

Authority: the answered [product interview](v5-product-interview.md), followed by
the user's request to implement Go only. The older v5 specification and review
describe prior behavior where they conflict with those answers. Dashboard work
is deferred; shared Go services and HTTP contracts still need the agreed behavior.

## Acceptance work

- [ ] Audit: only moderation, template/settings changes, appeals, tickets and
  failed actions. Separate delivery receipts; readable actor/member/case/rule/
  outcome/time; individual events; permission-checked retry buttons.
- [ ] Cases: immediate `/case add`, message and user context entry points;
  optional context/evidence added or edited afterward; view evidence/user/void
  controls; clear selected level, actions, notification and appeal status.
- [ ] Evidence: direct screenshot/file uploads and selected-message snapshots;
  capture before destructive enforcement; unavailable evidence never blocks a
  case; show preserved content and warnings to staff only. Recreate deleted
  storage channel but preserve administrator changes to existing channels.
- [ ] Templates: intuitive Discord creation/editing, editable starter policies,
  fixed reasons and engine-selected actions, notify enabled by default, optional
  per-template decay with all-time default. Edits affect future decisions only.
- [ ] Voids/recovery: void cancels pending work and attempts to undo active
  punishment; failures remain reviewable/retryable with live permissions.
- [ ] Appeals: one case, one submission, DM button/modal, no conversations or
  information requests; dedicated queue with accept/reject; accepting voids and
  reverses; anonymous member decisions and optional rejoin link.
- [ ] Tickets: read Legacy behavior; publish entry button; one private thread per
  member; native typing; owner or moderator closes; save queue transcript before
  deleting thread; no reopen, assignment, categories or conversation forms.
- [ ] Honeypot: Legacy-style channel/warning/counter setup, editable generated
  autoban template, moderator exemptions, one incident per burst, capture/delete
  trigger, immediate configuration effect and reviewable enforcement failures.
- [ ] General logging: Legacy event coverage in one configured channel; avoid
  duplicate Quack bans; normal disabled/unrouted events are not errors.
- [ ] Configuration/composition: one effective source for each setting, usable
  Discord setup and coherent feature wiring in one Go process.
- [ ] Storage/maintenance: simplify the pre-release migration structure and
  purpose-built package boundaries; preserve historical v4 case import and member
  history without counting imported cases toward v5 escalation; practical GoDoc.
- [ ] Presentation: preserve custom icons/plain text, concise natural copy,
  minimal anonymous member notices, paginated long records and web links where
  configured; no new dashboard code.
- [ ] Verification: focused tests then full backend suite at logical commit
  boundaries; live rehearsal in Quack's pond (1005778938108325970), using the
  authorized test bot/account; verify template → case → evidence → action,
  audit/retry/void, appeals, tickets and honeypot/logging journeys.

## Implementation notes

- Interview answers preserved in `751beb4` before implementation.
- Audit storage changes are implemented. Focused tests and `go test ./...` pass. Mirror delivery receipts have a separate
  table. Existing technical rows are excluded from staff queries. The current
  pre-release baseline was edited; local database reset/schema consolidation and
  runtime rehearsal remain pending.
- Implementation choices for open-ended answers: use optional per-template decay
  (zero means all time); preserve the current departed-target permission boundary
  until a concrete safe template workflow is implemented; use Discord attachment
  options for screenshots plus an add-context button for text/message links.
  These are implementation choices, not claimed completed behavior.

- Optional context and best-effort message evidence now pass focused and full Go
  tests. Missing adapters, deleted messages and transport failures retain a
  visible incomplete-evidence flag without dropping the moderation decision.
  Existing evidence channels are reused without edits. Saved text and capture
  warnings appear in staff detail. Direct uploads and post-creation editing are
  still pending; Discord creation still needs its mandatory form removed.

- Member case responses now omit staff context, evidence, events, level labels,
  correction notes and notification diagnostics. They include the template name
  and a small enforcement outcome. DMs no longer render staff context; a case
  without punishment is called a warning. Focused and full Go tests pass.

## Review finding coverage

The requested interview is present as `docs/v5-product-interview.md` (there is
no `docs/v5-interview.md` in this checkout). All 76 answers remain part of the
acceptance scope. Review findings map to implementation as follows; partial
means further work is required, not completion.

| Finding | Status / remaining work |
| --- | --- |
| 1 Audit noise | Storage fixed; readable enriched mirror and recovery buttons pending. |
| 2 Module switches | Pending single effective configuration source. |
| 3 Forbidden settings field | Pending Go contract correction; dashboard UI deferred. |
| 4 Missing appeal UI | Discord submission pending; web UI explicitly deferred. |
| 5 Disconnected Discord appeals | Pending submission, queue and decision wiring. |
| 6 CORS-based appeal URL | Pending explicit product URL configuration. |
| 7 Unpublished ticket entry | Pending setup and entry panel publishing. |
| 8 Ticket lifecycle mismatch | Pending open/close-only implementation and owner permission. |
| 9 Ticket transcripts | Pending native message capture and close/delete ordering. |
| 10 Honeypot intents | Pending startup/runtime intent correction. |
| 11 Honeypot recovery | Pending setup, debounce and incident recovery. |
| 12 Direct evidence uploads | Implemented on `/case add` and `/case evidence`; live rehearsal pending. |
| 13 Evidence feedback | Saved text/warnings, failure receipts, upload entry and dedicated view implemented; long-record pagination pending. |
| 14 Evidence preservation | Nonblocking capture, channel customizations and saved-message links fixed; orphan handling and live checks pending. |
| 15 Inconsistent case entry | Immediate slash/message creation implemented; user context entry and selector pagination pending. |
| 16 Fragile case drafts | Creation wizard/draft map removed; current saved context can be reopened and edited. |
| 17 Public deferred errors | Staff subcommands and evidence/history buttons are private; creation and other controls still need review. |
| 18 Stale result messages | Pending durable result refresh. |
| 19 Noisy process errors | Pending module non-event classification. |
| 20 Maintainability | Creation wizard removed; service composition, schema and stale docs still pending. |

- Immediate slash/message case creation and post-creation text context editing are
  implemented. Context edits are guild-scoped, require moderation permission,
  preserve enforcement and policy snapshots, and write a `case.update` event.
  The unused draft map, creation wizard and JSON slash option were removed.

- Direct screenshots/files now share the existing preservation limits and warning
  handling. `/case add file:` captures before enforcement; `/case evidence`
  appends to an existing case without rescheduling actions. Stored copies link to
  Discord messages instead of expiring attachment URLs. Case receipts and detail
  include context/evidence/user/void controls; staff reads and evidence responses
  are private. Focused tests include actual attachment option payloads and copy
  adapter responses; `go test ./...` passes. No live Discord rehearsal yet.

- Recovery retries now reject original punishments on voided cases at both the
  service and transactional storage boundaries. Storage locks the case before its
  action, matching void/claim ordering. Reversal retries use current reversal
  permissions, including unban for departed members; repeated requests also
  refresh permissions. Focused and full backend tests pass. Automatic reversal
  on void/appeal acceptance and late worker completion remain pending.

- Voiding a case and accepting its appeal now transactionally queue linked unban
  or timeout removal for successful original punishments. Pending original work
  and notices are cancelled; existing reversals remain intact. Late successful
  enforcement also queues removal, while late failures and expired workers cannot
  restart punishment on a voided case. Durable polling discovers queued removals.
  Workers check the requester's live permissions; a different authorized moderator
  can retry a failed removal. Case/appeal copy distinguishes voiding from removal
  completion. Regression tests cover both completion/void orders, deduplication,
  rollback on inverse storage failure, appeal linking, and permission failure then
  successful unban recovery. Live Discord rehearsal and enriched audit controls
  remain pending; existing Discord action retries are available on case detail.

- Appeals now collect one general statement. Old custom forms no longer affect
  new submissions, while historical form snapshots remain readable. Member reply,
  request-information, reopen, and custom-form editing routes/services were
  removed. Accept/reject are terminal at the storage boundary; close aliases
  rejection. Tests verify removed HTTP routes, one submission after rejection,
  terminal decisions, shared form behavior and existing acceptance/reversal.
  Focused tests and the full backend suite pass. Discord DM form and staff queue
  publishing/decision controls remain the next appeal integration work.

- Appealable case DMs now carry a Discord form button even when no website URL is
  configured. The one-statement modal checks the authenticated Discord identity
  on opening and submission, works for departed members, and gives explicit
  duplicate/ineligible feedback. Handler tests use real interaction payloads and
  migrated storage, covering another member's forged submission, duplicate sends,
  and a form submitted after voiding. Focused tests and the full backend suite
  pass. Staff queue publishing, decision buttons, setup and live rehearsal remain
  pending; this does not yet complete the appeal journey.

- Staff appeal notifications now render the stored statement, case number and
  rule with accept/reject buttons. Outbox delivery loads the current appeal, so
  delayed delivery after a decision does not offer stale decision controls.
  Every decision click resolves live Discord permissions; competing decisions
  remain fenced by storage. Successful clicks refresh the queue message and
  deliver a private receipt; member decision DMs still omit reviewer identity.
  Focused tests cover queue content, permissions, competing clicks and delayed
  outbox state; the full backend suite passes. Dedicated-channel setup, durable
  refresh after decisions from other interfaces, notification recovery, optional
  rejoin links and live Discord rehearsal remain pending.

- `/setup appeals channel:` now saves a dedicated private review destination with
  live Manage Server authorization and staff-channel validation. Appeal delivery
  no longer uses the audit mirror channel, and resolves configuration afresh on
  each send. The setting survives storage round trips and is cleared on channel
  deletion/rejoin repair. Focused and full backend tests pass. The disposable
  pre-release guild-settings baseline was extended; schema consolidation/reset
  and live setup verification remain pending. Previously failed notifications
  still need the recovery work listed above.

- Appeal notification delivery now distinguishes a leased item from an external
  send in progress. Stale leases cannot begin sending; interrupted sends retain
  an unknown-outcome failure instead of being resent automatically. Known staff
  channel/permission/rate-limit failures retry after a one-minute delay, allowing
  setup repair to recover undelivered queue entries. Blocked member DMs remain
  recorded failures. Tests cover safe retry timing, expired lease fencing and
  unknown outcomes; focused and full backend suites pass. A staff-facing recovery
  view for ambiguous notification failures and durable queue-message refresh
  remain pending, as does live verification.

- `/appeals` provides a private, paginated pending queue directly from storage,
  independent of notification delivery. It includes decision controls, refreshes
  live authorization on navigation, and recovers when intervening decisions
  shrink the queue. Private decisions offer the next pending appeal without
  attempting a public channel edit. Appeals now share the core service composition
  used by commands and module integration. Focused and full backend tests pass;
  tests cover undelivered submissions, pagination after a decision and revoked
  permissions. Notification failure diagnostics/resend controls, durable channel
  message refresh, rejoin links and live rehearsal still remain pending.

- Read Legacy ticket creation/closure before changing the workflow. Owners now
  use the normal Close control, and existing tickets remain closable when new
  tickets are disabled. Reopen service/route and Discord reply-modal controls
  were removed; members are directed to type in their thread. Daily limits and
  reopen-window settings were removed, retaining the one-active-ticket reservation
  and allowing retry after failed provisioning. Tests cover duplicate prevention,
  owner closure, unrelated-user denial, disabled-module closure and removed
  controls; focused and full backend suites pass. Queue transcript publication,
  delete-after-save ordering, thread-only setup, and consolidation of old
  resolved/cancelled persistence and HTTP closure paths remain pending.
