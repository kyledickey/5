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

- Ticket closure now locks the thread before capture, retains the transcript in
  storage, uploads it to the original staff queue message, saves the delivery
  receipt, and only then deletes the thread. Upload failures preserve the source;
  deletion retries reuse the receipt. Deleted queue messages are recreated with
  the full transcript. Queue destinations are checked for current guild ownership
  and privacy before delivery. Opening returns the created thread even if the
  staff queue send fails. Focused lifecycle and real Discord multipart transport
  tests pass, as does the full backend suite. The migration preservation fixture
  explicitly seeds the older schema before the new queue-channel column exists.
  Thread-only setup, recovery controls, closure-state/HTTP consolidation and live
  Discord verification remain pending; this code has not replaced the running bot.

- New tickets always use private, non-invitable threads; the alternate private
  text-channel setting and creation branch were removed. Entry channels must be
  text channels in the current guild. The opening message mentions only the owner,
  invites them to type normally while waiting for a moderator, and includes the
  ticket controls. A greeting failure does not suppress the staff queue send or
  hide the saved thread. Transport tests verify the actual thread type and welcome
  payload, including restricted mentions. Focused tests and the full backend suite
  pass. Custom staff-role configuration, setup/panel delivery and live verification
  remain outstanding.

- Removed custom ticket staff-role settings and the requirement to configure them.
  Queue messages now offer Join thread; joining uses freshly resolved moderation
  authority, verifies guild-scoped ticket access, and rejects closed tickets.
  Permission repair checks only existing thread members against live guild roles,
  removing former moderators while preserving the owner, bot and guild owner.
  It no longer scans the entire guild or invites every staff member at opening.
  Background repair targets open tickets rather than deleted historical threads.
  Removed obsolete private text-channel ACL construction and its matching test.
  Regression tests cover unauthorized/closed joins, current moderator retention,
  former moderator removal, and configuration without custom roles. Focused and
  full backend suites pass. Setup/panel delivery, recovery controls, closure-state
  consolidation and live verification remain pending.

- `/setup tickets entry:… queue:…` now reaches the module integration through an
  explicit command-handler dependency. It refreshes Manage Server authority,
  validates the entry's guild/type and the queue's current staff-only privacy,
  preserves unrelated ticket settings, enables tickets and publishes the member
  opening button. Publication failure reports that settings were saved and gives
  a concrete retry instruction. A command-routing regression test and the full
  backend suite pass. End-to-end setup transport/live validation, bot thread
  permission preflight and updating an existing panel instead of posting another
  remain pending; the running bot has not been restarted.

- Ticket setup now checks the bot's effective entry and queue permissions from
  fresh guild, bot-member and channel REST responses before saving configuration.
  The entry requires private-thread creation, thread posting/management and normal
  message/history access; the queue requires message/history access and transcript
  attachments. Failures identify the missing permissions and channel. Transport
  tests prove channel overwrite denials beat stale cached administrator authority
  and cover both thread-management and transcript-upload failures. Focused and
  full backend suites pass. Panel reuse and full live setup rehearsal remain pending.

- Ticket setup persists its entry-panel message/channel receipt and edits the same
  panel when rerun. An explicit deleted-message response recreates the panel;
  permission/transport failures do not fall back to another send. Receipt storage
  locks the current module configuration and rejects a stale channel selection,
  preserving other settings without emitting another staff audit event. Tests
  cover reuse, deletion, forbidden edits and delayed receipt writes. Focused and
  full backend suites pass. Concurrent first-time setup, disabling an old panel
  when moving channels, and live setup rehearsal remain pending.

- Transcript capture now includes readable author names with Discord IDs, orders
  equal timestamps by message snowflake, deduplicates overlapping history pages,
  and rejects malformed/non-advancing pages before closure can delete the thread.
  Attachment metadata includes its original URL with an explicit expiry caveat;
  this does not preserve attachment bytes. Transport tests cover overlapping and
  repeated pages, ordering and attachment context. Focused and full backend suites
  pass. Durable attachment capture and any desired deleted/edited-message history
  still require separate work; live transcript rehearsal remains pending.

- Honeypot claims now group distinct messages from the same member/channel within
  30 seconds into one incident. A configuration-row lock serializes claims across
  workers; external case application occurs after the transaction. Pending and
  created incidents suppress bursts, while failed case creation permits a later
  message to try again. Expected exemptions, duplicates and disabled/non-trigger
  events no longer become worker error logs. Tests cover concurrent distinct-message
  bursts, window expiry, failure recovery and draining independent members. The
  full backend suite passes. MySQL concurrency/live Discord verification, trap
  setup/counter, evidence deletion ordering and broader honeypot repair remain pending.

- Honeypot incident claims now revalidate enabled state, selected channel/template
  and exemptions under the configuration lock, rejecting jobs based on stale
  settings before persisting an incident. Temporary template lookup errors record
  a failed trigger but keep the trap enabled; only confirmed template unavailability
  disables it. Compatibility-review errors are classified as confirmed unavailable.
  Tests cover disabled/moved/reconfigured traps, new exemptions and automatic
  recovery after a temporary lookup failure. Focused and full backend suites pass.
  Changes after an incident has already been claimed still require completion of
  that accepted operation. Setup/counter and live verification remain outstanding.

- Gateway subscriptions are now stable for bot features rather than derived from
  which optional modules happen to be enabled at startup. Guild/member/moderation,
  message and message-content intents support later enablement plus evidence,
  transcripts and permission repair. Guild configuration still gates processing.
  Honeypot gateway handling checks the current trap channel and bot/webhook
  exemptions before Discord REST lookups, avoiding three requests per unrelated
  guild message. Template-change callbacks now disable only confirmed unavailable
  templates, preserving the transient-error behavior introduced above. Tests cover
  subscriptions before/after enablement and channel changes taking effect on the
  next event without unrelated REST calls. Focused and full backend suites pass.
  Actual privileged-intent availability and live gateway rehearsal remain pending.

- Honeypot moderator exemption now follows guild-level Moderate Members,
  Administrator and server ownership. Channel overwrites cannot manufacture a
  bypass or strip a moderator exemption; Manage Server, Kick or Ban alone no
  longer grant the moderation baseline. Projection rejects absent/mismatched
  guild state. Tests cover the authority combinations and channel grant/denial
  cases; focused and full backend suites pass. Existing custom role-exemption
  configuration and trap setup/counter still need reconciliation with the final
  simple workflow, followed by live verification.

- Removed honeypot custom role exemptions from configuration, message projections
  and validation. Trap bypass now depends on live moderator authority or bot/
  webhook identity. Old stored exemption lists are ignored rather than continuing
  to grant ordinary members a hidden bypass. Regression coverage checks old JSON
  settings alongside the existing moderator/bot exemption tests. Focused and full
  backend suites pass. Trap creation/default template/counter and live journeys
  remain pending.

- Added the normal template-service helper for honeypot setup: an initial editable
  ban policy with member notification and appeals enabled, using standard template
  validation/persistence/audit. Repeated use preserves administrator edits and
  version; archived templates require explicit restoration. A regression test
  creates the default, edits it to an actionless warning and proves reuse does not
  restore the ban. Focused and full backend suites pass. This helper is not yet
  connected to the Discord setup command; trap creation/warning/counter, concurrent
  setup handling and live rehearsal remain pending.

- Connected `/setup honeypot` through named module setup handlers. It resolves live
  Manage Server authority, creates/reuses the normal editable template, creates or
  reuses the trap channel, posts/edits a mention-suppressed warning and enables the
  trap only after warning delivery. Custom warning text is optional. Existing
  channel names and selected policy edits are preserved. A newly created channel
  reference is saved disabled before delivery so ordinary retry can reuse it.
  Setup displays the current created-incident count; live counter refresh is not
  wired yet. Command routing and the full backend suite pass. Full setup transport
  tests, permission preflight, concurrent/ambiguous setup recovery, live counter
  updates and live rehearsal remain pending.

- Successful honeypot incidents now refresh the configured warning's count through
  a presentation observer after durable case/trigger completion. The updater reads
  current settings and stored counts, serializes updates per guild, validates the
  destination guild and suppresses mentions. It reports incidents rather than bans
  because the policy is editable. Counter failures remain developer warnings and
  cannot repeat or undo moderation. A regression test proves a failed update plus
  replay still creates only one case. Focused and full backend suites pass. Counter
  transport/live verification, automatic missing-warning repair and setup/update
  concurrency still need coverage; broader honeypot live rehearsal remains pending.

- The production honeypot case adapter now deletes the triggering message only
  after the normal case path returns a saved case, following its optional evidence
  capture and persisted capture outcome. Failed creation leaves the source message;
  deletion failure is a developer warning and does not turn a saved case into a
  retryable application failure. Already-missing messages are harmless. Transport
  tests verify ordering and denied cleanup isolation; focused and full backend
  suites pass. Cleanup of additional debounced messages, durable deletion retries,
  missing warning repair and end-to-end live evidence/cleanup rehearsal remain pending.

- Honeypot validation now requires a same-guild text channel and effective View
  Channel, Send Messages, Read Message History and Manage Messages permissions.
  Setup performs this check before warning delivery/enabling, with repair guidance.
  Fresh bot membership/guild reads now use the caller's context and explicit no-
  retry request options. Transport tests cover missing history/cleanup permissions
  and invalid channel types. Focused and full backend suites pass. Permission drift
  during an already accepted incident, cleanup recovery and live rehearsal remain
  pending along with the rest of the tracked bot rewrite.

- Added `/setup logging channel:…` with live manager authorization and the logging
  service's destination validation. Setup routes every currently supported event
  category to the selected shared channel, enables available message/attachment/
  embed context and preserves cache/retry bounds. Routing tests cover moving all
  categories together and command dispatch; the full backend suite passes. Internal
  per-event route storage still needs simplification, along with full legacy event
  parity, Quack-ban suppression and live logging rehearsal. Module setup currently
  reuses the existing guild-authority task helper; its ticket-specific naming/types
  remain cleanup work.

- General ban/unban logging now consumes Discord audit-entry gateway events so
  actor attribution is explicit. Quack-authored actions are omitted; external
  actions preserve the moderator, target, reason and source entry separately.
  Removed registration of unattributed ban lifecycle handlers. Logging setup now
  checks fresh View Audit Log authority for the bot, explaining its purpose when
  missing. Tests cover own-action suppression and external ban/unban attribution;
  focused and full backend suites pass. Audit-entry gateway delivery, existing
  configurations missing the permission, reconnect replay behavior and live
  ban/unban rehearsal remain pending.

- General log presentation now explicitly renders ban/unban actor and target,
  deletion counts, available cached-content counts and channel lifecycle names.
  Arbitrary metadata is no longer printed as internal labels; reasons remain
  visible and source audit-entry identifiers stay out of staff-facing copy.
  Message edits use Before/After labels and attachments use a Files label.
  Tests cover moderation attribution, metadata omission and actual gateway channel
  operation values. Focused and full backend suites pass. Long-content delivery,
  remaining legacy event coverage and live visual review remain pending.

- Verified long general logs through the actual multipart Discord transport: full
  rendered content is retained in the existing text-attachment fallback. Logging
  now requires fresh View Channel, Send Messages and Attach Files permissions and
  shares the current moderator-only channel validator used elsewhere. Removed the
  obsolete module-local ACL checker and its cached-permission path. Transport tests
  cover retained tail content and refusal before send when attachment permission
  is missing. Focused and full backend suites pass. Live message rendering/delivery
  and remaining legacy event coverage are still pending.

- Message-edit logging now captures the previous cache version atomically before
  storing the update and queues a complete immutable snapshot. Delivery does not
  enrich it from a newer cache version, including when the original content was
  empty. Metadata-only updates without an edit timestamp and unchanged text are
  skipped. Partial updates preserve cached author/channel identity, and deletion
  enrichment restores the cached author. Tests queue multiple edits before delivery
  and verify exact before/after text. Focused and full backend suites pass. Attachment-
  only edit coverage, unknown-versus-empty display and live event rehearsal remain
  pending with the other tracked feature work.

- Edit snapshots now compare attachment identity as well as text, retaining both
  previous and current file metadata. Same-name replacements and file removal no
  longer disappear as unchanged-text updates. Staff logs show files before/after
  and distinguish known empty original text from unavailable history. Tests cover
  replacements, removal and display states; focused and full backend suites pass.
  Raw partial gateway payload behavior and live attachment edit/delete rehearsal
  still need verification; attachment bytes are not archived by general logging.

- Added the native user-context command Create case for member. It takes the target
  from Discord's resolved selection, applies the sole active policy immediately or
  presents a private template picker, refreshes live authority on selection, and
  creates through the normal case service without invented message evidence.
  Results reuse the existing public action-status presentation and optional later
  context/evidence controls. A regression test follows picker selection through
  creation for the intended member; the full backend suite passes. Picker paging
  beyond 25 templates, acknowledgement timing and live context-menu rehearsal still
  require work alongside the remaining tracked requirements.

- Both member and message context menus now share a paginated template picker,
  making every active template reachable beyond Discord's 25-option limit. Page
  navigation acknowledges immediately, refreshes live staff authority and reloads
  current templates; stale page positions clamp when templates are removed.
  Regression tests cover 51 templates, navigation boundaries, preserved member and
  message targets, empty lists and registered handlers. Focused and full backend
  suites pass. Initial context-command acknowledgement timing and live Discord
  rehearsal remain pending alongside the other tracked requirements.

- Selecting a template from either context-menu picker now acknowledges before
  live permission and policy lookups. The deferred task explicitly authorizes case
  creation, so revoked moderators receive a permission error before policy access.
  Message-case creation errors use the normal readable case error presentation.
  Tests prove the initial callback needs no services and both deferred paths reject
  revoked authority. Focused and full backend suites pass. Initial context-command
  lookup timing, private failure presentation and live rehearsal remain pending.

- Added the optional accepted-appeal rejoin invite to shared guild settings and
  `/setup appeals rejoin`. Omission preserves the current value; `none` removes it.
  Only HTTPS Discord invite URLs are accepted and normalized. Accepted decisions
  snapshot the link in their durable member notification with conditional wording
  that does not claim punishment removal already succeeded. Rejected decisions do
  not append a link. Persistence/removal/invalid-destination tests and the accepted
  decision outbox test pass, along with the full backend suite. The pre-release
  schema baseline includes this field; the planned local database reset and live
  setup/DM/rejoin rehearsal are still pending. Invite validity is controlled by
  Discord and is not guaranteed by URL validation.

- Appeal decisions now durably refresh the original staff queue notification,
  including decisions made outside that queue message. Staff delivery retains its
  channel/message receipt; decisions mark it for refresh without invalidating an
  active delivery lease. A decision arriving during delivery schedules a subsequent
  pass, preventing an older pending snapshot from becoming the final queue state.
  Discord delivery edits existing messages, safely retries failed edits, and only
  recreates an existing destination message after explicit Unknown Message. New
  sends retain the conservative ambiguous-outcome handling. Store race tests and
  transport edit/recreation tests pass, as does the full backend suite. Queue moves
  publish to the newly validated destination; cleanup of old-channel copies,
  unknown-send manual recovery, database reset and live rehearsal remain pending.

- Case, action and appeal audit mirrors now resolve guild-owned case details:
  case number, affected member and the immutable template name. Action outcomes
  name the actual action, system actors display as Quack, and unresolved failed
  executions offer the existing live-authorized retry control. Completed/dismissed
  actions and voided original punishments do not offer retry; failed reversals do.
  Enrichment excludes evidence/context and rejects cross-guild references. Tests
  cover snapshot names, tenant boundaries, current retry eligibility and component
  routing; focused and full backend suites pass. Template/ticket-specific enrichment,
  live audit rendering and destination repair behavior remain pending.

- Audit delivery no longer erases the configured destination when live validation
  or Discord delivery reports an inaccessible channel. The prior behavior could
  turn a temporary permission/network failure into permanently skipped later events.
  Failed events retain their retry receipt and resume delivery to the same channel
  after repair. The worker regression verifies retained configuration, recovery
  delivery and no repeat after success. Focused and full backend suites pass.
  Explicit channel deletion handling and live destination setup/recovery remain
  separate pending checks; ambiguous successful-send receipt loss is still tracked.

- Added `/setup audit channel` for the core moderation history destination,
  independent of optional general logging. It acknowledges privately before live
  authorization, requires current Manage Server authority, validates the shared
  staff-channel boundary and saves through audited guild settings. Workers read
  the new destination without a restart. Command tests cover registration, private
  deferred feedback, successful persistence, revoked manager authority and rejected
  destinations. Focused and full backend suites pass. Live registration/delivery
  and explicit bot send/attachment permission preflight remain pending.

- Shared staff destination validation now checks Quack's fresh REST membership
  and channel overwrites for View Channel, Send Messages, Read Message History
  and Attach Files. Audit/appeal setup and delivery, ticket queues and general logs
  all use this boundary, including long-record attachments. Evaluation uses an
  isolated state rather than cached gateway authority. Tests remove each required
  permission while the gateway cache still grants ownership and verify rejection;
  transcript/log transport regressions and the full backend suite pass. Live
  configuration and permission-repair rehearsal remain pending.

- Ticket HTTP closure now uses the same Discord adapter as the bot controls:
  freeze, capture the actual thread, persist and publish the transcript, then
  delete the thread. `/close` is canonical; `/resolve` and `/cancel` share this
  operation and no longer accept caller-authored transcript substitutes or bypass
  cleanup. All aliases use current owner-or-moderator authorization before
  idempotency replay. Without a Discord closer, reads remain available and closure
  returns unavailable. Route tests exercise every alias through failed publication,
  retained real transcript and successful retry; focused and full backend suites
  pass. Legacy service lifecycle simplification, concurrent closure behavior and
  live ticket rehearsal remain pending.

- Ticket close pipelines are serialized per guild/ticket in the shared Discord
  adapter used by both components and HTTP. Simultaneous closes reuse the stored
  transcript/queue receipt instead of racing capture, transition and deletion.
  Different tickets remain independent; waiting requests honor cancellation and
  reference-counted lock entries are removed when idle. A 12-caller regression,
  cancellation/independence checks and the ticket package race suite pass; the full
  backend suite also passes. This follows the required single-process runtime;
  multi-process coordination is not introduced. Live close/retry rehearsal and
  remaining ticket lifecycle simplification are still pending.

- Resolving a ticket now retains its member reservation until the transcript has
  a durable queue receipt and Discord thread deletion succeeds. Upload or deletion
  failures cannot create a second active thread for that member. Final release is
  conditional on the original ticket ID, so repeated closes cannot release a newer
  reservation. Tests cover both failure stages, recovery and an old-close/new-ticket
  race boundary; focused and full backend suites pass. Historical cancelled-state
  simplification, owner-facing recovery feedback and live rehearsal remain pending.

- Duplicate ticket opening now resolves the caller's own durable reservation and
  returns the existing thread link. A ticket awaiting closure cleanup instead
  offers Finish closing through the existing authorized close handler; provisional
  openings show a brief wait message. The lookup is scoped to guild and member,
  and does not expose another member's ticket. Ownership/isolation and presentation
  tests pass along with the full backend suite. Live recovery UX, direct close
  error feedback and remaining lifecycle simplification are still pending.

- Failed ticket closes now return private, stage-aware recovery feedback with a
  Retry close control for already-authorized callers. Copy distinguishes capture/
  close failure, captured transcript awaiting staff-queue delivery, and a saved
  transcript awaiting final cleanup. The adapter retains the authorized record on
  pre-persistence transport failures; missing or denied tickets expose no recovery
  controls. Focused presentation and closure tests plus the full backend suite pass.
  Live ticket recovery rehearsal and remaining legacy lifecycle cleanup are pending.

- Removed the legacy service cancellation path that could close records without
  captured transcripts, along with the redundant Discord cancellation method.
  Ticket persistence now has one explicit capture/close transition requiring a
  transcript, preserving the member reservation until adapter cleanup. Imported
  cancelled history remains readable. Existing closure, retry, ownership and import
  tests plus the full backend suite pass. Live ticket rehearsal and the remaining
  cross-feature requirements are still pending.

- Honeypot incident-counter refresh now recreates an explicitly deleted warning
  from saved text and durable incident counts, or restores a missing receipt.
  Ordinary Discord edit failures do not create duplicate warnings. Replacement
  receipts update only the current enabled warning configuration under a row lock,
  preserving concurrent administrator edits; presentation repair produces no staff
  audit event. Setup and recovery share the default warning text. Transport and
  stale-configuration tests plus the full backend suite pass. Immediate deletion
  event handling, setup/counter concurrency and live warning recovery remain pending.

- Honeypot Discord setup and incident-counter delivery now share a per-guild gate
  before loading settings, preventing stale counter edits from racing setup or
  concurrent setup requests from duplicating warning/channel work. Waiting honors
  cancellation and other guilds remain independent. Runtime wiring shares the gate
  with the counter observer; serialization/cancellation tests and the module race
  suite pass, as does the full backend suite. Live simultaneous setup/incident
  rehearsal, immediate deletion handling and remaining feature work are pending.

- Single and bulk gateway message deletions now restore a deleted configured
  honeypot warning without waiting for another incident. Recovery shares setup/
  counter coordination, reloads current settings, and ignores unrelated IDs and
  stale events for already-replaced messages. Work has a bounded deadline and runs
  independently of general logging configuration. Transport tests verify matching
  bulk IDs, unrelated deletion suppression and stale-event suppression; focused and
  full backend suites pass. Live gateway deletion/recovery and remaining feature
  acceptance checks are still pending.

- Added `/template create` with a short name/reason modal and native choices for
  warning, timeout, kick or ban. Timeout minutes are bounded; new policies activate
  immediately with member notifications and appeals enabled. The form opens without
  network work; submission rechecks live Manage Server authority and uses the
  existing template validation/audit service. Tests exercise each outcome through
  persisted policy creation and reject revoked manager authority. Focused and full
  backend suites pass. Discord escalation editing, decay configuration, full policy
  management and live template-to-case rehearsal remain pending.

- Added `/template level` with active-template autocomplete and native outcome
  choices. Administrators specify the human case number: 1 edits the default;
  3 stores the engine's two-prior-case escalation threshold. Reusing a threshold
  edits that level while preserving other levels, notification choices and rule
  fields. New levels notify members by default. Current manager authority is checked
  after a private acknowledgement. Persistence regression tests and the full backend
  suite pass. Level removal, broader template editing, concurrent edit protection,
  decay configuration and live policy-to-case rehearsal remain pending.

- Template edits now compare the source version before replacing policy children
  or appending a success audit. `/template level` copies one exact response snapshot
  instead of exporting a second read, and tells managers to rerun a conflicting
  edit. HTTP callers may supply `expected_version` and receive 409 on conflict;
  older callers omitting it receive protection only from the service's initial
  read onward. A persistence regression verifies stale edits preserve the winning
  name, escalation, version and audit count. Focused and full backend suites pass.
  Broader native template management, decay and live rehearsal remain pending.

- Added native `/template view`, `edit`, `remove-level`, `archive` and `restore`
  controls. Managers can change rule names, member reasons and appeal choices;
  `/template level` also accepts a per-level member DM choice. Views display
  human case thresholds and outcomes, while archive/restore retain the same rule
  identity. Autocomplete includes archived rules for inspection/editing/restoration
  and filters them out of active outcome controls. All handlers acknowledge
  privately before checking live Manage Server permission; policy edits use the
  existing optimistic version guard. Persistence lifecycle tests cover edits,
  per-level DMs, default-level removal rejection, escalation removal, archive
  exclusion and restore; revoked-manager tests cover each management command.
  Focused and full backend suites pass. Opt-in decay, live command usability and
  end-to-end policy-to-case rehearsal are still pending.

- Corrected native threshold conversion after tracing actual case selection: the
  engine includes the newly created case, so a third-case escalation stores 3,
  not 2. Native editing, removal and display now use that same count. This corrects
  the earlier ledger note describing a two-prior-case storage threshold. A new
  command-to-case regression creates three real cases and proves the first two
  remain warnings and only the third escalates. Focused and full backend suites
  pass. No running bot had loaded the earlier native command implementation.

- Implemented interview Q10's opt-in decay as a per-template rolling window in
  days. Zero retains all-time counting; `/template edit decay-days` configures it
  and `/template view` explains it. Cases older than the inclusive cutoff stop
  contributing to the next outcome but remain in history, with past actions and
  policy snapshots unchanged. Turning decay off restores all-time counting.
  The field survives guarded policy edits, storage, import/export and immutable
  case snapshots. The pre-release baseline schema includes a zero default; the
  previously planned local reset is still required before live testing. Tests
  cover exact cutoff inclusion, aged history and future enforcement, unchanged
  snapshots, import/export, invalid windows and native enable/disable. Focused
  tests and the full backend suite pass. Live Discord rehearsal and the broader
  remaining review findings are not yet verified.

- Both native context-menu entry points now acknowledge privately before live
  guild/permission/template lookups. Multiple templates produce a private picker;
  a sole template still creates the case immediately and posts a public result
  after completing the private acknowledgement. Initial lookup/authority failures
  remain private. Successful public followups remove the private copy; publication
  failure leaves the created case visible privately. Result refreshes now address
  the actual public message ID rather than always editing the original response.
  Tests cover deferred lookup timing, revoked authority, picker edits and sole-rule
  public visibility. Focused and full backend suites pass. Selected-template
  failure privacy, durable result refresh and live Discord verification remain
  pending, along with the remaining review requirements.

- User/message template selections now defer privately through authorization,
  template lookup and creation, then publish successful cases through the same
  private-receipt/public-result path as sole-template context commands. Removed
  the nested user-case handler/task invocation. Public publication failures keep
  the successful private case receipt with a clear explanation; private-copy
  cleanup failures no longer trigger a generic failed-command replacement after
  moderation has committed. Tests verify private selection acknowledgements,
  revoked-authority rejection without public output, successful public results,
  and publication/cleanup failure preservation. Focused and full backend suites
  pass. Durable result delivery/refresh, live rehearsal and remaining review
  requirements are still pending.

- Inspected the user-owned `quack` tmux session: pane `%1` still runs the old
  `quack-original-...` bot; pane `%3` runs the dashboard. Local Compose MySQL 8.4
  and Redis are healthy. No process was interrupted or database reset. Built the
  current backend to `/tmp/quack-v5-rehearsal` for the later runtime switch.
  Enabled the optional MySQL integration tests against isolated, automatically
  removed test databases. Corrected an outdated evidence fixture to supply the
  explicit actor identity required by the live authorization boundary. Added a
  real-MySQL concurrent template-edit regression proving one winning version,
  one rejected stale edit and one success audit. Focused MySQL checks and the
  complete `go test ./...` suite with `QUACK_TEST_MYSQL_DSN` configured pass.
  The database reset/schema simplification, runtime switch and actual Discord
  journeys are still pending; this verifies database behavior, not live UX.

- With explicit runtime-switch approval, stopped the old beta process, backed up
  local data to mode-0600 `/tmp/quack-before-v5-rehearsal.sql`, reset only the local
  `quack` database and migrated the current schema. The current backend connected
  to Beta Bot, MySQL and Redis. Live startup exposed a concurrent guild-bootstrap
  MySQL deadlock; bootstrap now retries only MySQL's fully rolled-back deadlock
  victims, with bounded context-aware delays and fresh per-attempt result flags.
  A 16-caller, four-guild MySQL regression and the full MySQL-enabled suite pass.
  Subsequent startup exposed command fingerprint churn and an unhandled Discord
  rate limit, currently being fixed before the feature rehearsal proceeds. The
  beta process is stopped at that registration failure; the new database is intact.

- Fixed the next live-startup failure: Discord omits `dm_permission` on guild
  command reads, so fingerprints now exclude that inapplicable field only for
  guild scope. Global comparisons still preserve DM policy. Registration clients
  honor Discord's explicit rate-limit cooldowns instead of aborting startup on a
  short 429. Tests exercise the actual client retry and guild/global comparison;
  the full MySQL-enabled backend suite passes. Loaded
  `/tmp/quack-v5-registration-fix` in tmux pane `%1`. Live `/status` reports Beta
  Bot, MySQL and Redis connected; all seven commands matched remotely with no
  rewrites. The local database confirms one settings row and one starter template
  for Quack's pond. The beta bot is running. Remaining live findings: 21 obsolete
  guild commands (pruning is still disabled) and a general-logging startup error.
  Full feature journeys, schema-code simplification and broader acceptance remain.
