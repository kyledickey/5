# Quack v5 product and implementation review

Reviewed September 7, 2026, on `v5/monolith` after the existing work was checkpointed. This review supports a product interview; it does not authorize or define the rewrite. The user's current direction takes precedence over older planning documents: finish the bot experience first, defer dashboard development, keep the template-based moderation idea, and make the audit log about meaningful moderation and administration events.

## Assessment

The central template/case model is implemented much more completely than the user journeys around it. The clearest problems are missing entry points, mismatched configuration contracts, inconsistent interaction flows, and technical events presented as product history. Renaming messages alone will not resolve these problems.

Keep the useful invariants while redesigning the workflows: guild isolation, versioned template snapshots, case numbers, all-time same-template escalation, non-destructive voiding, live authorization, durable action execution, explicit reversals, and target-owned member records. Whether individual product rules should change is an interview decision.

## Scope and evidence

This was a subsystem review across runtime composition, Discord commands/components/views, case/template/action/evidence/appeal services, optional modules, persistence and migrations, HTTP/auth contracts, dashboard feature routes/forms, and v4 migration behavior. I traced representative complete paths and searched callers to distinguish implemented services from reachable features. It is not a claim of a line-by-line correctness proof of every file or a live Discord acceptance test.

The source inventory contains 318 backend Go files including 88 test files, 106 dashboard TypeScript/TSX files including three test files, and 126 legacy Go files. Generated UI primitives and historical migration source snapshots were not reviewed as independent product features. Legacy command and module behavior was compared where it affects migration expectations.

Validation performed:

- Initial focused backend tests and `go test ./...` returned cached passes.
- Fresh `go test -count=1 -json ./...` passed outside the sandbox: 616 test/subtest pass events, 25 package passes, and 12 skipped external-storage tests. The sandbox attempt failed because miniredis could not bind local ports; the unrestricted rerun resolved those failures.
- Dashboard `bun run test`: three test files, five tests passed. `bun run typecheck` passed.
- Existing Go changes passed the gofmt check; `git diff --check` passed.
- Read the `quack` tmux bot and dashboard panes. Bot output showed case creation and sent notifications, plus general-logging failure messages. Those messages omit the error classification needed to establish their exact cause.
- The running bot is `/private/tmp/quack-original-response`, built from `e24143e` with `vcs.modified=true`. Its exact source snapshot cannot be inferred from that metadata. Source findings below refer to the checkpointed checkout.
- No live moderation actions, appeal submissions, ticket creation, database migration/import, or configuration changes were performed. External-storage skips and live Discord behavior remain unverified in this review.

## Existing work saved before review

| Commit | Boundary |
| --- | --- |
| `a475d8c` | `feat(discord): add Quack icon assets and shared message formatting` |
| `079eace` | `refactor(discord): unify case views and interaction presentation` |
| `91c151e` | `refactor(discord): update moderation and module notification copy` |
| `e65fa70` | `feat(dev): add Discord message and icon preview gallery` |

These preserve the pre-existing implementation, including its defects. They are checkpoints, not claims that the work is product-ready. `.DS_Store` was left untracked. No commits were pushed.

## Feature map

| Area | Implemented | Main gap |
| --- | --- | --- |
| Guild setup | Starter rule, settings, lifecycle, evidence-channel creation/repair | No Discord setup journey; dashboard settings contract is broken |
| Templates | Validation, versioning, thresholds, archive/restore, import/export | Admin setup depends on dashboard/API; terminology and complexity need decisions |
| Cases | Creation, history, view, void, replacement links in core | Fragmented Discord creation; JSON option; no outcome preview; difficult error recovery |
| Evidence | Message snapshots and managed copies of supported attachments | No direct file-upload journey; weak preservation feedback; Discord view omits captured text and warnings |
| Actions | Timeout/kick/ban, durable work, safe retries, failures, reversal | Execution IDs exposed in commands; public progress update is temporary; unresolved outcomes need clearer controls |
| Member notifications | One case-level notification after outcome, pre-opened DM for removal actions | Appeal link depends on dashboard configuration; long messages become text files |
| Appeals | Case ownership, form snapshots, one appeal, transitions, atomic acceptance/void, notification outbox | New appeal UI is absent; no Discord submit/review workflow |
| Audit | Immutable event history, filtering, Discord mirror | Reads and worker delivery bookkeeping occupy user history |
| Tickets | Private provisioning, permission repair, replies, closure, transcript, rate limits | Entry panel never posted; owner gets unusable Close control; HTTP lifecycle misses Discord effects |
| Honeypots | Trap matching, exemptions, one-message claims, normal case application, drift disable | Configuration switches disconnected; startup-only subscriptions; no setup/test journey or failed-trigger recovery |
| General logging | Routed Discord events, bounded cache, privacy settings, delivery retries | No usable configuration journey; disabled/unrouted events become error logs |
| Statistics | Derived case/action/appeal/audit counts | Audit activity is polluted by read/worker events; no Discord statistics command |
| Dashboard/API | Auth, scoped routes, many staff/member views | Dashboard has concrete incomplete features and contract drift despite green tests |
| v4 migration | Historical JSONL import, identity/fingerprint ledger, dry run/rollback/cutover | Actual v4 data and optional-module migration expectations need decisions |

## Confirmed findings

### 1. Audit noise comes from the product event model

[`TemplateService.List`](../apps/backend/internal/quack/templates.go) writes `case_template.read` on every successful list. `ListActive` calls it, so Discord autocomplete and repeated template resolution create audit rows. Other case, settings, member, appeal, and statistics reads have analogous writes.

[`AuditMirrorWorker.process`](../apps/backend/internal/quack/audit_mirror.go) writes `audit_mirror.skipped` when no mirror channel is configured, and records delivery/failure outcomes in the same audit table. [`ListPendingAuditMirrorEntries`](../apps/backend/internal/store/audit_statistics.go) uses those rows as delivery state. It excludes mirror outcomes from re-mirroring; this is not an infinite recursive loop, but it still adds service bookkeeping to moderation history.

[`ListAuditLogEntriesFiltered`](../apps/backend/internal/store/audit.go) does not default to an end-user event allowlist. The dashboard presents that general stream. The `Important` classification in [`audit_contract.go`](../apps/backend/internal/quack/model/audit_contract.go) currently controls mirroring, not the complete user-visible audit feed.

This also follows the old [product document](../v5.md), which explicitly includes successful permission-sensitive reads. That rule must change to match the current request. Merely hiding two labels in the dashboard would leave the underlying mismatch intact.

### 2. Module switches do not control runtime module settings

[`SettingsForm`](../apps/dashboard/src/features/settings/settings-form.tsx) submits `tickets_enabled`, `general_logging_enabled`, and `honeypot_enabled`. [`UpdateGuildSettings`](../apps/backend/internal/store/guild_settings.go) saves them in `guild_settings`.

The optional modules read `module_configurations.enabled` and module-specific `config_json` through the [module registry](../apps/backend/internal/modules/registry.go). The inspected update path does not synchronize these representations. A saved core switch is therefore not proof that the corresponding module was enabled or configured. Dedicated module routes exist, but there are no corresponding dashboard feature pages or Discord setup commands.

### 3. The dashboard settings form submits a forbidden field every time

The same form always includes `managed_evidence_channel_discord_id`. [`applyGuildSettingsInput`](../apps/backend/internal/quack/settings.go) rejects any non-nil value because Quack owns that channel. An otherwise valid form submission reaches this rejection before it can save. This affects notification text and the other settings in the same form too.

### 4. New appeals have no member submission UI

The [appeal route](../apps/dashboard/src/routes/guilds/$guildId.cases.$caseRef.appeal.tsx) renders existing appeals, but always displays “Appeal form temporarily unavailable” when a case is eligible for its first appeal. There is no new-appeal submit function in the [dashboard API client](../apps/dashboard/src/lib/api.ts). The message is an unimplemented branch, not evidence of a transient loading failure.

The [backend submit endpoint](../apps/backend/internal/httpapi/routes/member_appeals.go) and [appeal service](../apps/backend/internal/quack/appeals.go) exist. The member read contract does not supply the configured questions for a first submission, and the settings read route is staff-only. The form contract and journey must be completed together.

### 5. Discord appeals are entry links plus an isolated reversal handler

[`RegisterAppealComponents`](../apps/backend/internal/discordbot/appeal_components.go) registers only `appeal:reverse`. No Discord appeal-submit, queue, accept/reject, or request-information command is registered. [`AppealStaffMessage`](../apps/backend/internal/discordbot/ui/views/appeal.go) exists but has no production caller. [Staff outbox notifications](../apps/backend/internal/discordbot/appeal_notifications.go) send text directing staff to the dashboard rather than posting an actionable review card.

The core acceptance/voiding behavior is valuable and exists in [`TransitionAppeal`](../apps/backend/internal/store/appeals.go). The incomplete surface should not be mistaken for an absent state machine.

### 6. Appeal access is indirectly configured through CORS

[`dashboardBaseURL`](../apps/backend/internal/quack/app.go) chooses the first HTTPS CORS origin as the notification destination. [`AppealEntryMessage`](../apps/backend/internal/discordbot/ui/views/appeal.go) requires an HTTPS base URL. CORS policy and a member-facing application URL are different settings, yet they are coupled here. Discord-only appeals or even a minimal web appeal portal need an explicit product and configuration decision.

### 7. Ticket entry buttons are implemented but never published

[`EntryComponents`](../apps/backend/internal/modules/tickets/components.go) builds Open ticket and Staff queue buttons. Caller search found only a test, not production publication. The [command registry](../apps/backend/internal/discordbot/commands/registry.go) registers `/case`, the message case action, and a dev-only preview. There is no ticket setup command or member open command. V4 [did post an entry panel](../Legacy/commands/ticket-channel.go), so this is a concrete loss of reachability.

### 8. Ticket controls disagree with ticket lifecycle and permissions

[`ticketControls`](../apps/backend/internal/moduleintegration/ticket_components.go) gives the owner a Close button; its handler calls [`DiscordAdapter.Close`](../apps/backend/internal/modules/tickets/discord.go), which requires staff moderation authority. Owner cancellation exists in the adapter but is not registered as a control. Controls also remain the same for closed tickets.

Ticket HTTP resolve/cancel/reopen handlers call the [service directly](../apps/backend/internal/modules/tickets/routes.go), bypassing the Discord adapter. They can update database status without archiving/unlocking the Discord conversation. Reopen has no adapter operation that restores the thread to a usable state. The HTTP resolve endpoint accepts transcript content supplied by its caller instead of capturing it from Discord.

### 9. Ticket conversations and transcripts need one coherent model

Native thread messages are available to the [transcript capture](../apps/backend/internal/moduleintegration/discord_clients.go), but the durable ticket timeline receives replies through the modal/service path. A user typing normally and a user pressing Reply are taking different recording paths. Bot-posted modal replies do not identify the original speaker in their Discord text; the transcript sees the bot as author. Captured attachments are listed by name and size, not preserved file content.

These are product decisions: natural conversation versus mediated replies, staff attribution, transcript contents, owner closure, reopen, and retention.

### 10. Honeypot enablement can require a restart without saying so

[`RequiredGatewayIntents`](../apps/backend/internal/moduleintegration/honeypot.go) examines enabled module rows at startup. [`runtime.Run`](../apps/backend/internal/runtime/runtime.go) sets the connection's subscriptions once. If no applicable module was enabled then, changing honeypot settings later does not reconnect the gateway with newly required subscriptions. The exact effect depends on the subscriptions already active for other guilds/modules.

### 11. Honeypot testing and failure recovery are opaque

The [event projection](../apps/backend/internal/moduleintegration/honeypot.go) treats Administrator, Moderate Members, Kick Members, Ban Members, and Manage Guild as exempt authority. Bots, webhooks, and configured exempt roles are also excluded. Testing with a privileged account is therefore expected not to create a case.

All required context fields are rejected by the honeypot template validator, including message-link fields that automation could potentially fill. [Trigger claims](../apps/backend/internal/modules/honeypot/service.go) are per message, so a burst can produce multiple cases for one member. An interrupted pending claim or a failed application has no inspected retry/reconciliation worker; replay returns duplicate. The in-memory queue also sheds work when full. These limits differ from the recovery machinery for already-created case actions.

V4 created a warning channel, deleted trap messages, banned, and updated a counter. V5 configures an existing channel and applies a template; there is no equivalent warning-panel/counter/setup experience. Trigger-message removal only follows from configured ban history deletion, not a general honeypot cleanup step. Desired parity needs to be explicit.

### 12. Evidence means message capture, not direct upload

The [case input](../apps/backend/internal/quack/case_types.go) accepts message links, and the [capture service](../apps/backend/internal/quack/evidence.go) snapshots messages and copies their supported attachments. The Discord command offers a single `message_link` plus template context fields. There is no attachment option or standalone upload-and-attach endpoint in the inspected create workflow.

Captured message authors must equal the case target. This excludes witness messages, conversation context from other users, and a moderator uploading a screenshot into a channel and linking that message as evidence against someone else. That restriction may be intentional, but it conflicts with several ordinary meanings of “upload evidence.”

### 13. Evidence feedback and inspection omit the information staff need

[`CaseCreatedMessage`](../apps/backend/internal/discordbot/ui/views/case.go) displays case/outcome information without capture warnings. [`evidenceSummary`](../apps/backend/internal/discordbot/ui/views/case_moderator.go) shows the original message link and attachment copy codes, but does not render the captured text, `CaptureWarning`, or attachment warning explanations. A deleted original can leave staff clicking a dead source link even though the snapshot is stored.

The capture service retains warnings for unavailable, unsupported, oversized, and failed copies. These need a separate private confirmation/preview and a useful case evidence view, consistent with the limited public result.

### 14. Evidence fallback and preservation lifecycle have rough edges

[`validateCaseContextValues`](../apps/backend/internal/quack/case_context.go) permits unavailable-message fallback when any other context exists, including a boolean or number. There is no explicit “continue without this snapshot” decision after the failure. [`authorizeEvidenceSource`](../apps/backend/internal/discordbot/channel_security.go) can reject inaccessible channels before the typed unavailable-message error is produced, so unavailable paths are not all equivalent.

Attachments are copied before the [case transaction](../apps/backend/internal/quack/cases.go). If the transaction fails or redoes preflight after concurrent changes, already-uploaded files are not rolled back by that transaction. The copy adapter stores message/attachment IDs and URLs, but the inspected case read path returns stored URLs rather than refreshing them. Long-term usability of those links needs live verification.

The [managed channel](../apps/backend/internal/discordbot/evidence.go) explicitly grants access only to the bot, with administrator access implicit in Discord. Ordinary moderators are expected to use authorized case views. Repair also replaces the channel name/topic/overwrites. Decide whether this is hidden storage or a staff workspace before changing its permissions.

### 15. Case creation has different flows for the same rule

[`HandleMessageCaseInteraction`](../apps/backend/internal/discordbot/commands/case.go) redirects staff to `/case add` when the sole active template has ordinary context fields. With multiple templates, the [selection handler](../apps/backend/internal/discordbot/commands/case_context.go) can open a modal for those fields. The number of active templates changes what the user can do.

The context menu offers at most the first 25 templates without paging. `/case add` exposes JSON as an alternative to the form. Pasting a slash-command message link does not prefill its message-link field in the initial modal path, although the link is retained as separate evidence.

### 16. Form recovery is fragile

The [draft store](../apps/backend/internal/discordbot/commands/case_drafts.go) is process-local and expires after 15 minutes. [`handleContextModal`](../apps/backend/internal/discordbot/commands/case_context.go) deletes the draft before creation succeeds. It does not offer a review/edit/retry step after failure. A restart loses open drafts.

Booleans are typed as `true` or `false`. Short-text inputs allow 1,000 characters in the modal while the core accepts 500. Error mapping turns many concrete validation/evidence failures into “That case request is invalid.” A user should not have to infer the field or preservation problem from that response.

### 17. Deferred public responses make later failures public

Creation and several controls call `DeferPublic` before full preflight/action checks complete, then use `ErrorEdit` on that original response when checks fail. That edit does not convert the existing response to private. The current design therefore does not preserve the old rule that validation and permission failures stay private. This is visible in [`case.go`](../apps/backend/internal/discordbot/commands/case.go), [`case_context.go`](../apps/backend/internal/discordbot/commands/case_context.go), and the [response helper](../apps/backend/internal/discordbot/ui/responses.go).

The selected outcome is also committed without a moderator preview/confirmation in normal creation. Whether the fast path is appropriate for a fifth-case ban is a product question.

### 18. Successful backend recovery does not guarantee an updated Discord result

[`updatePublicCaseResult`](../apps/backend/internal/discordbot/commands/case_result.go) follows an action for at most 30 seconds in a process-local goroutine. There is no durable association in that path that lets a restarted worker update the command response. A later success can leave the original response saying queued. The case remains queryable, but the result should make that transition or recovery path clear.

Action execution itself has stronger protection: persisted actions, leases, safe retry classification, uncertainty handling, and polling. These are useful to retain while simplifying what staff see. Commands that require an execution ID should become controls attached to a case/outcome wherever possible.

### 19. Some developer error messages describe expected non-events

The [general-logging queue](../apps/backend/internal/modules/generallogging/queue.go) logs every returned error as delivery failure. The [service](../apps/backend/internal/modules/generallogging/service.go) returns `ErrDisabled` and `ErrNoDestination` for normal disabled/unrouted events. Honeypot workers similarly log exempt/duplicate/non-trap outcomes as failures. This explains why an error line alone cannot prove actual delivery broke. Developer logs need useful classifications even after they are removed from the product audit feed.

### 20. Code and documentation reflect implementation packages more than product workflows

- Core appeals and the audit mirror are composed inside the optional-module runtime, while `quack.Services` has neither an Appeals nor Statistics field. Turning off or reworking module composition can unintentionally affect core features.
- Discord formatting is imported by core case/appeal services. Durable appeal notifications store rendered copy. A clearer boundary would store notification intent/data and render it in the Discord adapter, subject to historical-message requirements.
- `Services` exposes the combined repository and configuration; some command code directly reads storage. Narrow use cases exist, but adapters can bypass them.
- Case command logic is spread across definition, helpers, create, context, drafts, components, controls, result, and pagination files. Splitting files is not inherently bad; the missing piece is a single understandable workflow and state owner.
- A search found 125 production-code lines using formulaic comment patterns or old QP/QI identifiers. Comments such as “encapsulates the … rule” add little explanation of the actual invariant.
- [`README.md`](../README.md) calls the dashboard root empty. [`v5-scope-drift.md`](v5-scope-drift.md) says no storage/integration product mismatch remains. [`v5-readiness.md`](v5-readiness.md) describes an older anchor and distinguishes missing live evidence, but its implementation-complete claims are not a reliable account of these current user journeys.
- Statistics load case/action/appeal/audit rows into memory for aggregation; frequent read events inflate audit activity. Optimize this after agreeing which statistics matter.

## Direction to discuss, not an implementation mandate

Use the same case workflow from a slash command and message action: select member/rule, collect context and evidence, show the selected outcome and preservation status when appropriate, confirm, then show a concise result with useful controls. Preserve a draft through correctable errors.

Give every launch feature a complete setup, entry, success, failure, and recovery path. Decide explicitly whether setup and appeals live in Discord or a small retained web surface during the bot-first phase. Retaining an API for future dashboard work does not require building the dashboard now.

Separate moderation/admin audit events, case-specific execution detail, optional Discord event logs, and operator diagnostics by purpose. Keep permanent accountability for meaningful decisions without making reads, queue activity, or mirror delivery state user events. Old audit rows should not be deleted as an incidental cleanup.

Build logical commits around complete changes: one configuration source; one case flow; evidence capture and inspection; appeal submission/review; ticket lifecycle; honeypot setup/recovery; audit event semantics; then copy/readability cleanup where it belongs. The exact order and scope depend on the [product interview](v5-product-interview.md).

Acceptance should include ordinary members and restricted moderators completing real journeys in a designated test guild. Green service tests and attractive synthetic preview messages do not establish that a feature is discoverable or usable.
