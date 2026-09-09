# First walkthrough revisions

The user's September 8 feedback supersedes earlier presentation choices where
they conflict. Dashboard work remains deferred.

## Implemented changes

- Public rule/setup commands and case/history commands; one public case result.
  Slash creation edits the original response. Context selection publishes once
  and removes the selector. Failed selector cleanup removes its controls, while
  its message identity prevents another moderation decision from that selector.
- Private errors, including unexpected asynchronous failures, without erasing a
  confirmed successful result. No ephemeral DM flags. Sensitive staff evidence,
  context, appeal statements, and recovery browsers retain their private audience.
- Short rule views and a `/help` command with rules, cases, appeals, and setup
  topics. Command references use actual IDs from startup sync, with no per-message
  Discord lookup. Renamed context menus replace only their exact known old names.
- Case lists show rule names. Voided cases lead with status. History uses localized
  timestamps in subtext. Completed timeouts show Discord's recorded expiry.
  Public case details exclude staff notes, evidence, and raw delivery diagnostics.
- Colored appeal controls; decisions update the source with accepted/rejected,
  reviewer, and reason. Member decision DMs identify the case and server. New
  durable intents preserve those labels; old intents retain their existing fallback.
- Markdown ticket entry, primary Open and red Close. No ordinary View/Join or
  timeline; staff Recovery remains for actual delivery/permission problems.
  Closure DMs include the transcript. Durable receipts prevent duplicate confirmed
  sends; ambiguous delivery is reconciled, while blocked DMs do not prevent closure.
  The transcript remains saved before deletion.
- New channels get topics and an introduction, unless their feature already sends
  a welcome panel. Supplied/reused channels are untouched. Introduction failure
  logs a warning and retains the channel identity rather than creating duplicates.
- Compact audit header plus adjacent `-#` metadata, explicit Quack settings,
  and direct action wording. Generated honeypot warnings name actual configured
  punishments; administrator-written warnings remain intact.

## Verification and running build

Focused regressions cover response count, audience separation, unexpected errors,
DM flags, command IDs and rename cleanup, timeout receipts, channel introductions,
appeal decisions, and ticket transcript delivery/retries. The MySQL-enabled full
backend suite passed. Clean source `3f54f18` was loaded into
`/tmp/quack-v5-walkthrough-ux`; all readiness checks passed.

A new live visual walkthrough was not completed: Helium reported concurrent user
changes, so computer control was left with the user. Prior live journey evidence
is not a visual acceptance of this new presentation. Existing messages are not
mass-rewritten. Re-run `/setup tickets` to refresh its saved entry panel; new
commands and refreshed case receipts use the new presentation.
