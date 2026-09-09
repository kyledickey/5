# v5 bot acceptance evidence

This matrix maps all 76 answers in [the product interview](v5-product-interview.md) and the named findings in [the product review](v5-product-review.md) to inspected backend production paths and regression evidence. It is an evidence artifact, not a declaration that v5 is ready to replace v4. Dashboard UI work is deferred; backend configuration and shared API correctness are not deferred with it.

**P means production wiring and relevant regression evidence were located. P does not mean live Discord acceptance.** U means acceptance remains unverified, I means an implementation mismatch or unfinished work, and D means a scope decision or deferred dashboard work. Tests named below are evidence pointers; this document does not claim every test has just been rerun. No real moderation or migration outcome is inferred from a test name or an older tracker.

## Current acceptance status

The authorized beta runs `/tmp/quack-v5-native-toggles` at `ee8de61`;
readiness and the full MySQL-enabled backend suite passed. The dashboard remains
stopped and its UI remains deferred. The latest evidence wording has rendering
test coverage; earlier live journeys used the builds recorded in the
[implementation ledger](v5-bot-implementation.md).

- **Native authoring and entry points:** the administrator created a rule, added
  a second-case ban, configured seven-day decay, and verified the native policy
  view. Member-context creation produced warning #15; slash creation selected
  the next threshold and banned in case #16. Voiding reversed that ban, the
  tester rejoined, and the synthetic rule was archived (`28d80d0` evidence).
  Elapsed decay timing remains regression evidence.
- **Cases, evidence, and privacy:** direct PNG upload was preserved before case
  #10's ban. Case #14 exposed a converted-image copy failure; after its fix,
  recapture preserved text and an image opened after source deletion. A limited
  moderator opened that saved image with an explicit channel read grant; removing
  the temporary grants restored denial. Member access and revoked-role controls
  were denied privately. Native history totals matched SQL.
- **Actions, appeals, and receipts:** ban DM, appeal submission, administrator
  acceptance, unban, and native invite rejoin passed; rejection and one-appeal
  enforcement also passed. A blocked DM left warning #13 valid, recorded failed
  delivery, and updated private feedback without exposing it publicly. Timeout
  reversal, newer-timeout protection, and retry after cleanup passed. An old
  public receipt refreshed after restart beyond its interaction-token lifetime.
- **Tickets:** open, duplicate-open protection, staff participation, member/admin
  close, transcript retention, and member-slot release passed. Deleted text
  survived restart in the transcript. Missing queue-post repair and old private
  receipt refresh passed. Injected lost-receipt adoption, stale-confirmation
  rejection, restart, and owner closure retained one transcript on the original
  queue post; this was a controlled fixture, not an actual interrupted send.
- **Honeypot and logs:** default setup, editable policy, staff exemption,
  enforcement, evidence-before-cleanup, counter, warning deletion repair, and a
  two-message/one-case burst passed. General logs retained member edit/delete
  text and attribution, including bot warning edits. A live attachment-only edit
  recorded old/new filenames; bulk deletion retained both synthetic messages'
  individual IDs, text, and corresponding files (`adaa072` evidence). Case #16
  produced the expected member-leave and public-result edit logs with no separate
  Discord ban log; semantic moderation audit remained present (`28d80d0`).
  General-log file links are not permanent archived copies.

- **Native module toggles:** disabling tickets blocked the existing entry panel
  privately without changing configuration or creating a ticket. Re-enabling
  restored that same panel without restart; owner closure retained one transcript
  and deleted the thread. The detailed evidence appears below.

The tracked representative **bot workflows now have live acceptance evidence**;
no further concrete representative workflow blocker is identified here. This
includes native authoring, context/slash entry, module toggles, and representative
attachment/bulk/own-ban logging. The user walkthrough still determines whether
the bot feels ready; the rollout gates and additional coverage below remain.

**Before replacing v4**, rehearse an authorized real backup and assess realistic
mixed guild/member load. Disposable historical import and the 85,000-event idle
poll improvement from 342 ms to 3.04 ms are useful evidence, not production
migration or capacity acceptance. Dashboard work and configured website
destinations remain separate from native bot readiness.

Additional failure combinations—such as blocked-DM bans, interrupted honeypot
execution, actual uncertain-send timing, and longer-term attachment access—remain
unverified where noted below. Prioritize these by risk; they do not each imply a
missing feature or require exhaustive live permutations to finish the bot.

## Production and test evidence map

All paths below are backend paths; the production runtime registers native commands, gateway handlers, workers and dependencies in [runtime.go](../apps/backend/internal/runtime/runtime.go), [command registry](../apps/backend/internal/discordbot/commands/registry.go) and [module runtime](../apps/backend/internal/moduleintegration/runtime.go). Core appeal/audit worker ownership is in [core_workers.go](../apps/backend/internal/runtime/core_workers.go); optional-module composition no longer owns those workers or core API/component registration.

| Key | Production evidence | Regression evidence |
| --- | --- | --- |
| C | [Case creation](../apps/backend/internal/quack/cases.go), [escalation](../apps/backend/internal/quack/case_escalation.go), [native definition](../apps/backend/internal/discordbot/commands/case_definition.go), [picker](../apps/backend/internal/discordbot/commands/template_picker.go) | `TestCaseAddActsImmediatelyWithOptionalContext`, `TestUserContextCreatesCaseForSelectedMember`, `TestTemplatePickerReachesEveryTemplateAndPreservesTarget`, `TestCaseDecayChangesFutureCountsWithoutErasingHistory`; command and quack test packages |
| T | [Native template creation](../apps/backend/internal/discordbot/commands/template_create.go), [management](../apps/backend/internal/discordbot/commands/template_manage.go), [levels](../apps/backend/internal/discordbot/commands/template_level.go), [starter policy](../apps/backend/internal/store/guild_settings.go) | `TestTemplateCreateModalActivatesSelectedPolicy`, `TestNativeTemplateThresholdMatchesCreatedCase`, `TestNativeTemplateDecayCanBeEnabledAndDisabled`, `TestGuildBootstrapCreatesExactStarterAndIsIdempotent` |
| E | [Evidence capture](../apps/backend/internal/quack/evidence.go), [uploads](../apps/backend/internal/quack/evidence_upload.go), [Discord copies/channels](../apps/backend/internal/discordbot/evidence.go), [context update](../apps/backend/internal/quack/case_context_update.go), [staff views](../apps/backend/internal/discordbot/ui/views/case_moderator.go) | `TestStaffUploadsPreserveFilesBeforeCreationAndWithoutRepeatingActions`, `TestCaseProceedsWithoutContextOrWorkingEvidence`, `TestContextMessageLinkCaptureIsOptionalAndIdempotent`, `TestCreatedEvidenceChannelAllowsCurrentStaffOnly`, `TestEvidenceNavigationRechecksAuthority` |
| A | [Recovery controls](../apps/backend/internal/quack/action_recovery.go), [native controls](../apps/backend/internal/discordbot/commands/case_staff.go), [void reversals](../apps/backend/internal/store/case_reversals.go), [live authorization](../apps/backend/internal/quack/authorization.go) | `TestCasePreflightMatrixAndNoPartialCommit`, `TestRetryUnbanRefreshesPermissionsForDepartedMember`, `TestRetryCannotResurrectVoidedPunishment`, `TestVoidQueuesReversalAcrossCompletionRace` |
| N | [Member notifications](../apps/backend/internal/quack/case_notifications.go), [public receipt worker](../apps/backend/internal/discordbot/case_publications.go) | `TestNotificationUsesRecordedExpiryAndHonestOutcomes`, `TestNotificationNeverIncludesStaffContext`, `TestCasePublicationReconcilesTerminalAndVoid`, `TestCasePublicationRetriesOutagesAndRetiresUnknownMessage` |
| P | [Appeal submission](../apps/backend/internal/discordbot/appeal_submission.go), [decisions](../apps/backend/internal/discordbot/appeal_decisions.go), [form contract](../apps/backend/internal/quack/appeal_settings.go), [outbox](../apps/backend/internal/quack/appeal_notifications.go), [native queue](../apps/backend/internal/discordbot/commands/appeals.go) | `TestAppealDMFormOwnershipAndSingleSubmission`, `TestAppealQueueDecisionChecksLivePermissions`, `TestAppealDecisionsAreTerminal`, `TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages`, `TestAppealsCommandFindsUndeliveredSubmissions` |
| K | [Ticket setup](../apps/backend/internal/moduleintegration/ticket_setup.go), [adapter lifecycle](../apps/backend/internal/modules/tickets/discord.go), [controls](../apps/backend/internal/moduleintegration/ticket_components.go), [transcript capture](../apps/backend/internal/moduleintegration/discord_clients.go), [original-message journal](../apps/backend/internal/modules/tickets/message_journal.go), [HTTP lifecycle](../apps/backend/internal/modules/tickets/routes.go) | `TestOwnerCanCloseExistingTicketAfterModuleDisabled`, `TestTicketDeletionWaitsForTranscriptPublication`, `TestConcurrentCloseReusesCapturedTranscript`, `TestEveryTicketCloseRoutePreservesDiscordTranscript`, `TestTicketTranscriptKeepsStableOrderAndAttachmentContext`, `TestJournalPreservesDeletedOriginalTextAcrossRestart`, `TestJournalFailureBlocksDeletionAndRetryFlushesOriginal`, `TestJournalCloseWaitsForReceivedWrite` |
| H | [Honeypot setup](../apps/backend/internal/moduleintegration/honeypot_setup.go), [gateway projection](../apps/backend/internal/moduleintegration/honeypot.go), [incident service](../apps/backend/internal/modules/honeypot/service.go) | `TestHoneypotTemplateIsEditableAndReused`, `TestMemberBurstCreatesOneIncident`, `TestBurstCleanupRetainsOneCaseAndEveryMessage`, `TestObsoleteRoleExemptionsDoNotBypassTrap`; primary recovery is committed; live recovery acceptance remains open |
| L | [Logging setup](../apps/backend/internal/moduleintegration/logging_setup.go), [delivery](../apps/backend/internal/modules/generallogging/service.go), [gateway events](../apps/backend/internal/moduleintegration/gateway_guilds.go) | `TestNativeSetupPersistsOneChannelAndDeliversMessageDetails`, `TestExternalBanLoggingSeparatesActorAndTarget`, `TestPrivacyRedactionRetryAndAuditIsolation`, `TestDeliveryQueueSkipsUnconfiguredEventsButReportsFailures`, `TestBulkDeleteKeepsPerMessageAttributionAndPrivacy`; gateway bot-message lifecycle regression |
| U | [Audit persistence](../apps/backend/internal/store/audit.go), [audit mirror](../apps/backend/internal/quack/audit_mirror.go), [case enrichment](../apps/backend/internal/quack/audit_mirror_case.go), [audit rendering](../apps/backend/internal/discordbot/ui/views/audit.go) | `TestAuditRejectsServiceEvents`, `TestAuditServiceRedactsAndFiltersCompleteContract`, `TestCaseCreatedAuditPreservesSelectedOutcome`, `TestStaffStatisticsAreGuildScopedDerivedAndUnranked` |
| M | [Current schema](../apps/backend/internal/store/schema_init.go), [adoption](../apps/backend/internal/store/schema_adopt.go), [historical import CLI](../apps/backend/cmd/quack-v4-import/main.go), [importer](../apps/backend/internal/v4import/import.go), [SQL exporter](../apps/backend/internal/v4import/export.go), [operator procedure](v4-historical-import.md) | `TestInitializeCurrentSchema`, `TestAdoptCurrentSchemaPreservesHistory`, `TestV4ImportDryRunIdempotencyIsolationCollisionAndRollback`, `TestEscalationExcludesImportedV4HistoryAcrossTemplateVersions`, `TestMySQLV4SQLExportImportRehearsal`, `TestExportPagesPreserveEverySourceIdentity` |

## All interview answers

| Questions | Requirement or decision | Status and evidence |
| --- | --- | --- |
| 1, 4, 8 | Bot first; cases/evidence, appeals, tickets, honeypot, logging, mirror | P/U: all native surfaces are composed; complete live acceptance outstanding. Dashboard UI is D. C/E/P/K/H/L/U. |
| 2 | Public bot at roughly 850 guilds, including large communities | U: bounded workers and concurrency tests do not establish acceptable production scale. |
| 3, 18–20 | Slash/message/user entry points; immediate execution; no required staff channel | P/U: common immediate creation and paginated picker, C. Native member-context creation produced warning #15 and slash creation selected the next threshold in #16; message-context capture also passed in #14. Representative entry flows have live evidence; subjective usability remains for the user walkthrough. |
| 5–7 | Light personality; distinct staff/member details; plain-text style | P/U: shared conversation/icon views and redacted member projection, C/N. Subjective polish needs user walkthrough. |
| 9–11 | Template chooses punishment; same-rule escalation; opt-in decay; future-only changes | P: C/T/A, including void and imported-history exclusions and immutable snapshots. |
| 12 | Seed easily editable defaults | P: central starter policy and idempotent lifecycle bootstrap, T. |
| 13–15 | Easy native authoring, example escalations, edits immediately active | P/U: create/edit/level/remove/archive/restore implemented, T. Live native creation, second-case ban level, seven-day decay configuration, policy inspection, threshold execution, and archival passed. Elapsed decay timing remains regression evidence; final usability judgment belongs to the user. |
| 16 | Policy for departed members/historical no-action cases | D/U: answer leaves product policy unresolved; existing authorization behavior is not a new agreed requirement. |
| 17 | Discord-derived authority; Moderate Members baseline | P: live permission refresh and actor/action checks, A. |
| 21 | Result includes selected outcome, errors, notification/appeal information | P/U: private moderator receipt includes selected level, actions/errors, DM state and appeal eligibility; bounded refresh follows action and DM completion independently. Live warning receipts updated to DM sent. Public notices exclude these staff fields; tester visibility passed. Blocked-DM warning feedback also passed; long-delayed delivery and restart-limited private refresh remain separate checks. |
| 22 | Evidence, user history and reasoned void controls | P/U: evidence/void are covered; native profile totals and imported labels are implemented, C/E/A. |
| 23 | Context/evidence updates without new punishment; audit actor | P: E/U; context link capture and visible failure tests added. |
| 24 | Recover forms only if simple; blank reopening acceptable | D/P: no durable draft system required. Immediate creation plus independent context form removes old mandatory draft dependency. |
| 25 | Staff resolve their own semantic duplicate incidents | P: independent cases remain possible; request replay protection is distinct, C. |
| 26–29, 31–32 | Optional uploads or selected message; no witness context; preserve available data and flag failures | P/U: E. Case #10 directly uploaded a synthetic PNG before its ban. Message-context case #14 exposed and correctly reported a converted-image copy failure; `0ce2511` fixes the metadata size mismatch. Retrying capture then deleting the source preserved both text and a saved PNG opened by the administrator. A limited moderator subsequently opened that saved image through private evidence paging with explicit channel read access; temporary grants were removed afterward. |
| 30 | Preferred preservation receipt | D: no concrete preference supplied; current warnings and detail views provide observable outcomes. |
| 33 | Members see template reason, not staff context/evidence | P: N/E; private staff evidence navigation rechecks authorization. |
| 34–36 | Discord evidence storage, retained admin edits, reopenable copies | P/U: E. Existing channel ACLs are preserved; newly created channels grant ordinary staff read access. Live limited-moderator access passed using an administrator-approved temporary read grant, and access disappeared when the grant was removed. Source-deletion survival is verified; retention over a longer period remains distinct. |
| 37–40 | Permission block; failure queue/retry; automatic reversal on void | P/U: A. Member evidence denial, limited-moderator private evidence metadata access, denial from the same old control after role removal, early timeout removal, and preservation of a newer manual timeout passed live. Retrying that failed reversal after manual cleanup preserved its failed attempt and succeeded with confirmed absence; public audit source remained intact and feedback was private. Ban ownership and permission-loss rehearsals remain open. |
| 41–42 | Notifications default on; concise outcome/reason; hidden staff identity; failed DM recorded | P/U: T/N. Case #10 delivered the native DM after a successful ban. Blocking beta on monkey produced a failed DM for warning #13, kept the case valid, updated the private receipt, and omitted DM status publicly. SQL retained attempted content with one failed attempt and no delivery ID; unblocking did not resend. Account settings were restored. A blocked-DM ban remains a separate enforcement combination. |
| 43 | No member self-history Discord command | D/P: staff history is gated; dashboard self-history deferred. |
| 44 | Pagination and web-equivalent link | P/U: native paging and configured case/history/evidence web links are implemented (`1c3fa74`); command regression tests pass. Live configured-link acceptance remains open. Dashboard UI remains D. |
| 45–47 | DM appeal form; one case, one statement, once; no conversation/deadline | P/D: P evidence. Lost-DM website journey is deferred dashboard work. |
| 48–52 | Actionable appeal queue; terminal accept/reject; void/reversal; hidden identity and optional rejoin link | P/U: P/A. Native acceptance and rejection, member DMs, one-appeal enforcement and acceptance voiding passed live. Case #10 additionally passed first-attempt ban reversal, accepted DM Rejoin Server navigation, and native invite acceptance back into the guild. |
| 53–59 | Private thread, natural chat, only open/close, owner/staff close, transcript before deletion, one open ticket | P/U: K. Member/staff close and retained transcript passed live. Original received text now survives edits/deletions through a persisted journal merged with final history; deleted-message/restart live acceptance passed: the published queue transcript retained the tester text deleted before a clean beta restart. See the bounded retention guarantee below. |
| 60–63 | Trap setup/warning/counter; editable template; staff exemption; one incident and cleanup/recovery | P/U: H. Live default setup, editable timeout, staff exemption, enforcement/evidence/counter passed. Durable warning refresh and startup reconciliation are implemented; deleting the beta's configured warning recreated it with the same four-incident count and no new case. A live two-message burst created only case #11, incremented the counter once, and completed both cleanups after saving the incident; interrupted enforcement recovery remains open. |
| 64–65 | Single general-log channel, near-v4 detail, omit Quack's own bans | P/U: L. Live member edit/delete, bot counter edits, attachment-only replacement, and bulk deletion passed. The bulk log retained individual IDs, text, and each message's files. Case #16 produced member-leave and public-result edit logs without a separate Discord ban log, while semantic moderation audit remained present. |
| 66–69 | Meaningful audit only; separate case/action entries; actor/member/rule/level/time | P: U. Storage allowlist and separate delivery state; selected outcome comes from immutable snapshot. |
| 70 | Statistics derived from real moderation activity | P/U: native history showed five tester cases, one valid and four voided, matching live SQL counts. Imported-history labels and broader statistics remain source/test evidence; Q70 does not require a separate statistics subsystem. |
| 71 | Disposable prerelease schema; import actual v4 history; translate settings where practical | P/U/D: M. Read-only SQL export, all six v4 types, bounded paging and historical-only import passed disposable MySQL rehearsal. Real-backup rehearsal remains open. Native module resetup is the documented cutover path, allowed by Q71; automatic settings translation is not a release requirement. |
| 72 | Clean cutover except historical cases | D/P/U: scope-check/import tools exist, M; production cutover not performed. |
| 73 | Cohesive purpose-built architecture | P/I: core worker/API/component ownership and case/appeal notification adapter ownership are corrected. Native case details use bounded event reads and omit unused attempts. Broad repository exposure remains, but the latest review found no authorization bypass and does not recommend indiscriminate wrappers. |
| 74 | Single binary, MySQL/Redis | P: runtime composition establishes this shape; distributed operation is not an acceptance requirement. |
| 75–76 | Named test guild/accounts and extensive journeys before replacing v4 | U: targets/authorization are not outcome evidence. Record actual rehearsals separately below. |

## Original review findings

| Finding | Current disposition |
| --- | --- |
| 1 Audit noise | P: storage allowlist rejects service events; mirror delivery state is separate, U evidence. |
| 2 Disconnected module switches | P/U: `02d6616` uses canonical registry reads and atomic explicit toggles, preserving configuration and rejecting invalid enablement before core/audit writes. Native/API parity and conflict tests pass. On `ee8de61`, native ticket disable/enable preserved configuration, denied the existing panel while disabled, and restored opening without restart; owner closure retained one transcript. |
| 3 Dashboard always submits forbidden evidence field | D: existing dashboard form remains broken; do not claim it functional. Native evidence journey is separate. |
| 4 Missing new web appeal form | D: web UI deferred; native DM form is implemented. |
| 5 Missing native appeals | P/U: native submission, private queue, accept/reject, member DM and repeat-appeal rejection passed live. |
| 6 CORS-derived application URL | P/U: explicit backend configuration committed in `4827cae`; native DM modal already avoids depending on a website URL. |
| 7 Unpublished ticket buttons | P: native setup and persistent entry receipt now publish them. |
| 8 Ticket lifecycle/control mismatch | P/U: shared close adapter, owner authority and removed reopen route; member/staff closure, transcript retrieval, deleted-message/restart retention, and missing-post repair passed live. Injected lost-receipt adoption, stale-confirmation rejection, restart, and owner closure retained one transcript on the original queue post. Actual interrupted-send timing and failed-deletion recovery remain separate checks. |
| 9 Competing conversation/transcript models | P/U: native chat is canonical; committed original-message journaling supplements final history and blocks deletion on failed capture. Deleted text/restart passed live; attachment longevity remains open. |
| 10 Honeypot restart requirement | P/U: live setup and template edits took effect without restart; the tester message triggered the configured timeout and evidence capture. |
| 11 Honeypot opacity/recovery | P/U: interrupted primary recovery and legacy bot/staff exemptions are committed. Setup/counter/template editing are implemented; live default setup, editable timeout, administrator exemption, member enforcement, evidence, cleanup, counter and two-message burst passed; interrupted recovery remains open. |
| 12 No direct evidence uploads | P/U: slash attachment options and add-evidence use case; administrator synthetic-file upload/copy/view and saved image access after source deletion passed live. A limited moderator opened the saved image with an explicit channel read grant; removing temporary grants restored denial. |
| 13 Missing evidence feedback/inspection | P: captured text, warnings, file results and private pages. |
| 14 Evidence lifecycle rough edges | P/U: stable message links and admin-preserving ACL lifecycle. Precommit upload failures can leave orphan copies; long-term live access unverified. |
| 15 Different case entry flows | P: immediate common creation, paginated picker, no required context/JSON input. |
| 16 Fragile mandatory drafts | P: superseded by accepted simple flow; Q24 does not require persistent drafts. |
| 17 Public deferred errors | P/U: case creation and void/reversal acknowledge privately before live checks; public success notices publish separately through standalone channel sends. Retry from a public audit entry passed live with private feedback and source preservation. The tester saw the new void notice without a broken reply reference; other failure paths remain separate live checks. |
| 18 Stale results after restart | P/U: durable receipt refresh is driven by source mutations in `9933511`; old receipt #2 updated to voided after restart and became idle. Outage/race tests pass; delayed action/reversal live coverage remains separate. |
| 19 Expected events logged as failures | P: logging queue distinguishes disabled/unrouted events; old logs do not establish present health. |
| 20 Architecture/documentation mismatch | P/I: core worker and registration ownership is corrected; combined repository exposure and core/presentation coupling remain. This matrix supersedes broad completion claims, not the user's requirements. |

## Application URL configuration

[Configuration parsing](../apps/backend/internal/config/config.go) and [URL validation](../apps/backend/internal/config/application_url.go) implement optional `APPLICATION_BASE_URL`, independent of `API_CORS_ALLOWED_ORIGINS`. Unset means no website link destination. A configured value must be an absolute HTTPS base URL without credentials, query or fragment; a path prefix is supported and trailing slashes are normalized. Native Discord appeal submission must continue to work without this value. `TestApplicationBaseURLIsOptionalExplicitAndNormalized`, `TestApplicationBaseURLRejectsMalformedConfiguration` and `TestApplicationLinksNeverInferCORSOrigin` cover this contract. This is source/test evidence, not live deployment evidence.

## Live acceptance record

The [implementation ledger](v5-bot-implementation.md) records the builds, account actions, SQL checks, and limitations behind the current summary and matrix. Later entries include source-deletion survival, restricted-moderator file opening, blocked-DM feedback, and ticket receipt adoption. Read historical pending statements in their dated context. Do not replace U with P solely because a tracker or unit suite is green.

### Native ticket disable/enable rehearsal

- Clean beta `ee8de61` at `/tmp/quack-v5-native-toggles` passed all readiness
  checks; the full MySQL-enabled suite passed in
  `/tmp/quack-native-module-toggle-final.log`.
- Administrator `/setup tickets enabled:false` returned private success. Monkey's
  existing Open ticket control returned the private Tickets are not enabled
  response. SQL retained six tickets and configuration MD5
  `81cc5415305eb780b6b486595bef0e86`, with enabled set to 0.
- `/setup tickets enabled:true` returned success with the same configuration
  hash and enabled set to 1. Without a restart, monkey used the same entry panel
  to open ticket `01M21XGY0N4NAW6WS6NTEQQ8GY`, thread
  `1547060630970835044`, and queue post `1547060641196417185`.
- Monkey sent the synthetic text “Synthetic module toggle rehearsal: the existing
  support panel works again after tickets are re-enabled.” Owner closure resolved
  the ticket in SQL at 19:47:29.251. Read-only Discord inspection returned `10003`
  for the deleted thread and found one ticket-ID `.txt` transcript on the original
  beta-authored queue post. Fetching that synthetic transcript confirmed the
  marker text was preserved; the latest 100 queue posts contained one matching
  file. This verifies the representative native toggle and restored ticket
  lifecycle without resetting module configuration.

## Configuration checks and implementation boundaries

1. **Q44 configured-link acceptance:** native case/history/evidence web buttons are committed and tested; live configuration/link inspection remains open.
2. **Q73 / review 20 boundaries:** `quack.Services.Store` still exposes the combined repository. Case publication commands use narrow receipt-registration and action-status use cases (`3dcf02b`); case and appeal notification rendering belongs to the Discord adapter. New appeal decisions persist versioned facts including the decision-time reason and rejoin URL; legacy rows retain their saved-body fallback. Staff statistics are composed in the core service set and require only derived reads; honeypot compatibility checks use the template service rather than integration-owned repository rules. Native command startup now receives interaction deduplication and a narrow command-hash capability explicitly from process composition; it no longer reaches through `Services.Store` for these dependencies. Native evidence pages now fetch one snapshot and its attachments (`e658418`), with long-text subpages and fresh authorization; SQL count/offset work remains. Broader HTTP infrastructure access is not itself evidence of an authorization bypass.
3. **Ticket closure feedback:** `5848e7c` acknowledges saved progress before deleting the source thread and avoids the impossible final edit. Member closure on the updated beta resolved the ticket, retained its transcript and avoided the old post-deletion interaction error. The brief progress message was not captured visually. `40533c3` subsequently passed live verification of in-place View refresh on an old private entry receipt: closed state and the transcript replaced the deleted-thread mention and obsolete controls. Receipts still require user-triggered refresh; no perpetual refresh infrastructure is added.

Known limits are kept separate from new feature scope: Discord public-send/receipt-storage and evidence-upload/storage are not atomic; a crash can leave an untracked public receipt or orphan copy. Concurrent context submissions lack a durable evidence reservation. The ticket journal retains received messages admitted before final capture; delayed deleted events first delivered afterward and buffered writes lost during a database outage plus hard crash are outside its guarantee. These are documented recovery boundaries, not claims of exactly-once Discord effects.

Ticket queue recovery now supports validated adoption or explicit administrator
nondelivery confirmation, with durable attempt IDs protecting newer sends from
stale decisions and late transport results. Live synthetic lost-receipt adoption,
stale-confirmation rejection, restart, and owner closure retained one transcript
on the original queue post. This used controlled local fixture injection; actual
interrupted-send timing and successful nondelivery replacement remain unverified
live. See the implementation ledger's receipt-adoption rehearsal.

Remaining representative native acceptance and future v4 rollout gates are separated in the current status above. Dashboard review findings 3–4 remain deferred; module resetup is allowed rather than a missing mandatory migration subsystem. Unverified failure combinations and documented recovery limits do not independently establish unfinished implementation.
