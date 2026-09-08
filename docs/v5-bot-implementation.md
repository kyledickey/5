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
