# Quack v5 Remaining Work

This checklist tracks unfinished work in this backend repository for the product defined in [`v5.md`](v5.md). It excludes implementation of the separate dashboard web application, but includes the backend contracts and behavior that the dashboard needs.

Items are grouped by concern, not by implementation order or priority. Completed infrastructure is intentionally omitted.

## Product Model Alignment

- [x] Remove escalation time windows from live domain models, template requests, responses, snapshots, validation, and tests while retaining frozen compatibility columns only.
- [x] Change escalation counting to use all-time non-voided cases only.
- [x] Remove severity from templates, cases, API contracts, live storage records, snapshots, filters, and tests while retaining frozen compatibility columns only.
- [x] Remove weight from cases, escalation behavior, live storage records, API contracts, and tests while retaining the frozen compatibility column only.
- [x] Remove the separate enabled state from live templates, levels, and level actions while retaining frozen compatibility columns only.
- [x] Make reversible archive and restore the only template availability lifecycle.
- [x] Remove soft-delete concepts that imply templates or cases can be deleted outside the defined archive/void flows.
- [x] Remove moderator reason overrides from case inputs, HTTP routes, Discord commands, snapshots, and tests.
- [x] Enforce zero or one timeout, kick, or ban action per template level.
- [x] Remove ordered multi-action behavior and `continue_on_error` from the product model.
- [x] Remove action-level notification settings now that each case has one level-owned notification decision.
- [x] Remove technical action settings that are no longer admin-facing, including configurable backoff, execution timeout, and idempotency scope.
- [x] Retain only the admin-configurable safe retry-count limit in template action contracts.
- [x] Replace mixed case lifecycle statuses with valid or voided case state.
- [x] Keep action execution and appeal statuses separate from case validity.
- [x] Remove note-related case event types and note-oriented model fields from the live v5 boundary while preserving retired rows for compatibility.
- [x] Remove free-form staff and member case-note expectations from backend contracts and documentation.
- [x] Restrict normal case sources to dashboard, Discord, honeypot automation, and v4 import where appropriate.
- [x] Preserve existing v5 data through explicit compatibility migrations while rejected fields are retired.
- [x] Update all current template and case JSON contracts and snapshots to match the simplified v5 model.
- [x] Update technical documentation after each model correction so it continues to describe running code.
- [x] Remove resolved entries from `docs/v5-scope-drift.md` only after code and tests match `v5.md`.

## Guild Setup and Settings

- [x] Add guild settings records and repository methods for core Quack configuration.
- [x] Add settings for the Discord audit-mirror channel.
- [x] Add settings for the managed evidence channel.
- [x] Add settings for the optional member-notification introduction and footer.
- [x] Add settings for independently enabling tickets, general logging, and honeypots.
- [x] Add backend read and write APIs for guild settings.
- [x] Restrict guild settings changes to the owner, `Administrator`, or `Manage Guild` as defined by the setting.
- [x] Audit successful, failed, and denied guild-setting changes.
- [x] Bootstrap a guild when Quack is first installed instead of waiting for an arbitrary dashboard request.
- [x] Create the active General rule violation starter template during guild bootstrap.
- [x] Seed starter cases 1–2 as case-only notifications.
- [x] Seed starter cases 3–4 with a 24-hour timeout.
- [x] Seed starter case 5 and later with a ban and 24-hour message-history deletion.
- [x] Make starter-template cases appealable and member notifications enabled at every level.
- [x] Store whether the one-time starter-policy review notice has been shown.
- [x] Expose the starter-policy notice through the backend for the dashboard setup flow.
- [x] Create and permission the staff-only evidence channel during guild setup.
- [x] Detect and report evidence-channel permission drift.
- [x] Add a safe repair flow for a missing, deleted, or misconfigured evidence channel.
- [x] Handle Discord guild create, update, leave, and rejoin events.
- [x] Preserve guild history when Quack leaves instead of hard-deleting guild data.
- [x] Define and implement reactivation behavior when Quack rejoins a known guild.
- [x] Refresh stored guild name, icon, owner, and active state from Discord events.
- [x] Clean or repair channel references when configured Discord channels are deleted.
- [x] Document the Discord install permissions and privileged intents required by core and optional modules.
- [x] Reduce hard-coded Discord intents to the minimum enabled feature set.

## Discord Identity and Permissions

- [x] Refresh Discord guild membership and permission bits before every sensitive write or enforcement operation.
- [x] Stop relying on session-time permission snapshots for sensitive authorization.
- [x] Treat stored staff-member rows as audit/display caches only and never as an independent source of permission.
- [x] End former staff access on their next protected request while preserving historical attribution.
- [x] Grant full guild access to the guild owner and members with `Administrator`.
- [x] Grant template and module configuration to members with `Manage Guild`.
- [x] Grant case creation, case review, appeal review, audit reads, case voiding, and failure dismissal to members with `Moderate Members`.
- [x] Allow all moderators to read the complete audit log instead of limiting it to `Manage Guild`.
- [x] Require `Moderate Members` for selected timeout actions.
- [x] Require `Kick Members` for selected kick actions.
- [x] Require `Ban Members` for selected ban actions.
- [x] Require the matching current Discord permission for every manual action retry or reversal.
- [x] Check the moderator's role hierarchy against the target before punitive case creation.
- [x] Check the Quack bot's role hierarchy against the target before punitive case creation.
- [x] Check the Quack bot's action-specific Discord permissions before case creation.
- [x] Block the entire case request when the selected level cannot be enforced by the actor or bot.
- [x] Reject normal cases targeting the acting moderator.
- [x] Reject normal cases targeting bot accounts, including Quack.
- [x] Reject normal cases targeting the guild owner.
- [x] Require normal template application targets to be current human guild members.
- [x] Keep imported history and explicit reversal flows able to reference departed members without weakening normal target rules.
- [x] List dashboard guild access for moderators with Quack capabilities instead of requiring `Manage Guild` for every staff entry point.
- [x] Provide a member-facing guild/case entry path that does not depend on the banned user still appearing in Discord's guild list.
- [x] Add consistent authorization errors for dashboard and Discord callers.
- [x] Audit denied sensitive operations with actor, guild, requested capability, request ID, and correlation ID.
- [x] Add permission-matrix tests covering owner, Administrator, Manage Guild, Moderate Members, Kick Members, Ban Members, former staff, and ordinary members.
- [x] Add hierarchy tests covering moderator, bot, owner, peer-role, and higher-role targets.

## Templates and Escalation

- [x] Add simple structured template context-field definitions.
- [x] Support short-text context fields.
- [x] Support long-text context fields.
- [x] Support boolean context fields.
- [x] Support number context fields.
- [x] Support Discord-message-link context fields.
- [x] Support required and optional context fields.
- [x] Include context definitions in template create, update, get, list, import, export, and snapshot contracts.
- [x] Validate context-field names, labels, types, ordering, and required flags.
- [x] Treat all submitted context fields as member-visible.
- [x] Keep the official template reason fixed during case creation.
- [x] Require exactly one default level on every template.
- [x] Allow the default level to create a case without an enforcement action.
- [x] Validate positive case-count thresholds for every non-default level.
- [x] Reject duplicate or ambiguous threshold definitions.
- [x] Select the highest reached threshold and continue using it until a higher threshold is reached.
- [x] Count the new case when evaluating escalation.
- [x] Count matching non-voided cases across all versions of the same template identity.
- [x] Exclude voided and imported-v4 historical cases from escalation.
- [x] Preserve immutable case snapshots when templates are edited.
- [x] Ensure template edits increment the version without creating a new escalation identity.
- [x] Implement reversible template archive behavior.
- [x] Implement template restore behavior.
- [x] Keep archived templates readable but unavailable for new cases.
- [x] Prevent archived templates from appearing in Discord autocomplete.
- [x] Add template export with policy fields only.
- [x] Exclude guild IDs, Discord channel IDs, history, audit data, and secrets from template exports.
- [x] Add template import validation and confirmation.
- [x] Create a new guild-owned template identity for every import.
- [x] Make valid imported templates active immediately after confirmation.
- [x] Validate timeout duration as a required setting for timeout levels.
- [x] Validate Discord-supported message-history deletion values for ban levels.
- [x] Validate the safe retry-count limit.
- [x] Audit template create, update, archive, restore, import, and export success and failure.
- [x] Add backend contract tests for every template request and response shape.
- [x] Add template-version and cross-version escalation tests.
- [x] Add archive/restore and import/export round-trip tests.

## Case Creation and History

- [x] Replace reason-override inputs with structured context submissions.
- [x] Validate every required template context field before creating a case.
- [x] Reject unknown, duplicate, or incorrectly typed context values.
- [x] Snapshot submitted context with the case.
- [x] Snapshot the selected level and its zero or one action with the case.
- [x] Keep case creation, escalation selection, numbering, snapshot, event, action row, notification work record, and audit insertion atomic.
- [x] Validate target membership, actor permission, bot permission, and both role hierarchies before committing the case.
- [x] Keep case numbers unique, sequential per guild, and never reusable after voiding or import.
- [x] Add case void service behavior with a required reason.
- [x] Add case void storage behavior and immutable case event.
- [x] Remove voided cases from future escalation counts without deleting their records.
- [x] Add backend endpoints for voiding cases.
- [x] Prevent edits that change a case target, template, selected level, reason, or action.
- [x] Preserve an explicit replacement-case reference when staff recreate an incorrect case.
- [x] Keep terminal action failures from automatically voiding the case.
- [x] Separate action progress and appeal progress from case validity in all case responses.
- [x] Add staff case search by case number, member, moderator, template, validity, action result, appeal status, and date.
- [x] Add staff case pagination and stable sorting for large guilds.
- [x] Add staff member-history summaries derived from valid and voided cases.
- [x] Add member-owned case list and case-detail services.
- [x] Authorize member case access by target Discord identity rather than current guild membership.
- [x] Allow banned and departed members to read only cases targeting their Discord identity.
- [x] Include valid and voided cases in member history with clear labels.
- [x] Hide moderator identities from member-facing case responses.
- [x] Hide raw Discord errors, worker IDs, internal retry fields, and technical action payloads from member-facing responses.
- [x] Show official reason, visible context, evidence, selected outcome, public history, and appeal state to the member.
- [x] Define and enforce public, staff, and internal case-event response views without reintroducing free-form notes.
- [x] Audit permission-sensitive case and history reads.
- [x] Add concurrency tests for simultaneous case creation and voiding in the same guild.
- [x] Add authorization tests proving members cannot enumerate other users or guild records.

## Evidence Capture and Preservation

- [x] Add domain models and storage records for message evidence snapshots.
- [x] Add domain models and storage records for attachment metadata and preserved copies.
- [x] Link evidence records to a case without embedding transport-specific Discord objects in the core.
- [x] Add a shared evidence-capture service used by Discord and HTTP case creation.
- [x] Parse and validate Discord message links.
- [x] Verify that a pasted message belongs to the selected guild.
- [x] Verify that Quack can access the linked channel and message.
- [x] Fetch live message text, author, timestamps, IDs, embeds, and attachments.
- [x] Snapshot message content before case creation commits.
- [x] Add a Discord message context action for starting a case from a live message.
- [x] Derive the target member from the selected message author in the context-action flow.
- [x] Add template selection and structured-context collection after the message context action.
- [x] Allow pasted message links in Discord case creation.
- [x] Allow pasted message links through backend case-creation contracts used by the dashboard.
- [x] Re-upload supported attachments to the managed staff-only evidence channel.
- [x] Store copied evidence message and attachment references.
- [x] Enforce Discord upload size and file-type limits without blocking unrelated case creation.
- [x] Preserve filename, content type, size, and original URL when an attachment cannot be copied.
- [x] Warn staff when attachment bytes were not preserved.
- [x] Require explicit visible context when a linked message is already deleted or inaccessible.
- [x] Prevent evidence capture from silently changing the case target.
- [x] Present captured evidence to the affected member through an authorized backend response.
- [x] Avoid exposing the staff-only evidence channel or unrelated evidence records to members.
- [x] Handle expired Discord attachment URLs by resolving the managed evidence copy when available.
- [x] Define evidence behavior when the managed channel is deleted between capture and read.
- [x] Audit evidence capture success, partial capture, and failure.
- [x] Add limits for message length, embed count, attachment count, and total capture work.
- [x] Add tests for live messages, deleted messages, inaccessible channels, wrong-guild links, oversized files, unsupported files, and partial capture.

## Discord Enforcement Actions

- [x] Expand the Discord action port beyond direct messages to timeout, kick, ban, timeout removal, and unban operations.
- [x] Implement real `timeout_user` execution.
- [x] Implement real `kick_user` execution.
- [x] Implement real `ban_user` execution.
- [x] Apply the template-defined timeout duration exactly.
- [x] Apply the template-defined ban message-history deletion window exactly.
- [x] Generate Discord audit-log reasons from the case number and official reason.
- [x] Classify Discord validation, permission, hierarchy, unknown-member, rate-limit, server, timeout, and network failures.
- [x] Mark only known-safe failures as automatically retryable.
- [x] Never automatically retry uncertain irreversible kick or ban outcomes.
- [x] Enforce the admin-configured retry-count limit for safe automatic retries.
- [x] Keep retry timing and backoff internal to Quack.
- [x] Add execution timeouts for outbound Discord calls.
- [x] Add idempotency protection around every enforcement attempt.
- [x] Prevent duplicate execution when the same case is submitted repeatedly.
- [x] Prevent duplicate execution when Discord or the HTTP caller retries a case-creation request.
- [x] Recover action rows left in `running` after a worker crash by using a bounded claim lease.
- [x] Record every action attempt, response summary, error classification, and final outcome.
- [x] Redact secrets and unnecessary Discord payload data from action attempts and audit metadata.
- [x] Keep failed enforcement from invalidating or hiding the case.
- [x] Add a failed-action review query and backend response.
- [x] Add manual retry service and storage behavior.
- [x] Recheck actor permission, bot permission, target membership, and hierarchy before manual retry.
- [x] Add failure-dismissal service and storage behavior without deleting attempt history.
- [x] Add retry, dismiss, and void backend endpoints.
- [x] Add immutable audit entries for retry requests, retry results, dismissals, and voids.
- [x] Add timeout-removal as a staff-confirmed reversal operation, not a template action.
- [x] Add unban as a staff-confirmed reversal operation, not a template action.
- [x] Require matching Discord permission and hierarchy for reversals.
- [x] Attach reversal attempts and results to the original case and accepted appeal where applicable.
- [x] Add mocked Discord tests for timeout, kick, ban, timeout removal, unban, rate limits, retries, and ambiguous failures.
- [x] Add action idempotency and crash-recovery integration tests.

## Member Notifications

- [x] Replace generated `send_dm` action rows with one case-level notification workflow.
- [x] Remove separate level and action DM paths that can notify twice for one case.
- [x] Let only the selected level decide whether the member is notified.
- [x] Prevent moderators from changing notification behavior during case creation.
- [x] Prepare or open the member DM channel before kick or ban when needed.
- [x] Send the single structured notification after the enforcement outcome is known.
- [x] Include guild name, official reason, visible context, outcome, case reference, and appeal access.
- [x] Include the optional guild-specific introduction and footer without supporting arbitrary executable templates.
- [x] Clearly state whether Discord enforcement succeeded, failed, or requires staff review.
- [x] Record notification delivery separately from enforcement outcome.
- [x] Keep DM failure from changing case validity or member dashboard visibility.
- [x] Surface DM delivery failures to staff without creating a second punitive action failure.
- [x] Make notification sending idempotent so worker and request retries cannot send duplicates.
- [x] Ensure action retry does not resend the original case notification unless staff explicitly requests another notice.
- [x] Add notification rendering tests for case-only, timeout, kick, ban, failed enforcement, appealable, non-appealable, and DM-failure cases.
- [x] Add integration tests proving one case produces at most one automatic member notification.

## Discord Moderator Experience

- [x] Remove the free-form reason option from `/case add`.
- [x] Add structured template-context prompts to `/case add`.
- [x] Use Discord modals or components for context that cannot fit slash-command options.
- [x] Keep template selection autocomplete limited to active templates.
- [x] Show required-context validation errors privately.
- [x] Keep successful case summaries public in the invoking staff channel.
- [x] Keep public case summaries limited to case number, target, template, level, and action status.
- [x] Update the public case summary after asynchronous action and notification outcomes are known.
- [x] Add `/case view` for authorized staff case detail.
- [x] Add `/case list` for authorized staff case browsing.
- [x] Add `/case user` for authorized member history.
- [x] Add stable case pagination buttons.
- [x] Add case detail embeds that separate validity, action result, appeal state, context, and evidence links.
- [x] Add member-history embeds without exposing other guilds or hidden technical details.
- [x] Add a Discord failed-action review view.
- [x] Register real retry, dismiss, and void button handlers.
- [x] Require a void reason through a modal before voiding from Discord.
- [x] Register the Discord message context command for evidence-backed case creation.
- [x] Add an appeal entry button or secure dashboard link to eligible case notifications.
- [x] Add Discord views for staff to inspect appeal status and case-linked review history.
- [x] Add Discord audit-mirror rendering for important moderation events.
- [x] Keep Discord audit-mirror messages separate from general logging output.
- [x] Remove legacy direct moderation commands after migration.
- [x] Prevent v4 and v5 command-name collisions during coexistence.
- [x] Remove placeholder component and modal handlers as real workflows are registered.
- [x] Add interaction deduplication using Discord interaction IDs.
- [x] Add tests for command definitions, permissions, autocomplete, context modals, message commands, pagination, buttons, deferred edits, and failure recovery.

## Authentication and Backend API

- [x] Add member-authenticated routes for listing and reading the caller's own cases.
- [x] Add member-authenticated routes for creating, reading, and updating the caller's appeal.
- [x] Keep member access independent of current guild membership when a case targets their Discord ID.
- [x] Add staff routes for case voiding, failed-action review, retry, dismissal, reversals, and staff statistics.
- [x] Add admin routes for template import, export, restore, context definitions, and guild settings.
- [x] Add backend routes for audit-mirror configuration.
- [x] Add backend routes for optional-module settings and status without implementing dashboard UI.
- [x] Standardize structured API error responses with stable error codes, request IDs, and correlation IDs.
- [x] Keep status codes and error bodies consistent across validation, authentication, authorization, conflict, and dependency failures.
- [x] Add complete contract tests for staff, admin, member, former-member, and unauthenticated API access.
- [x] Document the dashboard-facing request and response contracts maintained by this repository.
- [x] Add configurable CORS origins for non-local environments.
- [x] Validate production CORS configuration and fail closed for unknown origins.
- [x] Add CSRF protection for cookie-authenticated mutating requests.
- [x] Set explicit secure cookie attributes for production, including `Secure`, `HttpOnly`, and an intentional `SameSite` policy.
- [x] Add session revocation behavior for logout, compromised sessions, and account changes.
- [x] Add Discord OAuth token refresh or a clear forced-reauthentication flow.
- [x] Handle revoked Discord OAuth grants without returning internal errors.
- [x] Add rate limits for OAuth, member reads, template writes, case creation, retries, and evidence capture.
- [x] Add HTTP request-body limits appropriate for JSON and evidence metadata.
- [x] Add HTTP read-header, read, write, and idle timeouts.
- [x] Add standard security headers where appropriate for the API.
- [x] Log authentication failures and permission denials with trace IDs without logging tokens or cookies.
- [x] Redact OAuth tokens, session IDs, cookies, and secrets from logs and error payloads.
- [x] Add API idempotency for case creation and other externally retried writes.
- [x] Add pagination limits and defensive query bounds to every list endpoint.
- [x] Add malformed JSON, oversized request, expired session, revoked token, and cross-guild access tests.

## Appeals

- [x] Replace placeholder appeal records with the final case-linked appeal model.
- [x] Add guild-configurable appeal questions and Quack's default appeal form.
- [x] Validate appeal question ordering, required fields, and supported simple input types.
- [x] Snapshot the appeal form used when a member submits an appeal.
- [x] Snapshot case appealability so later template edits do not change existing cases.
- [x] Enforce at most one appeal record per case with a database uniqueness constraint.
- [x] Allow only the target Discord identity to create and read a member-facing appeal.
- [x] Allow banned and departed members to appeal after Discord authentication.
- [x] Reject appeals for non-appealable, voided, or unrelated cases with clear errors.
- [x] Add appeal submission, staff response, request-more-information, reopen, accept, reject, and close behavior.
- [x] Reopen the existing appeal for more information instead of creating another appeal.
- [x] Require `Moderate Members` or higher for appeal review.
- [x] Record member and staff appeal events as an immutable timeline.
- [x] Hide staff identities from the member-facing appeal history while retaining them in audit data.
- [x] Make accepting an appeal atomically void the case.
- [x] Keep rejected or closed appeals from changing case validity.
- [x] Offer timeout removal or unban as a separate staff-confirmed operation after acceptance.
- [x] Never silently reverse Discord enforcement when an appeal is accepted.
- [x] Notify the member when staff request information or decide the appeal.
- [x] Add Discord notification entry links for eligible appeals.
- [x] Add staff appeal queue and filtering backend behavior.
- [x] Audit appeal reads, submissions, status changes, decisions, and reversal requests.
- [x] Add concurrency tests for duplicate submissions and simultaneous appeal decisions.
- [x] Add end-to-end tests for accepted, rejected, reopened, closed, and failed-reversal appeals.

## Audit Log and Staff Statistics

- [x] Define the complete set of audit action names and metadata contracts.
- [x] Record the correct source for dashboard, Discord, system, import, and honeypot activity.
- [x] Audit every meaningful successful write.
- [x] Audit every failed or denied sensitive write.
- [x] Audit permission-sensitive case, history, appeal, template, settings, and module reads.
- [x] Audit action attempts, retries, dismissals, reversals, and terminal outcomes.
- [x] Audit template archive, restore, import, and export.
- [x] Audit member appeal activity and staff decisions.
- [x] Audit optional-module configuration and automated honeypot cases.
- [x] Enforce append-only audit behavior in application and storage interfaces.
- [x] Prevent normal repository methods from updating or deleting audit rows.
- [x] Redact secrets, tokens, private transport payloads, and unnecessary personal data from audit metadata.
- [x] Allow every moderator to read the complete guild audit log.
- [x] Add audit filters for actor, source, action, resource, result, case, member, and date.
- [x] Add stable audit pagination and ordering.
- [x] Add audit-mirror delivery to a configured staff-only Discord channel.
- [x] Keep audit-mirror delivery failures visible without blocking the original moderation operation.
- [x] Add repair behavior when the audit-mirror channel is removed or becomes inaccessible.
- [x] Derive staff case, action, appeal, and outcome statistics from existing records.
- [x] Add time-range, template, action, and result breakdowns without creating leaderboards.
- [x] Add backend statistics responses for authorized staff use.
- [x] Keep statistics guild-scoped and prevent cross-guild aggregation of member history.
- [x] Add tests for audit immutability, redaction, permissions, filtering, mirroring, and statistics calculations.

## Optional Ticket Module

- [x] Write a module-specific product definition for ticket settings, lifecycle, permissions, transcript retention, and Discord behavior.
- [x] Add independent per-guild ticket-module enablement and settings.
- [x] Add ticket entry-channel and private-thread configuration.
- [x] Create private Discord ticket threads or channels with validated staff/member permissions.
- [x] Add ticket open, resolve, cancel, and authorized reopen behavior defined by the module specification.
- [x] Keep tickets separate from cases, actions, appeals, escalation, and member moderation history.
- [x] Add ticket event timelines without reusing case-note behavior.
- [x] Capture ticket transcripts when a ticket closes.
- [x] Add authorized transcript read or export behavior.
- [x] Add Discord ticket creation, queue, view, reply, and close components.
- [x] Add backend APIs for ticket settings, status, queue, detail, and transcript access.
- [x] Audit ticket settings and lifecycle changes.
- [x] Handle missing or deleted ticket channels and parent channels.
- [x] Add ticket rate limits and duplicate-open protection.
- [x] Add module-specific v4 ticket migration with dry-run and idempotency support.
- [x] Add permission, privacy, lifecycle, transcript, Discord, and migration tests.

## Optional General Logging Module

- [x] Write a module-specific product definition for logged events, channel routing, privacy, formatting, and retention boundaries.
- [x] Add independent per-guild general-logging enablement and settings.
- [x] Configure staff-only Discord destinations for message, member, moderation, and server events.
- [x] Validate logging-channel permissions and repair deleted channel references.
- [x] Add a bounded in-memory message cache for edit and delete context.
- [x] Make cache limits configurable and safe for large guilds.
- [x] Log configured message edits with before/after content when available.
- [x] Log configured message deletions and bulk deletions when cached context is available.
- [x] Log configured attachment and embed metadata without permanently archiving file contents.
- [x] Log configured member join and leave events.
- [x] Log configured Discord ban and unban events separately from Quack's audit log.
- [x] Log configured guild or channel changes selected by the module definition.
- [x] Keep general logging output in Discord instead of building a searchable permanent event archive.
- [x] Keep general logging failures and retries separate from case action execution.
- [x] Add Discord rate-limit handling and bounded retries for log delivery.
- [x] Redact tokens, webhook secrets, and content excluded by module privacy settings.
- [x] Add module-specific v4 logging-settings migration with dry-run and idempotency support.
- [x] Add cache, event, formatting, permission, retry, privacy, and migration tests.

## Optional Honeypot Module

- [x] Write a module-specific product definition for honeypot setup, trigger rules, exemptions, and safety behavior.
- [x] Add independent per-guild honeypot enablement and settings.
- [x] Allow admins to select a honeypot channel and one active case template.
- [x] Validate that the selected template remains active and compatible with automated use.
- [x] Handle message events in configured honeypot channels.
- [x] Ignore Quack, other bots, and exempt staff according to the module definition.
- [x] Prevent recursive triggers from Quack responses or logging output.
- [x] Apply the configured template through the normal case creation transaction.
- [x] Mark honeypot cases with an explicit automated source.
- [x] Use the normal escalation, action, notification, evidence, permission, and audit records.
- [x] Define how actor permissions are represented for system-created honeypot cases without granting a fake staff identity.
- [x] Wire the production honeypot case adapter through a system-only core boundary that cannot fabricate or reuse a staff identity.
- [x] Register honeypot routes, migration, gateway projections, drift forwarding, conditional intents, and graceful runtime shutdown centrally.
- [x] Handle selected-template archive and honeypot-channel deletion safely.
- [x] Add honeypot trigger counts and outcomes to derived module statistics.
- [x] Add backend APIs for honeypot settings and status.
- [x] Audit honeypot configuration, triggers, failures, and created cases.
- [x] Add module-specific v4 honeypot migration with dry-run and idempotency support.
- [x] Add setup, exemption, loop-prevention, action, failure, and migration tests.

## V4 Migration and Coexistence

- [x] Define the final v4 historical-case import format.
- [x] Import v4 cases as clearly labeled historical records.
- [x] Preserve useful v4 case identity, guild, target, moderator, reason, type, context link, and creation time.
- [x] Prevent imported v4 cases from contributing to v5 template escalation.
- [x] Prevent imported records from creating v5 action executions or member notifications.
- [x] Keep imported records readable in authorized staff and member history views.
- [x] Decide how legacy moderator identities are displayed when the user is no longer available.
- [x] Add dry-run import reports before any data is written.
- [x] Add import validation with per-record warnings and failures.
- [x] Make imports idempotent and safely repeatable.
- [x] Record import batches, source identifiers, counts, failures, and checksums.
- [x] Audit every import batch and material import failure.
- [x] Keep v4 and v5 database schemas and Redis keys isolated during coexistence.
- [x] Add command-scope checks so v4 and v5 do not register conflicting slash commands.
- [x] Document the transition that removes v4 direct moderation commands after migration.
- [x] Add module-specific migration hooks without making core migration depend on unfinished modules.
- [x] Add migration fixtures covering warnings, timeouts, kicks, bans, departed members, missing users, and malformed legacy rows.
- [x] Add migration rollback and rerun tests.
- [x] Add a real-data dry-run checklist that does not expose member data in logs.

## Database and Storage Reliability

- [x] Replace production reliance on startup-only `AutoMigrate` with versioned, reviewable database migrations.
- [x] Remove startup `AutoMigrate` instead of retaining a separate local-development schema path.
- [x] Add forward and rollback migration procedures for every v5 model realignment.
- [x] Test the migration foundation against the current pre-ledger v5 schema and representative stored data.
- [x] Preserve table names, IDs, case numbers, snapshots, action attempts, events, and audit history through the migration foundation.
- [x] Add database constraints for one default level per template where feasible.
- [x] Add database constraints for zero or one enforcement action per level where feasible.
- [x] Add database constraints for one appeal per case.
- [x] Add uniqueness and index coverage for template identity/version, guild case numbers, member history, audit filters, action claims, evidence, and module settings.
- [x] Add mapper tests for every new or changed domain/storage record.
- [x] Add MySQL integration tests for JSON fields, indexes, foreign keys, locks, and transaction rollbacks.
- [x] Add integration tests proving concurrent case creation keeps unique numbers and correct escalation after the model changes.
- [x] Add integration tests proving void and appeal acceptance cannot race with escalation counts.
- [x] Add claim-lease recovery for action executions left running after crashes.
- [x] Add safe cleanup or archival policies for expired OAuth state and sessions.
- [x] Define backup and restore procedures for MySQL.
- [x] Define the required Redis durability and recovery behavior for sessions and command cache.
- [x] Test backup restoration into a clean environment.
- [x] Verify that restoring storage cannot duplicate action execution or case numbering.

## Queue, Concurrency, and Recovery

- [x] Add a bounded lease or heartbeat to database action claims.
- [x] Recover stale running claims after process termination.
- [x] Prevent stale workers from completing work after another worker reclaims the action.
- [x] Add explicit failed-action review state without turning terminal failures into automatic retries.
- [x] Define when dismissed failures leave the active review queue.
- [x] Add safe replay tooling for operators without bypassing permission and idempotency checks.
- [x] Keep persisted actions as the durable source of truth after notification and action model changes.
- [x] Verify queue polling remains bounded under large pending backlogs.
- [x] Add fairness tests across guilds and cases so one busy guild cannot starve others.
- [x] Add duplicate-submit tests for Discord, HTTP, startup recovery, and poller overlap.
- [x] Add crash tests between Discord success and database completion.
- [x] Add crash tests between notification delivery and database completion.
- [x] Add race tests for queue start, submit, poll, stop, retry, reclaim, dismiss, and void interactions.
- [x] Add operational counters for stale claims, reclaims, safe retries, manual retries, dismissals, and permanent failures.

## Operations, Security, and Deployment

- [x] Separate liveness from readiness checks.
- [x] Include database, Redis, Discord, queue, migration, and action-capability readiness.
- [x] Report guild-scoped degraded status when required Discord permissions or managed channels are unavailable without taking healthy guilds offline.
- [x] Add metrics for case creation, escalation levels, action attempts, failures, retries, notifications, appeals, audit mirroring, and optional modules.
- [x] Add alerting guidance for queue backlog, stale running actions, repeated Discord failures, database failures, and migration failures.
- [x] Add structured logs for authentication, authorization, case, action, appeal, evidence, migration, and module workflows.
- [x] Keep request and correlation IDs on all new HTTP, Discord, queue, audit, and module paths.
- [x] Add log redaction tests for tokens, cookies, session IDs, webhook URLs, member content, and action payloads.
- [x] Validate all required production configuration at startup with actionable errors.
- [x] Add configuration for CORS, HTTP timeouts, rate limits, managed channels, notification branding, and module toggles.
- [x] Document production Discord application settings, OAuth redirects, intents, and bot permissions.
- [x] Document production MySQL and Redis sizing, persistence, backup, and recovery expectations.
- [x] Document container/deployment resource limits and graceful termination guidance; applying release-infrastructure limits remains explicitly unauthorized.
- [x] Verify bounded shutdown behavior for action, HTTP, audit/appeal, evidence, and optional-module lifecycles with isolated adapters; production deployment shutdown remains an external rehearsal.
- [x] DEFERRED — add `govulncheck` to CI only after the user authorizes release-infrastructure changes; the exact proposed job is documented in `docs/release-infrastructure-proposal-v5.md`.
- [x] DEFERRED — add repository secret scanning only after the user authorizes release-infrastructure/settings changes; local credential-pattern checks remain a QP-I release gate.
- [x] Review HTTP and Discord error messages to prevent internal details or personal data leaks.
- [x] Add an operator runbook for failed migrations, Discord outages, Redis outages, MySQL outages, queue backlog, and stuck actions.
- [x] Add an operator runbook for manually retrying, dismissing, or voiding failed moderation work.
- [x] Add a production rollback procedure that preserves cases and prevents duplicate action execution.

## Testing and Release Readiness

- [x] Update core unit tests to the final no-window, no-severity, no-weight, archive-only, one-action model.
- [x] Add table-driven tests for every permission and hierarchy rule, including later retry and reversal operations when those surfaces exist.
- [x] Add template validation tests for context fields, archive/restore, import/export, timeout duration, ban deletion, and retry count.
- [x] Add case tests for context, evidence, target validation, voiding, replacement, member visibility, and action-independent validity.
- [x] Add action tests for every Discord result, retry classification, idempotency boundary, manual control, and reversal.
- [x] Add notification tests proving one message at most and accurate outcome rendering.
- [x] Add appeal tests for ownership, one-per-case, reopen, decisions, voiding, and reversals.
- [x] Add audit tests for completeness, denied operations, read events, redaction, immutability, and all-moderator access.
- [x] Add backend API contract tests for every staff, admin, member, and module endpoint.
- [x] Add full Discord interaction tests for slash commands, message commands, autocomplete, components, modals, deferred edits, and public/private responses.
- [x] Add Redis integration tests for OAuth state, sessions, command cache, expiry, unavailable Redis behavior, and restart-durable interaction dedupe.
- [x] Add MySQL integration coverage for schema migrations, locks, JSON, constraints, concurrency, v4 import, and restore verification.
- [x] Add optional-module unit, integration, Discord, privacy, and migration tests.
- [x] Add end-to-end tests for template creation, case creation, escalation, evidence, enforcement, notification, audit, member read, appeal, and voiding.
- [x] Add end-to-end tests for safe retry, unsafe manual retry, dismissal, reversal, and action crash recovery.
- [x] DEFERRED — execute Docker Compose smoke only after the user authorizes Compose/deployment-infrastructure changes; the exact smoke contract is documented in `docs/release-infrastructure-proposal-v5.md`.
- [x] Add a real test-guild checklist covering install, starter policy, permissions, hierarchy, evidence channel, case creation, each action, member DM, member case access, appeal, audit mirror, and recovery.
- [x] Run targeted `go test -race` coverage for the queue, action claims, case creation, appeal decisions, evidence capture, HTTP policies, Discord dedupe, and modules.
- [x] DEFERRED — add `go vet ./apps/backend/...` to CI only after the user authorizes release-infrastructure changes; QP-I still runs vet as a local required gate.
- [x] DEFERRED — add race, integration, and migration CI service jobs only after the user authorizes release-infrastructure changes; exact jobs are documented in `docs/release-infrastructure-proposal-v5.md`.
- [x] Keep `go test ./apps/backend/...`, `go vet ./apps/backend/...`, and builds for all four operator/application commands as required release gates in `apps/backend/scripts/v5-readiness.sh`.
- [x] DEFERRED — enforce a CI coverage threshold only after the infrastructure owner selects the non-regression policy and the user authorizes release-infrastructure changes.
- [x] Add fuzz tests for Discord message links, custom IDs, imported template/config JSON, structured context, and legacy import rows.
- [x] DEFERRED — change Compose/container Go pinning only after the user authorizes Dockerfile/Compose release-infrastructure changes; the repository module pins Go 1.25.4 and the exact proposal is documented.
- [x] Update architecture, module, API, configuration, testing, migration, operations, storage-recovery, coexistence, and release documentation as implementation changes land.
- [x] Complete a final scope-drift audit against every rule in `v5.md`; the result is recorded in `docs/v5-readiness.md`.
- [x] Complete a security review of authentication, authorization, evidence exposure, audit data, member privacy, and operational redaction.
- [x] Complete an isolated v4/v5 coexistence, repeat-import, command-scope, and guarded rollback rehearsal; a real Discord cutover remains external.
- [x] DEFERRED / NOT EXECUTED — complete a clean-install rehearsal in a new Discord guild after the user authorizes use of a non-production Discord guild/application. This remains required release evidence because `v5.md` requires exact join/bootstrap and Discord-authoritative behavior; no readiness exception has been accepted.
- [x] Complete a clean-schema and current-v5 upgrade rehearsal with existing cases, pending actions, final constraints, and restore-manifest verification.
- [x] Produce the final backend release checklist and record its results in `docs/v5-readiness.md`; the local strict gate passes and the unauthorized real-guild evidence is recorded as deferred / not executed, not as a pass.
