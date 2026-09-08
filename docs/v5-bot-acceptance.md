# v5 bot acceptance evidence

This matrix maps all 76 answers in [the product interview](v5-product-interview.md) and the named findings in [the product review](v5-product-review.md) to inspected backend production paths and regression evidence. It is an evidence artifact, not a declaration that v5 is ready to replace v4. Dashboard UI work is deferred; backend configuration and shared API correctness are not deferred with it.

**P means production wiring and relevant regression evidence were located. P does not mean live Discord acceptance.** U means acceptance remains unverified, I means an implementation mismatch or unfinished work, and D means a scope decision or deferred dashboard work. Tests named below are evidence pointers; this document does not claim every test has just been rerun. No real moderation or migration outcome is inferred from a test name or an older tracker.

## Work in progress and acceptance gates

- Live ticket acceptance passed for member open, duplicate-open protection, staff join/reply, member close, new ticket after closure, and admin queue close. Both closed tickets retained queue transcripts and released the member slot. See the implementation ledger; deleted-message retention remains implementation work.
- Native history totals/import labels and bounded Unicode pages are committed in `b8be3c8`; command/view regression packages pass. Live profile acceptance remains open.
- Honeypot interrupted primary-incident recovery and legacy bot exemptions are committed in `bad4bb3`; race and MySQL-enabled backend tests pass. Live acceptance remains open.
- Canonical module enablement is committed in `02d6616`, including live configuration validation and atomic updates. Full backend tests pass; live acceptance remains open.
- Explicit application URL configuration is committed in `4827cae`; focused tests pass. Live configuration validation remains open. See the configuration contract below.
- Bot-message logging parity still requires resolution against Q63–65.

Before release, record live results for template creation → case creation → evidence inspection, denied/failed action → audit retry, void/reversal, appeal acceptance/rejection, ticket open/close/transcript, honeypot and logging. Also rehearse an actual v4 export/import and assess realistic guild/member load. Passing unit tests does not close these gates.

## Production and test evidence map

All paths below are backend paths; the production runtime registers native commands, gateway handlers, workers and dependencies in [runtime.go](../apps/backend/internal/runtime/runtime.go), [command registry](../apps/backend/internal/discordbot/commands/registry.go) and [module runtime](../apps/backend/internal/moduleintegration/runtime.go).

| Key | Production evidence | Regression evidence |
| --- | --- | --- |
| C | [Case creation](../apps/backend/internal/quack/cases.go), [escalation](../apps/backend/internal/quack/case_escalation.go), [native definition](../apps/backend/internal/discordbot/commands/case_definition.go), [picker](../apps/backend/internal/discordbot/commands/template_picker.go) | `TestCaseAddActsImmediatelyWithOptionalContext`, `TestUserContextCreatesCaseForSelectedMember`, `TestTemplatePickerReachesEveryTemplateAndPreservesTarget`, `TestCaseDecayChangesFutureCountsWithoutErasingHistory`; command and quack test packages |
| T | [Native template creation](../apps/backend/internal/discordbot/commands/template_create.go), [management](../apps/backend/internal/discordbot/commands/template_manage.go), [levels](../apps/backend/internal/discordbot/commands/template_level.go), [starter policy](../apps/backend/internal/store/guild_settings.go) | `TestTemplateCreateModalActivatesSelectedPolicy`, `TestNativeTemplateThresholdMatchesCreatedCase`, `TestNativeTemplateDecayCanBeEnabledAndDisabled`, `TestGuildBootstrapCreatesExactStarterAndIsIdempotent` |
| E | [Evidence capture](../apps/backend/internal/quack/evidence.go), [uploads](../apps/backend/internal/quack/evidence_upload.go), [Discord copies/channels](../apps/backend/internal/discordbot/evidence.go), [context update](../apps/backend/internal/quack/case_context_update.go), [staff views](../apps/backend/internal/discordbot/ui/views/case_moderator.go) | `TestStaffUploadsPreserveFilesBeforeCreationAndWithoutRepeatingActions`, `TestCaseProceedsWithoutContextOrWorkingEvidence`, `TestContextMessageLinkCaptureIsOptionalAndIdempotent`, `TestCreatedEvidenceChannelAllowsCurrentStaffOnly`, `TestEvidenceNavigationRechecksAuthority` |
| A | [Recovery controls](../apps/backend/internal/quack/action_recovery.go), [native controls](../apps/backend/internal/discordbot/commands/case_staff.go), [void reversals](../apps/backend/internal/store/case_reversals.go), [live authorization](../apps/backend/internal/quack/authorization.go) | `TestCasePreflightMatrixAndNoPartialCommit`, `TestRetryUnbanRefreshesPermissionsForDepartedMember`, `TestRetryCannotResurrectVoidedPunishment`, `TestVoidQueuesReversalAcrossCompletionRace` |
| N | [Member notifications](../apps/backend/internal/quack/case_notifications.go), [public receipt worker](../apps/backend/internal/discordbot/case_publications.go) | `TestNotificationUsesRecordedExpiryAndHonestOutcomes`, `TestNotificationNeverIncludesStaffContext`, `TestCasePublicationReconcilesTerminalAndVoid`, `TestCasePublicationRetriesOutagesAndRetiresUnknownMessage` |
| P | [Appeal submission](../apps/backend/internal/discordbot/appeal_submission.go), [decisions](../apps/backend/internal/discordbot/appeal_decisions.go), [form contract](../apps/backend/internal/quack/appeal_settings.go), [outbox](../apps/backend/internal/quack/appeal_notifications.go), [native queue](../apps/backend/internal/discordbot/commands/appeals.go) | `TestAppealDMFormOwnershipAndSingleSubmission`, `TestAppealQueueDecisionChecksLivePermissions`, `TestAppealDecisionsAreTerminal`, `TestAppealQueueRefreshEditsOrRecreatesOnlyMissingMessages`, `TestAppealsCommandFindsUndeliveredSubmissions` |
| K | [Ticket setup](../apps/backend/internal/moduleintegration/ticket_setup.go), [adapter lifecycle](../apps/backend/internal/modules/tickets/discord.go), [controls](../apps/backend/internal/moduleintegration/ticket_components.go), [transcript capture](../apps/backend/internal/moduleintegration/discord_clients.go), [HTTP lifecycle](../apps/backend/internal/modules/tickets/routes.go) | `TestOwnerCanCloseExistingTicketAfterModuleDisabled`, `TestTicketDeletionWaitsForTranscriptPublication`, `TestConcurrentCloseReusesCapturedTranscript`, `TestEveryTicketCloseRoutePreservesDiscordTranscript`, `TestTicketTranscriptKeepsStableOrderAndAttachmentContext` |
| H | [Honeypot setup](../apps/backend/internal/moduleintegration/honeypot_setup.go), [gateway projection](../apps/backend/internal/moduleintegration/honeypot.go), [incident service](../apps/backend/internal/modules/honeypot/service.go) | `TestHoneypotTemplateIsEditableAndReused`, `TestMemberBurstCreatesOneIncident`, `TestBurstCleanupRetainsOneCaseAndEveryMessage`, `TestObsoleteRoleExemptionsDoNotBypassTrap`; primary recovery is committed; live recovery acceptance remains open |
| L | [Logging setup](../apps/backend/internal/moduleintegration/logging_setup.go), [delivery](../apps/backend/internal/modules/generallogging/service.go), [gateway events](../apps/backend/internal/moduleintegration/gateway_guilds.go) | `TestNativeSetupPersistsOneChannelAndDeliversMessageDetails`, `TestExternalBanLoggingSeparatesActorAndTarget`, `TestPrivacyRedactionRetryAndAuditIsolation`, `TestDeliveryQueueSkipsUnconfiguredEventsButReportsFailures` |
| U | [Audit persistence](../apps/backend/internal/store/audit.go), [audit mirror](../apps/backend/internal/quack/audit_mirror.go), [case enrichment](../apps/backend/internal/quack/audit_mirror_case.go), [audit rendering](../apps/backend/internal/discordbot/ui/views/audit.go) | `TestAuditRejectsServiceEvents`, `TestAuditServiceRedactsAndFiltersCompleteContract`, `TestCaseCreatedAuditPreservesSelectedOutcome`, `TestStaffStatisticsAreGuildScopedDerivedAndUnranked` |
| M | [Current schema](../apps/backend/internal/store/schema_init.go), [adoption](../apps/backend/internal/store/schema_adopt.go), [historical import CLI](../apps/backend/cmd/quack-v4-import/main.go), [importer](../apps/backend/internal/v4import/import.go) | `TestInitializeCurrentSchema`, `TestAdoptCurrentSchemaPreservesHistory`, `TestV4ImportDryRunIdempotencyIsolationCollisionAndRollback`, `TestEscalationExcludesImportedV4HistoryAcrossTemplateVersions` |

## All interview answers

| Questions | Requirement or decision | Status and evidence |
| --- | --- | --- |
| 1, 4, 8 | Bot first; cases/evidence, appeals, tickets, honeypot, logging, mirror | P/U: all native surfaces are composed; complete live acceptance outstanding. Dashboard UI is D. C/E/P/K/H/L/U. |
| 2 | Public bot at roughly 850 guilds, including large communities | U: bounded workers and concurrency tests do not establish acceptable production scale. |
| 3, 18–20 | Slash/message/user entry points; immediate execution; no required staff channel | P/U: common immediate creation and paginated picker, C. Live interaction timing/visibility still needs acceptance. |
| 5–7 | Light personality; distinct staff/member details; plain-text style | P/U: shared conversation/icon views and redacted member projection, C/N. Subjective polish needs user walkthrough. |
| 9–11 | Template chooses punishment; same-rule escalation; opt-in decay; future-only changes | P: C/T/A, including void and imported-history exclusions and immutable snapshots. |
| 12 | Seed easily editable defaults | P: central starter policy and idempotent lifecycle bootstrap, T. |
| 13–15 | Easy native authoring, example escalations, edits immediately active | P/U: create/edit/level/remove/archive/restore implemented, T. Ease of use not proven by unit tests. |
| 16 | Policy for departed members/historical no-action cases | D/U: answer leaves product policy unresolved; existing authorization behavior is not a new agreed requirement. |
| 17 | Discord-derived authority; Moderate Members baseline | P: live permission refresh and actor/action checks, A. |
| 21 | Result includes selected outcome, errors, notification/appeal information | P/U: C/N, durable refresh wired; delayed/restarted live demonstration outstanding. |
| 22 | Evidence, user history and reasoned void controls | P/I: evidence/void are covered; native profile totals and imported labels are implemented, C/E/A. |
| 23 | Context/evidence updates without new punishment; audit actor | P: E/U; context link capture and visible failure tests added. |
| 24 | Recover forms only if simple; blank reopening acceptable | D/P: no durable draft system required. Immediate creation plus independent context form removes old mandatory draft dependency. |
| 25 | Staff resolve their own semantic duplicate incidents | P: independent cases remain possible; request replay protection is distinct, C. |
| 26–29, 31–32 | Optional uploads or selected message; no witness context; preserve available data and flag failures | P/U: E. Live ban plus screenshot preservation still needs rehearsal. |
| 30 | Preferred preservation receipt | D: no concrete preference supplied; current warnings and detail views provide observable outcomes. |
| 33 | Members see template reason, not staff context/evidence | P: N/E; private staff evidence navigation rechecks authorization. |
| 34–36 | Discord evidence storage, retained admin edits, reopenable copies | P/U: E. Existing channel ACLs are preserved; newly created channels grant ordinary staff read access. Existing live ACLs and long-term saved-copy access still require acceptance. |
| 37–40 | Permission block; failure queue/retry; automatic reversal on void | P/U: A. Real Discord denial, failure, retry and reversal demonstrations remain outstanding. |
| 41–42 | Notifications default on; concise outcome/reason; hidden staff identity; failed DM recorded | P/U: T/N. Actual blocked-DM/ban behavior needs rehearsal. |
| 43 | No member self-history Discord command | D/P: staff history is gated; dashboard self-history deferred. |
| 44 | Pagination and web-equivalent link | P/I: native case/evidence/appeal paging exists; consistent web links and explicit application URL integration are unfinished. Dashboard UI remains D. |
| 45–47 | DM appeal form; one case, one statement, once; no conversation/deadline | P/D: P evidence. Lost-DM website journey is deferred dashboard work. |
| 48–52 | Actionable appeal queue; terminal accept/reject; void/reversal; hidden identity and optional rejoin link | P/U: P/A. End-to-end decision/rejoin rehearsal outstanding. |
| 53–59 | Private thread, natural chat, only open/close, owner/staff close, transcript before deletion, one open ticket | P/U: K. Root live acceptance active. Transcript captures available history at closure, unlike v4's continuously cached text; deleted-message behavior needs acceptance. |
| 60–63 | Trap setup/warning/counter; editable template; staff exemption; one incident and cleanup/recovery | P/I/U: H. Primary incident recovery active. Current all-bot exemption exceeds Q61 and legacy's Quack/staff exclusion. |
| 64–65 | Single general-log channel, near-v4 detail, omit Quack's own bans | P/U: L. v5 skips bot-message cache entries whereas legacy cached all; parity needs explicit resolution. |
| 66–69 | Meaningful audit only; separate case/action entries; actor/member/rule/level/time | P: U. Storage allowlist and separate delivery state; selected outcome comes from immutable snapshot. |
| 70 | Statistics derived from real moderation activity | P/I: derived backend evidence U; native user/profile surface implemented. |
| 71 | Disposable prerelease schema; import actual v4 history; translate settings where practical | P/I/U: M. Real-data extraction/import and distributed v4 settings translation are not demonstrated by JSONL fixtures or standalone helper importers. |
| 72 | Clean cutover except historical cases | D/P/U: scope-check/import tools exist, M; production cutover not performed. |
| 73 | Cohesive purpose-built architecture | I: broad repository exposure, worker composition ownership and presentation/persistence coupling remain; not a completed maintainability rewrite. |
| 74 | Single binary, MySQL/Redis | P: runtime composition establishes this shape; distributed operation is not an acceptance requirement. |
| 75–76 | Named test guild/accounts and extensive journeys before replacing v4 | U: targets/authorization are not outcome evidence. Record actual rehearsals separately below. |

## Original review findings

| Finding | Current disposition |
| --- | --- |
| 1 Audit noise | P: storage allowlist rejects service events; mirror delivery state is separate, U evidence. |
| 2 Disconnected module switches | I: core settings accepts/saves shadow booleans while runtime modules read the registry. Backend correction in progress, not deferred UI. |
| 3 Dashboard always submits forbidden evidence field | D: existing dashboard form remains broken; do not claim it functional. Native evidence journey is separate. |
| 4 Missing new web appeal form | D: web UI deferred; native DM form is implemented. |
| 5 Missing native appeals | P/U: production handlers/outbox/queue wired; live acceptance outstanding. |
| 6 CORS-derived application URL | P/U: explicit backend configuration implemented in this slice, awaiting root integration; native DM modal already avoids depending on a website URL. |
| 7 Unpublished ticket buttons | P: native setup and persistent entry receipt now publish them. |
| 8 Ticket lifecycle/control mismatch | P/U: shared close adapter, owner authority and removed reopen route; live acceptance active. |
| 9 Competing conversation/transcript models | P/U: native chat is canonical; available-history capture replaces continuous cache. Deleted text and attachment longevity need explicit acceptance. |
| 10 Honeypot restart requirement | P/U: gateway subscriptions remain stable across live setup; privileged intent availability still needs live verification. |
| 11 Honeypot opacity/recovery | I: primary recovery active; bot-author exemption mismatch remains. Setup/counter/template editing are implemented. |
| 12 No direct evidence uploads | P: slash attachment options and add-evidence use case. |
| 13 Missing evidence feedback/inspection | P: captured text, warnings, file results and private pages. |
| 14 Evidence lifecycle rough edges | P/U: stable message links and admin-preserving ACL lifecycle. Precommit upload failures can leave orphan copies; long-term live access unverified. |
| 15 Different case entry flows | P: immediate common creation, paginated picker, no required context/JSON input. |
| 16 Fragile mandatory drafts | P: superseded by accepted simple flow; Q24 does not require persistent drafts. |
| 17 Public deferred errors | P/U: safer private acknowledgement/public publication paths with regression tests; live visibility acceptance remains necessary. |
| 18 Stale results after restart | P/U: durable public receipt worker wired into runtime; restart/outage tests exist, live rehearsal outstanding. |
| 19 Expected events logged as failures | P: logging queue distinguishes disabled/unrouted events; old logs do not establish present health. |
| 20 Architecture/documentation mismatch | I: more real callers and explicit services, but repository exposure, ownership and stale documentation remain. This matrix supersedes broad completion claims, not the user's requirements. |

## Application URL configuration

[Configuration parsing](../apps/backend/internal/config/config.go) and [URL validation](../apps/backend/internal/config/application_url.go) implement optional `APPLICATION_BASE_URL`, independent of `API_CORS_ALLOWED_ORIGINS`. Unset means no website link destination. A configured value must be an absolute HTTPS base URL without credentials, query or fragment; a path prefix is supported and trailing slashes are normalized. Native Discord appeal submission must continue to work without this value. `TestApplicationBaseURLIsOptionalExplicitAndNormalized`, `TestApplicationBaseURLRejectsMalformedConfiguration` and `TestApplicationLinksNeverInferCORSOrigin` cover this contract. This is source/test evidence, not live deployment evidence.

## Live acceptance record

No new live acceptance result is claimed by this audit. Root's current ticket rehearsal and the other authorized scenarios must record actual outcome, test identity, relevant configuration, failures and follow-up. Do not replace U with P solely because the implementation tracker or unit suite is green.
