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

- General logging now treats disabled logging and events without a destination as
  normal queue outcomes, keeping them out of developer error logs. Actual delivery
  failures still report an error and increment failure status. Regression coverage
  verifies both quiet outcomes and real failures without adding audit entries;
  the focused and full MySQL-enabled backend suites pass. Loaded
  `/tmp/quack-v5-logging-fix` in the approved beta pane; the false startup error no
  longer recurs. Live Helium rehearsal as dickey successfully created `Rehearsal
  rule` through the native Discord modal and displayed its private confirmation
  (warning, DM enabled, appeals enabled). Discord's native app remains the monkey
  tester account. Command pruning still awaits separate explicit approval.

- Continued live rehearsal through both real accounts: dickey used the current
  `/case add` entry in Helium to create warning case #1 for monkey. Monkey received
  the rule/reason DM with an Appeal decision button, submitted the one-answer modal,
  and received a submission receipt. Dickey found that appeal with `/appeals` and
  accepted it. The review message updated to accepted with a next-pending control;
  monkey received the acceptance DM. A direct read of the local database confirms
  case #1 is `voided` and its appeal is `accepted`. This verifies the warning path;
  punishment reversal and the dedicated review-channel delivery remain unverified
  live. Duplicate legacy slash entries still require care during command selection.
- The rehearsal exposed technical warning copy. Public warning receipts now use
  the warning icon and say `Warning recorded.`; staff detail uses the same wording.
  Voided case details no longer claim that the member can appeal the case. A
  regression checks valid versus voided appeal guidance and disabled void controls.
  Focused tests and the full MySQL-enabled backend suite pass. These view changes
  are committed source changes; the running beta still uses the earlier logging
  fix binary until the next deliberate reload. Remaining bot acceptance work is
  still open.

- Replaced the evidence button's unbounded response with native text pages, bounded
  after resolving application-specific icons. Each page retains upload instructions
  and Previous/Next controls; navigation reloads the case through live staff
  authorization rather than trusting the original interaction's permission bits.
  Text splitting preserves every character, handles Discord's UTF-16 budget, and
  keeps ordinary Markdown evidence links together. Tests cover long Unicode
  captures without message.txt fallback, terminal navigation controls, lossless
  splitting, clickable links at page boundaries, and revoked moderator access.
  Remaining long-record work includes case detail and appeal statement pagination,
  configured web-equivalent buttons, and a presentation for individual URLs longer
  than a whole page (those currently retain their text but may span pages). The
  updated evidence UI still needs a live rehearsal after a deliberate beta reload.
  Focused tests and the final full MySQL-enabled backend suite pass.

- Native case detail now paginates the rendered record for `/case view` and the
  post-upload receipt. Every page retains context/evidence/member navigation and
  the applicable void/retry controls. Evidence and detail share a single live-
  authorized page handler, and page responses are explicitly private. The long
  Unicode context regression verifies the final text is reachable, no message.txt
  is generated, every message fits Discord's limit, and retry stays available.
  Focused tests and the full MySQL-enabled backend suite pass. Live verification,
  appeal statement pagination, configured web buttons, and the broader outstanding
  acceptance items remain pending; the running beta has not been reloaded yet.

- Long appeal statements now render as native pages in `/appeals`, dedicated staff
  queue sends/refreshes, and decision receipts. Paging a shared channel message
  opens a private reading copy; subsequent clicks update that private copy, so
  moderators do not overwrite each other's reading position. Every read resolves
  current guild authority and calls the authorized appeal service. Decision buttons
  remain attached to the same appeal on each page, with a return to pending appeals.
  Tests verify a complete 3,000-duck Unicode statement without a file fallback,
  retained decision identity, shared/private acknowledgement behavior, and revoked
  permission rejection. Discord-focused and full MySQL-enabled suites pass. These
  pagination changes still await live rehearsal after the next beta reload; web
  buttons, schema simplification, broader module rehearsal and other tracked
  acceptance work remain open.

- Began schema consolidation by removing the obsolete placeholder AppealRecord
  and AppealEventRecord definitions. The service, reversals, and schema inventory
  now share the actual current records, including statement snapshots, version,
  actor classification, and the unique case reference. Two aliases isolate names
  still referenced by the frozen migration source without changing the checksums
  applied to the running rehearsal database. The historical placeholder fixture
  explicitly omits its not-yet-existing actor_type column. Focused store/service
  tests and the corrected full MySQL-enabled suite pass. This removes competing
  live schema definitions; it does not complete replacement of the eleven-step
  pre-release migration runner, which remains outstanding.

- Added `quack-migrate init` to create a fresh database directly from the current
  core records and module schemas. Directly initialized databases have a small
  marker instead of the historical checksum ledger; startup recognizes that marker
  and reconciles the current definitions. Initialization refuses an unmarked
  nonempty database, supports retry after partial MySQL DDL, and disallows the old
  rollback operation. SQLite/MySQL tests cover creation, repeated startup, a
  missing-table retry, default-level uniqueness, and preservation on refusal. The
  complete MySQL-enabled backend suite passes. No live database was reset or
  switched. The old runner remains for existing databases, and final constraint
  application is still shared with its last migration; replacing that runner,
  making direct initialization the default, and deleting frozen compatibility
  code remain part of the outstanding schema cleanup.

- Empty databases now use direct current-schema initialization by default, so the
  ordinary feature test harnesses exercise it rather than replaying the eleven-step
  chain. This exposed and fixed missing audit-mirror receipt initialization, an
  unconditional legacy template-quarantine lookup, recovery manifests assuming the
  old ledger, and readiness rejecting current-schema databases. Historical fixture
  tests now explicitly create their retired quarantine table or invoke the legacy
  runner; clean composition instead verifies current-schema readiness. Focused
  storage/service and route/readiness tests pass, followed by the full MySQL-enabled
  backend suite. Existing nonempty databases retain the old compatibility runner;
  deleting it and switching the live rehearsal database remain outstanding.

- Added `quack-migrate adopt` for the fully migrated pre-release database. It
  validates the complete unchanged ledger and required preserved tables before
  adding the current-schema marker; it does not delete the ledger or rewrite
  application rows. SQLite/MySQL regressions compare recovery manifests and case
  numbering before adoption and after startup, and reject partial histories.
  Focused adoption checks and the full MySQL-enabled backend suite pass.
- Backed up the local rehearsal database to mode-0600
  `/tmp/quack-before-schema-adoption.sql`, stopped the approved beta pane `%1`,
  adopted its schema without resetting data, and started
  `/tmp/quack-v5-current-schema` there with command pruning still disabled. Live
  `/readyz` is green (current schema version 1), and `/status` reports Beta Bot,
  MySQL and Redis connected. A database read confirms tester case #1 remains voided
  and its appeal accepted. This binary includes the accumulated warning and native
  pagination changes. Old runner deletion and remaining feature rehearsals are
  still pending; the dashboard pane was not changed.

- Setup policy correction from the live ticket rehearsal: all setup channel
  options are now optional. First setup creates suitable channels; later runs
  reuse configured destinations, replacing only confirmed deleted channels.
  Explicit destinations retain their existing overwrites and viewer roles.
  Appeals, audit, tickets, logging and honeypot share this selection behavior.
  Created staff channels allow moderator/manager roles and Quack; ticket entry
  supports private threads and honeypot permits member messages with bot cleanup.
  Shared destination validation now checks guild ownership and bot delivery
  permissions, including during background delivery, without policing viewers.
  This supersedes earlier requirements for rejecting public/non-staff viewers on
  administrator-selected destinations. Managed evidence source/storage protection
  remains separate. Selection/failure/default permission tests, updated setup and
  audit delivery regressions, and the full MySQL-enabled backend suite pass.
- Live verification: reloaded the approved beta pane with
  `/tmp/quack-v5-setup-defaults`; `/readyz` remained fully ready. From dickey in
  Helium, `/setup tickets` with no options created `support` and `ticket-log` and
  published the Open ticket panel. Subsequent explicit-channel UI testing paused
  when CUA detected the user interacting with Helium; explicit selection and
  non-staff/public-viewer acceptance are covered by automated regressions.
- Ticket setup now serializes each guild's channel/panel configuration sequence.
  Moving the entry retires the old panel's controls and points to the new channel
  before posting a replacement. A forbidden/unavailable old panel blocks new
  publication and retains its saved reference for a retry; confirmed deleted
  messages/channels need no cleanup. Shared cancellable guild locking now serves
  honeypot warning updates and ticket setup with independent lock maps. Focused
  panel relocation/lock checks and the full MySQL-enabled backend suite passed.
- Normal startup now reconciles current schema only. Historical unmarked databases
  require explicit `quack-migrate legacy-up` (when incomplete), then `adopt`; startup
  leaves them unchanged. Current schema constraints are independent DDL and no
  longer invoke historical row conversion or action-recovery inserts. Frozen
  historical definitions remain available only for explicit recovery/adoption.
  SQLite/MySQL regression tests preserve existing history, verify the explicit
  transition, and prove repeat current startup does not rewrite historical rows.
  Root independently reran the schema/migration tests with MySQL successfully.
  The current schema also includes the public-case receipt table prerequisite for
  the durable Discord publication worker being implemented separately.
- Closed ticket detail now removes dead thread navigation and impossible controls.
  Authorized owners can retrieve retained transcript files; staff can also follow
  the queue receipt. Finish closing is shown only while that ticket still holds
  its owner's reservation. Long histories use native previous/next pages that
  update the same private message and refresh live authorization. Root reviewed
  the lifecycle/privacy boundaries and independently passed focused ticket and
  closure tests. The service still loads the full timeline before presentation.
- General logging bulk deletions now retain available cached attachment/embed
  details, and queued edits defensively copy their prior attachment snapshots.
  Native one-channel setup already enabled content and metadata; added a service
  regression following saved setup through delivery, including destination and
  absence of transport audit noise. Root independently passed general-logging
  and moduleintegration package tests after review.
- Public case receipts now persist message/channel coordinates and an allowlisted
  presentation snapshot. A bot-credential worker resumes after restart, refreshes
  enforcement/void status and evidence health, retries transport/storage failures,
  and retires definitively deleted messages. Pending receipts reconcile every two
  seconds; stable receipts every five minutes, subject to backlog/outages. Initial
  response failures retain a committed-case explanation instead of generic failure
  or another moderation operation. Root reviewed privacy/lifecycle boundaries and
  the consolidated MySQL-enabled backend suite passed. Initial Discord send and
  local receipt storage remain non-atomic: a crash between them can leave an
  untracked public message; failed registration retains private feedback and a
  bounded interaction-token refresh fallback. Recovery manifests now preserve
  current-schema publication receipts without requiring them before legacy adoption.
- New/recreated managed evidence channels now grant current moderator and guild
  manager roles View Channel and Read Message History, so preserved Discord file
  links are usable without Administrator. Default nonstaff visibility stays denied;
  existing administrator channel permissions/name/category remain untouched per
  interview Q34. Root independently passed evidence adapter tests after reviewing
  creation/recreation, effective role permissions and existing-channel preservation.
  Previously created bot-only channels retain their configured permissions.
- Edit context now captures valid pasted Discord message links after saving the
  freeform text. The shared evidence boundary checks source visibility, guild and
  target; optional failures keep context and show the exact `/case evidence` retry
  command. URL aliases and previously recorded sources are deduplicated on later
  edits without changing enforcement or deleting historical evidence. Root reviewed
  this flow and independently passed context/evidence tests in services and commands.
  Concurrent submissions and upload-before-storage crashes still lack a durable
  capture reservation; this is not a claim of exactly-once external uploads.
- Case-created audit mirrors now include selected level and selected outcome from
  the immutable case snapshot, including timeout duration. Historical records with
  no selected level do not invent one. These fields describe the saved decision;
  separate enforcement-result events still report actual success/failure, and
  transport events remain excluded. Root reviewed snapshot provenance and passed
  focused service/view audit tests.
- Honeypot cleanup now persists one receipt per qualifying message linked to its
  debounced incident. Bursts share one case, while each message is deleted only
  after the incident has saved case/evidence. Leased cleanup retries independently
  of moderation, resumes after restart, and treats missing messages/channels as
  complete. Exempt messages are never scheduled. Cleanup cancellation interrupts
  blocked deletion during shutdown without preventing accepted case work draining.
  Recovery manifests include pending/completed cleanup state. SQLite/MySQL schema
  checks, 30 repeated cleanup tests, full honeypot race tests, and the consolidated
  MySQL-enabled backend suite pass. Primary incidents interrupted before being
  marked created still require recovery; their messages deliberately remain rather
  than deleting evidence or blindly repeating moderation.
- Integrated parallel work was built as `/tmp/quack-v5-parallel-review` and loaded
  into the approved beta tmux pane `%1` with the existing adopted database and
  command pruning disabled. Live `/readyz` reports all checks ready, including
  Discord, MySQL, Redis, queue, action capabilities and current schema version 1.
  Dashboard pane `%3` was untouched. Feature-level Discord rehearsals remain
  incomplete; readiness is startup evidence, not proof of every user journey.

### September 8 parallel integration and live ticket acceptance

- Separate reviewed commits: `4827cae` explicit application URLs; `b8be3c8` native
  case history totals/import labels with UTF-16 budgeting; `bad4bb3` interrupted
  honeypot incident recovery; `02d6616` canonical module enablement with live checks.
  Focused packages and the consolidated MySQL-enabled backend suite pass.
  These newer changes have not yet been loaded into the running beta process.
- With the previously integrated beta, the tester opened a private ticket, received
  the same ticket on duplicate open, and exchanged messages with the admin who
  joined from the staff queue. The tester closed it inside the thread. The queue
  retained the transcript, both participant messages were present in persisted
  transcript content, and the thread was removed. The tester could retrieve the
  closed transcript through View without access to the staff queue.
- The tester then opened a new ticket and the admin closed it from the queue.
  Discord displayed a closure receipt and transcript attachment. Database checks
  confirmed both tickets resolved by the respective actors and the member slot
  was empty. No live member content is included in this evidence record.
- Remaining ticket gaps: old ephemeral receipts retain stale deleted-thread
  mentions until refreshed, and closure inside a deleted thread provides little
  visible feedback. Legacy retention of text deleted before closure is being
  restored; this rehearsal does not establish that behavior.

### Core composition, logging parity, and historical import

- `f1e1eb7` moves appeal notification and audit mirror worker ownership into
  process composition, starts them after Discord connects, and stops them before
  Discord/storage teardown. Core case/appeal controls and authenticated appeal
  API routes register independently of optional modules. Focused startup/cancel
  and route tests and the MySQL-enabled backend suite passed; independent review
  found no remaining regression in the separation.
- `d90a771` restores cached bot-message context and author identity. Bulk deletion
  output keeps each author's text/files together under existing privacy flags.
  Gateway, module and view tests pass. The semantic audit allowlist was reviewed
  and required no additional change.
- `8e3a64b` adds explicit read-only legacy SQL extraction and preserves all six
  legacy types as non-executable historical cases. Imported views identify the
  actual historical action, moderator and context. Duplicate source IDs fail
  preview. Root independently passed parser/CLI tests and the disposable MySQL
  extraction/import rehearsal, including idempotence and no enforcement work.
  Real-backup rehearsal remains open; exports remain bounded to 64 MiB per file
  without automatic chunking, and module setup is explicitly manual for cutover.
- Ticket original-text retention remains uncommitted while thread-level
  concurrency and final-capture boundaries are reviewed. No newer binary has
  been loaded during this integration pass.

### Ticket journal integration and direct evidence upload

- `11ad055` adds bounded legacy export pages with deterministic ordering and
  continuation offsets. Focused parser/export CLI tests pass. Paging assumes an
  unchanged restored source snapshot; it removes the earlier manual splitting gap.
- `c6e21ed` retains original received text for positively identified ticket
  threads independently of logging. Per-thread gates flush admitted writes before
  merging final history from the locked/archived thread. Failed persistence or
  buffer overflow blocks deletion. Retention expires with the transcript, and
  current-schema startup/recovery includes the journal. Focused tests, repeated
  race tests and the root MySQL-enabled backend suite pass. Delayed deleted events
  first delivered after the final snapshot and buffered writes lost during a
  database outage plus hard crash remain outside this guarantee.
- Real Discord direct-upload rehearsal succeeded using a synthetic plain-text
  file and the warning-only Rehearsal rule. Case #2 showed a private preserved-file
  link; opening it reached a Beta Bot attachment with the expected text. This
  verifies administrator upload/copy/view, not restricted-role access or source
  deletion. The case notification was recorded sent.
- Loaded `/tmp/quack-v5-retention-review` into authorized tmux pane `%1` after
  clean shutdown. Existing database retained; no reset or command pruning. Live
  readiness passed database, Redis, Discord, queue, action capabilities and schema.
  New journal deletion/restart acceptance remains to be exercised in Discord.

### Live deleted-message retention and interaction refinements

- The tester opened a private ticket, sent synthetic original text, then deleted
  that message in Discord. SQL showed its journal row before a clean beta restart.
  After restart/readiness, the tester closed the ticket. The staff queue's attached
  transcript visibly contained the deleted original text; SQL also confirmed the
  resolved state and retained content. This is live deletion/restart evidence,
  distinct from only testing surviving close-time history.
- `8e2ee63` registers the durable ticket before inviting the member, closing an
  opening window where immediate replies could be missed. Failed permission sync
  preserves the ticket and exposes repair rather than deleting possible evidence.
  Focused/race tests and a MySQL-enabled full suite passed.
- `1c3fa74` adds optional configured web buttons to private case/history/evidence
  views and pages, using existing route shapes and retaining native controls.
  Root command tests pass. `5848e7c` acknowledges saved closure progress before
  source-thread deletion and skips impossible post-deletion edits; focused and
  full-suite tests pass. Neither change edits dashboard source.
- Running beta is still `/tmp/quack-v5-retention-review`; these latest interaction
  refinements require the next build/reload before live acceptance.

### Live logging and honeypot rehearsal

- Default `/setup logging` created a log channel. The tester sent, edited and
  deleted synthetic text in the testing channel. The administrator visibly
  verified author, channel, message ID, exact before/after edit versions and the
  final deleted text in the log. Attachment, bulk-delete and bot-message paths
  remain test-backed rather than live-verified.
- Default `/setup honeypot` created its channel and warning. The administrator
  changed its editable template to a one-minute timeout. An administrator message
  remained untouched with no counter increment. A tester message then disappeared,
  Discord displayed the one-minute timeout and the warning counter advanced to one.
- Read-only database checks confirmed case #3 (`01M20JTVTPNGKV9NMNTCK5VFE3`),
  source `honeypot`, valid state, one successful timeout execution, captured
  triggering text and a sent notification. This establishes the ordinary incident
  flow, not burst suppression or interrupted-incident recovery in live Discord.
- `3dcf02b` removes direct command access to case publication storage through
  narrow receipt-registration and action-status use cases. Root focused command
  and case publication tests pass; durable worker ownership remains in composition.
- Subsequent administrator inspection also verified bot-message logging: the
  honeypot counter update included Beta Bot attribution and both warning versions.
  The tester visibly received case #3's timeout DM and opened its appeal form.
  Submission appeared in `/appeals`; administrator rejection updated the private
  receipt and delivered a decline DM. Clicking the original appeal button again
  returned the existing-appeal explanation instead of another form.

### Parallel scale review and bounded improvements

- GPT-6 medium agents reviewed scale paths and implemented independent fixes;
  root reviewed their changes and performed live acceptance in the meantime.
- `e907627` fixes singular incident/minute copy. `704a0e9` resolves an incoming
  message's guild once, captures context before honeypot REST lookups and bounds
  those database/Discord requests. Configuration remains current on every message;
  this removes one repeated lookup, not all per-message database work.
- `fb7c236` derives staff statistics through four grouped SQL count projections,
  preserving source filters, exact labels and UTC buckets. Independent SQLite and
  disposable MySQL checks include a non-UTC DSN across DST. Memory now follows
  aggregate groups; the database still scans matching source history.
- `939986c` imposes an estimated 64 MiB aggregate logging-cache budget alongside
  per-guild FIFO limits. Global oldest-first eviction and bounded idle metadata
  prevent guild count from multiplying retained context without a shared limit.
  The estimate is not a precise heap ceiling; there is no time expiry or reserved
  minimum per guild. Focused tests and ten race runs pass.
- The MySQL-enabled full backend suite passed at
  `/private/tmp/logging-cache-budget-full-test.log`; root separately reran the
  final statistics tests and gateway integration package. These checks do not
  establish measured capacity at 850 guilds or 200,000 members.
- Scale review still identifies indefinite stable-receipt reconciliation and
  complete detail/timeline reads behind some native pages. These remain follow-up
  work. The live beta has not yet been reloaded with this batch.

### Updated beta and ticket closure verification

- Built `/tmp/quack-v5-scale-review` from `012fe9e` and cleanly reloaded the
  authorized `%1` beta pane. The existing database and dashboard process were
  preserved. Readiness passed Discord, database, Redis, queue and action checks.
- The tester opened ticket `01M20KM6Y87T10EPCPMNFPQCJ5`, sent synthetic text and
  closed it inside its thread. The thread disappeared, SQL confirmed resolution
  by the tester and a published transcript, and runtime logs recorded success
  without the old post-deletion interaction error. The brief progress message
  was not captured visually, so this is not proof of its on-screen duration.
- The original private entry receipt still showed a stale deleted-thread mention.
  Its View button successfully returned closed state and an attached transcript
  containing the synthetic text. Refreshing that private receipt in place is the
  next small interaction fix; public controls must retain private replies.

### Narrow reads and clearer private controls

- `a5378c1` removes discarded ticket timeline reads from permission/lifecycle
  operations while preserving current owner/staff checks and full detail history.
  `4e7f72c` gives native evidence pages a case-and-evidence-only authorized read.
  Focused query guards, rendering equivalence, revoked authority and lifecycle
  tests pass. Full API case details remain unchanged.
- `40533c3` refreshes private ticket entry receipts in place when View is clicked;
  public controls still receive private responses. Boundary tests pass. This is
  user-triggered refresh, not background editing of all old ephemeral receipts.
- Live tester access to case #2's View evidence control exposed no staff data,
  but produced an unhelpful generic error. `77f46b4` explains recognized permission
  denials privately. Dispatcher tests verify both private original replies and
  private followups without overwriting a shared message.
- `9933511` makes receipt refresh transactional with relevant case/action/evidence
  mutations. Revisions fence stale completions, including late edits after newer
  workers; stale repair requests advance revision too. Terminal receipts become
  idle. SQLite/MySQL mutation, rollback, interleaving, upgrade and recovery tests
  pass. Two historical appeal fixtures needed the current receipt table when
  invoking current acceptance code; historical migrations were left unchanged.
- Full MySQL-enabled backend suite passed at
  `/tmp/receipt-read-integration-tests-final.log`. Built and loaded
  `/tmp/quack-v5-receipt-review` at `9933511` with the existing database. Readiness
  passed; the member's denied evidence click now visibly explains permission.
- Administrator voided synthetic warning #2 with a correction reason. The
  original public receipt, created hours earlier before multiple beta restarts,
  visibly added the voided state and disabled Void. SQL confirmed revision 1,
  completed digest and `refresh_requested=false`. No punishment was attached to
  this warning, so this does not establish actual ban/timeout reversal.
- The tester clicked View on the stale 7:34 private ticket entry receipt using
  the new beta. Discord visibly edited that same receipt into closed state with
  the retained transcript, removing the unknown-thread mention and obsolete
  controls. The separate older detail response also reflected its updated reply.
- Next architecture boundary identified by read-only review: case notification
  formatting still belongs to the core action service. Move member-safe intent
  into the Discord transport and return rendered delivery receipts, preserving
  claim/prepared-channel/retry behavior. Appeal decision bodies are different:
  they freeze decision reason and rejoin URL transactionally, so their eventual
  intent migration needs versioned persisted payloads and legacy-body fallback.
  Do not rebuild historical appeal messages from current settings.

### Live timeout reversal and audit verification

- Tester triggered synthetic honeypot case #4. Discord visibly showed the active
  one-minute timeout. Administrator opened the new case and voided it with a
  correction reason; the tester's input returned. SQL confirmed original timeout
  success at 07:50:54.609 local, recorded expiry 13:51:54 UTC, and successful
  removal at 07:51:49.950 local—about four seconds before expiry. This is an
  actual early removal, not merely observing an expired timeout.
- Grouped live audit rows contained case creation/voids, successful actions,
  appeal submission/decisions, ticket open/resolve and module settings changes.
  No read events, skipped mirrors or service-firing bookkeeping appeared in that
  rehearsal database. Discord mirror delivery remains a separate check.
- Reversal review found a distinct ownership gap: an old case could remove a
  newer punishment. A bounded guard is being implemented to compare recorded
  timeout expiry or original ban provenance with current Discord state, and
  detect newer Quack enforcement. A mismatch must require review, not removal.

### Notification adapter and guarded reversals

- `5d93ca5` moves case notification rendering into the Discord adapter. Core
  passes member-safe rule/outcome facts and retains the returned rendered receipt
  on success or failure. Preparation and delivery fencing are unchanged. Lost
  sends now have consistent uncertain-outcome classification; current failed
  notification state remains terminal and is not automatically resent.
- `ac4b5f7` verifies original enforcement, competing same-kind Quack work and live
  timeout expiry or original ban reason before reversal. Missing linkage or
  provenance fails closed. New timeout receipts preserve fixed milliseconds;
  legacy second-only receipts remain supported. Already absent punishments have
  explicit no-op attempt/event/audit outcomes. Native summaries describe the
  resulting state. Visible audit footers omit database resource names and IDs.
- The guard is an observation, not an atomic Discord compare-and-remove: manual
  or newly concurrent enforcement can race between inspection and removal. An
  exactly copied ban reason is weaker than immutable ownership. Newer failed work
  is conservatively treated as potentially affecting punishment and needs review.
- SQLite/MySQL provenance and focused ownership, absence, precision, missing-link,
  privacy and notification tests pass. Root full MySQL-enabled backend suite
  passed at `/tmp/notification-reversal-integration.log`. Loaded
  `/tmp/quack-v5-ownership-review` at `ac4b5f7`; readiness passed and the database
  and dashboard process were preserved.
- Live `/setup audit` created channel `1546881393651486720` (`moderation-log`).
  New synthetic case #5 (`01M20NC3983MTZP59HGBMJ7441`) delivered a member timeout DM
  with exact expiry and native appeal button. The mirror showed separate case
  creation and timeout success with member, rule, selected level and outcome,
  without raw database IDs. After expiry, administrator voided the case. SQL
  recorded `timeout_already_absent` and `reversal_noop=true`; the mirror visibly
  confirmed absence and that no reversal request was sent.
- Live changed-punishment/ban ownership and action failure-to-retry acceptance
  remain open. Case notification formatting is no longer core-owned; persisted
  appeal decision formatting and broad repository exposure remain follow-up
  architecture work, not completed by relocating case notification rendering.

### Native history totals verification

- Administrator used View user from case #5 in Helium. The private native history
  listed cases #5 through #1, marked the four voided cases and displayed
  `5 total · 1 valid · 4 voided` with disabled pagination on page 1/1.
- A read-only query of the local tester's cases confirmed one valid and four
  voided rows in the rehearsal guild. This verifies current native totals and
  visibility; no imported cases were added to the live database, so imported
  labels and multipage navigation remain separate acceptance checks.

### Parallel appeal and native detail integration

- Two GPT-6 medium agents completed independent slices; root reviewed the
  boundaries and integrated them in separate conventional commits.
- `a588649` persists version-one appeal decision facts and renders member copy
  in the Discord adapter. The decision reason and rejoin URL remain snapshots
  through settings changes, reconstructed workers and safe delivery retries.
  Legacy rows without intent retain exact saved bodies. Invalid or unsupported
  nonempty payloads fail without sending or falling back. Nullable additive TEXT
  supports MySQL upgrades; legacy migration sources are unchanged. SQLite/MySQL
  upgrade and recovery-manifest mutation checks passed.
- `7c9f95f` gives native case details the latest six events and omits unused
  action-attempt reads. Event filtering happens before the SQL limit; IDs break
  timestamp ties. Authorization, recovery controls and native output remain
  covered by parity tests. Full HTTP detail and mutation responses stay complete.
- The combined MySQL-enabled `go test ./...` passed; output is in
  `/tmp/native-detail-appeal-intent-full.log`. Build
  `/tmp/quack-v5-appeal-detail-review` also passed. The existing beta remained
  healthy during this work; this batch has not yet been loaded for live acceptance.
- The architecture review recommends considering bounded evidence reads next,
  rather than wrapping every remaining infrastructure repository access. Evidence
  collections remain unbounded and need deterministic pagination; no associated
  live correctness failure or authorization bypass was established by that review.

### Live replacement-timeout ownership check

- Loaded `/tmp/quack-v5-appeal-detail-review` from `7c9f95f` in the authorized
  beta pane, preserving the rehearsal database and dashboard process. Readiness
  passed all dependencies after the additive appeal intent schema update.
- Tester triggered synthetic honeypot case #6 (`01M20PFS06PHQV876QGE74V1YT`).
  After its one-minute timeout expired, administrator applied a separate
  five-minute timeout through Discord's native moderation panel, then voided
  the original case. Native case detail rendered correctly on the new projection.
- SQL confirmed the case void and a failed reversal with
  `reversal_ownership_conflict`. The audit mirror explained that the current
  timeout differed and nothing was removed; the tester still visibly had a
  timeout. Administrator then manually removed this synthetic replacement as
  cleanup. Retry from the mirror remains the next live step after its source
  message preservation fix is loaded.

### Recovery controls and ticket publication integration

- `ca1f969` preserves public audit sources when Retry/Dismiss is clicked and
  refreshes existing ephemeral queues in place. Void/reversal modal failures stay
  private; committed results remain visible despite later publication failures.
- `bdd5170` lets Repair ticket restore a definitely missing initial staff queue
  post. A durable admission marker prevents repeated initial sends after network
  uncertainty or a lost receipt; only explicit nondelivery releases it. Such
  uncertain deliveries still require administrator inspection, with no new manual
  receipt reconciliation workflow. Transcript messages retain View ticket, allowing
  staff to reach Finish closing after cleanup fails.
- Full MySQL-enabled backend tests and build passed. Output is in
  `/tmp/recovery-controls-ticket-full.log`; focused ticket/moduleintegration race
  tests also passed three repetitions. Loaded `/tmp/quack-v5-recovery-review`
  from `bdd5170` in the authorized beta pane; readiness passed, database preserved.
- Administrator clicked Retry on case #6's original failure audit message after
  manually cleaning up the replacement timeout. The original message remained
  intact. A separate private response reported the queued retry and empty active
  failure queue; separate semantic audit messages recorded retry and confirmed
  absence without removal. SQL verified the same execution
  `01M20PNP7BY7KTBSF3AEZCYFPS` succeeded with two retained attempts: ownership
  conflict followed by `timeout_already_absent`/`reversal_noop=true`.
- Remaining source-confirmed workflow gaps from this audit: honeypot warning and
  counter refresh failures lack independent eventual repair; the initial compact
  case receipt omits parts of Q21's moderator feedback. Its member/staff audience
  split needs care because Q20 allows command use anywhere and public command
  results already exist. These are separate from unverified live ban/evidence,
  ticket repair/deletion-failure, import and scale acceptance.

### Live versioned appeal delivery

- Created synthetic warning case #7 (`01M20Q7AXWJ0ZH6WK57GY7JWTR`) and used its
  native DM appeal button. The first form-opening response missed Discord's
  deadline: runtime logged HTTP 404/code 10062 with elapsed 3157 ms. A second
  click opened the form and the tester submitted one statement. The successful
  retry does not establish the cause of the first timeout.
- Administrator accepted appeal `01M20QANHBFYRGH1CBGCPBCPPD` from the private
  native queue. The queue showed acceptance, the case became voided, and the
  tester received the acceptance DM. SQL confirmed `sent` with empty legacy body
  and version-one intent containing accepted status and the saved decision reason.
- `adebc00` adds preparation and response timing only to existing interaction
  failure logs, so a future timeout can distinguish work before acknowledgement
  from Discord response latency. Focused interaction tests passed; no routine
  service events were added to the user audit log.

### Moderator receipt and warning recovery acceptance

- `8c78334` gives all three case entry paths private moderator receipts with
  selected level, safe action errors, DM status, appeal eligibility and evidence
  warnings. Private refresh follows both enforcement and DM completion, backs off
  after 30 seconds and stops at terminal state or 14 minutes. It retains no
  interaction token in storage; View case rechecks current authority afterward.
  The private/public split was the announced default while the visibility
  clarification remained unanswered. Public notices contain the member-facing
  rule reason, case number and outcome, without level or private diagnostics.
- `8325427` persists bounded, coalesced honeypot warning refreshes independently
  of enforcement and seeds configured guilds through a one-time paged startup
  reconciliation. Successful incident completion and its refresh request are
  atomic. Replacements retain a send fence after uncertain delivery instead of
  creating duplicates. Full MySQL-enabled tests passed in
  `/tmp/receipt-honeypot-full.log`; focused race/adoption/rollback checks passed.
- Loaded `/tmp/quack-v5-receipt-warning-review`; readiness and startup warning
  reconciliation passed. New warning case #8 showed private DM-sent and appeal
  feedback plus a short public notice. In shared honeypot channel, case #9's
  public notice exposed no private fields to the tester, but Discord displayed
  a broken reply preview pointing at the hidden acknowledgement.
- `94cfda2` corrects that observed rough edge: public case/recovery notices use
  standalone bot-authenticated channel sends; fallback edits use the same channel
  coordinates. Private fallback remains ephemeral, mentions are suppressed, and
  uncertain POSTs are not retried. The full backend/MySQL suite and build passed
  (`/tmp/standalone-receipts-full.log`). Loaded `/tmp/quack-v5-standalone-review`,
  readiness passed, then voided synthetic case #9. The tester saw the new public
  void result without a broken reply preview. Old messages retain old references.
- For the warning repair rehearsal, a temporary SDK helper verified the beta's
  identity, guild, author, exact configured message and four-incident warning
  before deleting the beta's own message `1546871485371514971`. The worker recreated
  it as `1546897037067554830`; the tester saw the same warning/count. SQL confirmed
  the saved replacement receipt, idle refresh and unchanged four honeypot cases.
  This tests ordinary deletion repair; live transient permission failures and
  ambiguous replacement delivery remain outside this rehearsal.

### Evidence storage, attachment links, and appeal rejoin

- `b940eda` repairs evidence storage on attachment capture with a bounded attempt.
  Channel receipt updates compare the expected old ID and preserve concurrent
  administrator settings and channel choices; capture uses the winning ID.
  Existing channel names and permissions remain untouched. Failed repair leaves
  explicit metadata-only evidence warnings, and later uploads retry. A channel
  created by a losing concurrent repair is left for inspection rather than deleted.
- `8885dd0` adds an optional Rejoin server link button to accepted appeal DMs,
  using the validated URL saved in the decision intent. Legacy stored bodies keep
  their original behavior; delivery does not reconstruct links from current settings.
- `1fb6fdb` retains attachment links in single-message and bulk deletion logs and
  edit context. Signed URL rotation alone refreshes the cache without producing a
  false edit event. These links are not archived copies and may expire; large log
  bodies retain the `message.txt` attachment fallback.
- The full MySQL-enabled backend suite passed in
  `/tmp/evidence-links-rejoin-final.log`. Loaded
  `/tmp/quack-v5-evidence-links-review` in the authorized `quack` beta pane `%1`;
  `/readyz` checks passed. Live `/setup appeals rejoin:https://discord.gg/BC8EDwJTS`
  created the appeals channel by default. The live ban/rejoin rehearsal remains
  pending; this records setup and readiness, not successful ban reversal or rejoin.
- The temporary `Ban evidence rehearsal` rule form is prepared in the admin's
  Helium session. Automatic approval review rejected submission because it changes
  live moderation configuration without sufficiently specific authorization.
  No rule was created and no tester ban was performed; explicit permission is
  needed before continuing this live rehearsal.

### Synthetic load measurements

- `0025298` adds reproducible cache benchmarks and an opt-in MySQL audit-history
  assessment. On this Apple M3 Pro with Go 1.25.4, one-second cache samples across
  850 guild identities used 256-byte text and two attachments with 200-byte URLs.
  At GOMAXPROCS=1, replacement measured 672 ns/op, global eviction 935 ns/op,
  and mixed replacement/read 713 ns/op (748 ns/op with eight workers). The
  eviction workload retained 40,169 messages and 67,107,485 accounted bytes,
  below the 64 MiB budget. Accounting excludes transient allocations and Go
  allocator overhead. Raw results: `/tmp/logging-cache-bench.txt`.
- Cache timings exclude gateway SQL/configuration reads, JSON decoding, queueing
  and Discord delivery. They do not prove capacity for 850 production guilds.
  Run `go test ./internal/modules/generallogging -run '^$' -bench
  BenchmarkCache850Guilds -benchmem -benchtime=1s -cpu=1,8` from `apps/backend`.
- Against an isolated local MySQL database containing 85,000 delivered audit
  events across 850 guild identities, 20 idle polls measured median 342 ms,
  p95 522 ms and maximum 679 ms. The current anti-join scans retained history;
  this identifies an implementation cost to remove, not a successful scale gate.
  Raw results: `/tmp/quack-audit-load.log`. Run `TestAuditMirrorHistoricalLoad`
  with `QUACK_LOAD_TESTS=1` and `QUACK_TEST_MYSQL_DSN` to repeat the assessment;
  the helper creates and drops its own database and does not touch beta data.
- `e658418` bounds native evidence content reads to one snapshot and its
  attachments. Previous/Next traverse long-text subpages and evidence items in
  stable oldest-first order; every navigation rechecks current staff authority.
  SQLite/MySQL tests cover query bounds, attachment scope, old component payloads,
  empty evidence and cross-item navigation. The full backend/MySQL run passed in
  `/tmp/evidence-pages-full.log`; this is not yet live UI acceptance. SQL
  count/offset work remains, and the full HTTP evidence contract is unchanged.
- `25fd8bc` replaces repeated audit anti-joins over retained history with
  indexed unfinished/due delivery rows. Source decisions, semantic audit events
  and initial receipts commit together. Startup backfills missing receipts once,
  preserving finished receipts and future retry deadlines. A ready schema with a
  missing delivery ledger fails closed. Tests cover both SQLite and MySQL, older
  marker upgrades, rollback, adoption preservation and bounded orphan retirement.
  The unchanged 85,000-event assessment measured median 3.04 ms, p95 4.70 ms and
  maximum 4.99 ms (`/tmp/audit-mirror-load-after.log`), versus 342/522/679 ms
  before. This is an isolated local idle-poll comparison, not full bot capacity.
  Send-before-receipt crash ambiguity remains; the queue does not claim atomic
  delivery across Discord and SQL.
- After updating two minimal older test fixtures to include the required delivery
  table, focused SQLite/MySQL reversal/publication checks and the full backend
  suite passed (`/tmp/audit-evidence-integrated-final.log`). Loaded
  `/tmp/quack-v5-audit-queue-review` in beta pane `%1`; all readiness checks passed.
  Before and after the live schema upgrade, SQL showed exactly 50 audit events
  and 50 finished delivery receipts. The queue readiness marker is now true.
  The temporary ban-rule form remains unsubmitted pending the specific approval
  requested after automatic review rejection; no ban/rejoin outcome is claimed.

### Evidence readback, copy cleanup, and earlier stopped runtime

- In a fresh Helium admin tab, `/case view case:2` and View evidence displayed
  the private bounded evidence page with its saved attachment and `Evidence 1
  of 1`. The existing durable copy link reopened the original synthetic text
  after the intervening restarts. SQL remained at 50 audit events and 50 finished
  delivery receipts: these read actions did not add moderation history.
- `ec41e48` replaces bare capture-status headings such as `uploaded` with
  readable labels and changes the command description to `View case details.`
  Focused tests and the full backend/MySQL suite passed
  (`/tmp/evidence-copy-full.log`); `/tmp/quack-v5-evidence-copy-review` built.
- Fresh-tab case detail also rendered the correct stored tester ID as
  `@unknown-user`, and Discord's profile lookup said the user was inaccessible.
  Existing ticket messages in another Helium tab resolved the same member;
  the native tester remained in the guild. Source review found the correct
  string ID and intentional mention suppression, not a proven serialization
  defect. Missing cold-client member metadata is a hypothesis. Diagnosis would
  require sanitized returned content/mentions/flags, without interaction tokens;
  no permission changes, new target fetches, or mention-policy relaxation were made.
- After the interruption, both tmux panes were at `fish`. The beta log showed
  Ctrl-C followed by a clean shutdown at 16:56; its new copy build was not started.
  Previously prepared browser tabs/form were no longer present. At that point
  the explicit permission request for creating the temporary ban rule was unanswered; no live
  ban or successful rejoin had been established. The following rehearsal
  supersedes that stopped-state and permission status.

### September 8 live ban, direct evidence, appeal, and rejoin

- At approximately 17:12–17:14 local time, following explicit user approval for
  the beta restart, temporary ban rule, and monkey evidence/ban/appeal/unban/rejoin
  journey, `/tmp/quack-v5-evidence-copy-review` at `ec41e48` was running. The
  dashboard remained stopped. The appealable `Ban evidence rehearsal` template
  `01M21MAXSZ4Z4DJ6D3SB5XB9EQ` was created separately from the unchanged honeypot
  configuration.
- Administrator `/case add file:` created case #10
  (`01M21MPN3PGS1CQF3B7G8YXXZA`) for tester `498380784323919893`, directly
  uploading the 1,052-byte synthetic PNG `quack-ban-evidence-rehearsal.png`.
  SQL recorded the uploaded snapshot and preserved attachment at 17:12:27.254,
  before the ban started at 17:12:27.276 and succeeded at 17:12:27.784 on attempt
  1. This establishes direct-upload preservation before destructive enforcement;
  it does not establish preservation after separate source-message deletion.
- The native tester observed the ban DM; SQL recorded it sent at 17:12:28.521.
  While banned, the tester submitted an appeal and the administrator accepted it
  from the native staff queue. SQL recorded successful unban attempt 1 from
  17:13:28.358 to 17:13:29.839, with `reversal_of_execution_id`
  `01M21MPN3XPZ546WMD5RNN2BCR`, linking reversal to the original ban execution.
- The tester observed the accepted-appeal DM and its Rejoin Server button.
  Clicking opened the native invite with Accept as monkey; accepting returned
  the tester to the guild with a message composer. This closes the ordinary live
  accepted-appeal ban reversal and configured rejoin journey. Ban ownership
  conflicts, blocked DMs, restricted-role saved-copy access, source-message
  deletion, and the other release gates remain separate checks. No backend code
  changed and no Go tests were rerun for this evidence-only ledger update.
- After rejoin, the administrator opened preserved message
  `1547021831783981067` in evidence channel `1546756720037073008` and visually
  verified the expected gold/navy checkerboard in Discord's media viewer.
  The public case receipt showed the completed unban and voided status. The
  moderation log showed case creation, ban, appeal submission, acceptance,
  case voiding, and unban events. Old mentions now displayed monkey in that
  browser session; this is an observation, not proof of a mention-rendering fix.

### September 8 live honeypot burst

- Native tester monkey posted two synthetic messages in configured honeypot
  channel `1546871478274752543` in quick succession. Both appeared in Discord
  before disappearing, and the warning count advanced from four to five.
- SQL recorded one created incident `01M21NA6WN7Z0F1MPF0C5ESYN5` and only case
  #11 (`01M21NA8AMGNYH4TVHBMQTC4BS`). Its single timeout action succeeded on
  attempt 1. The first message's text was captured at 17:23:09.524 local time.
- Message cleanup receipts `1547024517103099974` and `1547024518193745952`
  point to that same incident and completed on their first attempts at
  17:23:09.746 and 17:23:10.590, after evidence and case persistence. This
  verifies live burst suppression and cleanup ordering for two messages; it
  does not establish interrupted-process recovery or capture of every burst
  message as separate case evidence.
- The one-minute timeout expired naturally; the native tester's message composer
  returned. No manual permission or punishment changes were needed.

### Statistics and automation policy boundaries

- `6c35709` composes staff statistics with the other core services. The HTTP
  adapter uses that shared service instead of constructing one from the combined
  repository. Statistics no longer requires an audit-write capability or builds
  discarded read events; authorization and bounded derived queries are unchanged.
  Focused core/API tests and the full MySQL-enabled backend suite passed.
- Honeypot setup, enablement, and policy-change handling now ask the template
  service to validate unattended compatibility. Integration no longer owns those
  rules or retains a combined repository field for them. Policy unavailability
  still maps to the honeypot sentinel, while transient storage errors remain
  distinct. Focused tests cover the compatibility rules, guild isolation, and
  adapter error mapping. These are composition changes, not a new live rehearsal.
- The integrated full backend suite passed with MySQL enabled; the beta remains
  on its previously verified binary until the next authorized runtime switch.

### Ticket gateway and operations lookup boundaries

- `4856039` moves operations-key guild identity lookup behind the guild service.
  Diagnostics still work without live Discord access, unknown guilds return 404,
  and session-based administrator authorization is unchanged. Core and route
  regressions passed, including the added unknown-guild case.
- Ticket gateway handlers now call purpose-built service reads instead of
  querying ticket tables. Deleted-channel lookup remains guild-scoped and
  includes resolved records; membership repair uses only open tickets with an
  exclusive ID cursor and fixed 100-row page. Discord effects stay in integration.
  Tests cover 103-row traversal, excluded guild/status rows, database errors,
  and deleted-channel forwarding without changing repair behavior.
- Full backend tests passed with MySQL enabled. This refactor has not replaced
  the running beta binary or closed outstanding live recovery/permission gates.

### Restricted moderator and revoked-role rehearsal

- The beta passed readiness. A read-only inspection confirmed monkey had no
  roles and the existing evidence channel retained only bot access plus the
  everyone View Channel deny. The inspection helper initially double-prefixed
  the configured authorization value, causing 401; using the runtime's exact
  token format corrected the helper without changing credentials or the bot.
- Temporary role `1547028302932476038` (Quack beta permission rehearsal) granted
  only Moderate Members and was assigned only to monkey. Native `/case view`
  opened case #10 privately, and View evidence opened its private saved-file
  metadata page. Existing storage-channel ACLs were not changed; this does not
  establish that a restricted moderator can open the stored file in that channel.
- A ban-template case request was denied privately. SQL remained at 11 cases,
  with latest case #11. The generic creation-denial copy did not explain the
  reason or next step and was flagged for a focused fix. Code tracing showed
  this request hit self-target rejection before the action-specific permission
  check; it is not live proof of a missing Ban Members rejection.
- The temporary role was deleted, then the same old View evidence control
  returned a private permission denial. A fresh REST read confirmed monkey's
  roles were empty again and the original evidence overwrites were unchanged.

### Case creation denial feedback

- The live generic self-target error led to a creation-specific private error
  mapper. Known reasons now explain self-target, target/bot role hierarchy,
  membership, or the exact failed Moderate Members/Kick Members/Ban Members
  requirement and give the appropriate next step. Core denial order and the
  no-case-on-denial invariant are unchanged.
- Typed permission metadata records the failed check rather than inferring it
  from the selected action. Tests distinguish self-target-before-ban-permission
  and revoked basic moderation authority from missing action permissions. Unknown
  reasons and internal metadata are not exposed. Existing-case reads, edits, and
  reversals retain context-neutral errors instead of claiming creation occurred.
- Focused core/command tests and the full backend suite with MySQL passed. The
  revoked-live-permission command test now checks the specific missing permission
  while retaining its private-response and no-queued-task assertions.
  The running beta still has the prior copy;
  this new feedback requires a later runtime switch for live verification.

### Ticket queue replacement and closure recovery

- Repair ticket now checks a saved staff queue post and replaces it only after
  Discord confirms that it is missing. A failed or uncertain read stops repair.
- Missing-message edits and queue destination changes return through durable
  send admission before a replacement POST. Lost responses and failed receipt
  writes retain that admission across restart, preventing blind duplicate sends.
- Retrying closure after a failed thread deletion verifies the saved transcript
  post. A definitely missing post clears the stale receipt and must be republished
  before source deletion; unavailable reads or publication preserve the thread
  and member reservation.
- Focused tests cover repair, rejected/uncertain sends, receipt persistence
  failure, restart, queue moves, and deletion retries. The transport integration
  test exercises the real service and adapter, verifies admission at the POST,
  and checks intact transcript content before deletion. The full backend suite
  with MySQL also passed. Live recovery acceptance
  remains open. Uncertain sends still require administrator inspection; a manual
  receipt reconciliation workflow has not been added.
- The separate blocked-DM regression (`b011be8`) verifies Discord 403/50007 is
  recorded as a definitive undelivered send with retained attempted content and
  no automatic resend. Account-level blocked-DM acceptance remains open.

### Live missing-ticket-queue repair

- Switched only the authorized beta pane `%1` to
  `/tmp/quack-v5-ticket-recovery-review`, built from `e091aad` with
  `vcs.modified=false`. Existing MySQL/Redis state was retained. `/readyz` passed
  all checks, including Discord, storage, queue, and action capabilities.
- Native Discord monkey opened ticket `01M21Q7TG4Y8KGG24E47NTMS6Y`, thread
  `1547032990302085164`, and sent the synthetic queue-recovery marker. Helium
  dickey deleted only that ticket's beta-authored queue post
  `1547033000859144266` in `1546774567782195239`.
- From the surviving thread View control, dickey selected Repair ticket and
  received private success feedback. SQL saved replacement post
  `1547033423158312971`; selecting Repair ticket again retained the same ID.
- Monkey closed through the surviving thread control. SQL recorded resolved,
  retained the replacement post as the transcript URL, and cleared the member's
  open-ticket reservation. Helium displayed the exact synthetic marker inside
  `ticket-01M21Q7TG4Y8KGG24E47NTMS6Y.txt` on that replacement post.
- Read-only beta REST verification returned Discord `10003` for the deleted
  thread and found exactly one matching transcript attachment in the queue's
  latest 100 messages, authored by beta `819019613371236432`. No pending synthetic
  ticket remains. This proves missing-post repair and normal closure after
  repair; failed deletion and uncertain-send recovery remain regression evidence,
  not live Discord acceptance.

### Live blocked member DM

- Used the existing Rehearsal rule, whose single default level has notifications
  enabled and no punishment actions. Both cases targeted only monkey.
- Turning off Quack's Pond Direct Messages did not block the beta DM: case #12
  (`01M21QM81DGZQ1APH017Y2MJM9`) remained valid and notification attempt 1 was sent.
  Native Discord showed that DM. This is not evidence of a rejection. Restored
  Direct Messages and Message requests to their original on values before the
  next attempt.
- Temporarily blocked Beta Bot in native Discord. Case #13
  (`01M21QS9EKTZP337ZJ54WV5G49`) remained valid; its private Helium moderator
  receipt updated to "The member's DM couldn't be delivered" while the public
  warning notice omitted notification status. SQL recorded notification failed,
  attempt_count 1, `dm_send_permission_or_hierarchy_denied`, an empty delivery
  message ID, NULL sent_at, and 230 bytes of retained rendered message.
- Removed the beta-only block. Native Discord confirmed user unblocked and
  restored the DM composer. A subsequent SQL read still showed one failed
  attempt for #13, with no automatic resend after unblocking. Both records are
  synthetic warning fixtures; this proves warning validity and notification
  failure feedback, not a separate ban-with-blocked-DM enforcement rehearsal.

### Captured image conversion and source deletion

- Monkey posted synthetic text and the checkerboard image in commands, message
  `1547036695801765940`. Helium dickey used its message context action and chose
  Rehearsal rule, creating valid warning #14 (`01M21R4BX11KP1N190NHM3SKST`).
  The initial capture retained text but marked the image metadata-only; the
  private receipt correctly warned that some evidence could not be saved.
- Read-only diagnosis found Discord metadata reported 550 bytes/image-webp while
  the attachment URL returned HTTP 200 with 726 bytes/image-png. Exact metadata
  size validation incorrectly rejected this converted representation.
- `0ce2511` bounds downloads by the existing absolute 25 MiB limit plus one byte
  instead. Returned bytes are preserved unchanged; read failures, oversized
  content and unsafe CDN URLs still fail. Original snapshot metadata stays as
  reported by Discord. Focused tests and the full backend/MySQL suite passed.
- Loaded clean build `/tmp/quack-v5-evidence-conversion-review` from `0ce2511` in
  the authorized beta pane; readiness passed. `/case evidence case:14` with the
  same source link created successful capture `01M21RH8CKYGM27YZ069FD04YE`, saved
  in evidence message `1547038685508149380`. The earlier failed capture remains
  recorded rather than being silently rewritten.
- Dickey deleted only the synthetic source. REST then returned `10008` for that
  source and confirmed the separate beta-authored saved PNG still existed with
  726 bytes. Helium opened the saved checkerboard in Discord's image viewer; SQL
  retained the captured source text. This verifies administrator file access
  after source deletion; restricted-moderator file access remains separate.

### Remaining uncertain ticket delivery recovery

- Review confirmed an operability gap: an uncertain send has a durable fence but
  no staff operation to record the result of inspecting the queue. Repair/Close
  cannot finish that state merely by restarting or retrying. A future bounded
  repair needs validated adoption of an existing beta-authored ticket post, or
  explicit administrator confirmation before admitting a replacement; it must
  preserve transcript-before-delete and reservation ownership.
- `8efd4b1` fixes the immediate misleading close error: wrapped unknown-delivery
  results retain private inspection guidance even when a ticket was returned,
  without claiming definite nondelivery or offering a blind Retry close. Focused
  tests and the full suite above passed. This copy fix does not resolve the
  missing reconciliation operation.

### Administrator recovery of uncertain ticket posts

- The missing operation above is implemented. Private View ticket offers Recover
  queue post only for an unresolved delivery on an open ticket or pending close.
  Existing ticket-content authority still applies; recording a recovery decision
  additionally requires current manager permission.
- Use existing post accepts a Discord message link and verifies the actual guild,
  recorded channel, current bot author, and matching ticket controls through fresh
  reads. It adopts the receipt without sending another message. Post was not sent
  requires a separate explicit confirmation before ordinary Repair ticket or
  Finish closing may publish a replacement.
- Every send has a durable attempt ID. Recovery decisions, late transport
  receipts, and nondelivery results cannot clear or overwrite a newer attempt.
  Older destination-only fences acquire a stable ID during recovery inspection.
  The decision and ticket event commit together; reconciliation retains the owner
  reservation and leaves transcript publication and deletion to normal closure.
- Focused transport, UI, ticket lifecycle, and SQLite/MySQL upgrade tests cover
  identity rejection, unavailable verification, current authority, stale controls,
  concurrent decisions, rollback, and delayed results. Live uncertain-send
  recovery remains pending; these tests do not claim a Discord crash rehearsal.

### Live receipt adoption and stale-control rehearsal

- `683c430` and `8f902c7` passed the full MySQL-enabled backend suite. Beta pane
  `%1` loaded the clean `8f902c7` binary and passed every readiness check.
- Monkey opened synthetic ticket `01M21SR5MX0V1QZK1ANX2GBWXJ` in thread
  `1547044033443139654`; its staff queue receipt was `1547044041462644819`.
  After recording that receipt, the rehearsal cleared only this ticket's saved
  message ID in the disposable local database. The Discord post remained intact.
  This is deliberate lost-receipt fixture injection, not a real crash at send time.
- Helium dickey used Join thread, Recover queue post, and Use existing post.
  The private form accepted the original message link and restored exactly the
  recorded receipt. A stale Post was not sent confirmation then returned explicit
  changed-state guidance; it did not clear the adopted receipt or send again.
- Parallel review found that transcript PATCHes appended files when editing an
  existing post. `b286ed2` explicitly replaces attachments with the new canonical
  upload. Its multipart regression covers three repeated updates and excludes
  prior attachment IDs. Full backend/MySQL tests passed; the clean build loaded in
  `%1` and passed readiness before owner closure.
- Monkey closed the recovered ticket. Helium displayed the retained synthetic
  text in the original queue post's transcript. SQL recorded resolved status,
  empty delivery attempt, and released member reservation. Read-only Discord
  inspection returned `10003` for the deleted thread and found exactly one
  matching transcript attachment in the latest 100 queue posts, authored by beta.
- Live coverage now includes adoption, stale-confirmation rejection, restart,
  and ordinary closure after recovery. A true interrupted send, successful
  nondelivery-confirmation replacement, and repeated transcript replacement
  remain separate from this rehearsal's evidence.
