# Quack v5 product interview

These questions follow the [implementation review](v5-product-review.md). They are unanswered decisions, not requirements. Short answers, examples, and “recommend something” are all useful. We should resolve contradictions and walk through sample conversations before rewriting the code or updating `v5.md` as the agreed specification.

## First: audience, scope, and feel

1. When you say the dashboard is later, must admins configure everything and members appeal entirely inside Discord, or should a minimal setup/appeal website remain available while the full dashboard waits?
2. Is Quack primarily for your own servers, a few known communities, or a public bot that strangers should set up without your help? What sizes of communities and staff teams should the first release handle comfortably?
3. Describe the ideal spam incident from noticing a message to being done. What does the moderator click/type, and what should they see at each step?
4. Which v4 interactions felt good enough to preserve? Which specific v5 interactions or messages annoyed you most beyond the audit screenshot?
5. Should Quack feel like a terse moderation tool, a calm helpful assistant, or something with light duck personality? Where is personality inappropriate?
6. Which names should users actually see: template, rule, infraction, case, warning, level, outcome? In particular, what should a case with no punishment be called?
7. Do you want the current custom icons/plain-text direction preserved, a return to embeds, or a mix? What should determine which style is used?
8. What is the smallest feature set that makes you comfortable replacing v4? Rank cases/templates, evidence, appeals, tickets, honeypot, general logging, and utilities by launch priority.

## Moderation rules and administrator setup

9. Keep the rule that moderators choose a template and Quack chooses the outcome, with no punishment, reason, or level overrides? Are there any exceptional actions administrators must be able to perform?
10. Keep escalation all-time and confined to the same rule? For example, should a third spam case after two years still trigger the third-case outcome?
11. What should happen once the highest level is reached, and when an old case is voided after newer cases already exist? Should only future decisions change?
12. Should a new guild immediately get the current starter policy—two warnings, 24-hour timeout on cases three/four, ban from five—or require an admin to review/activate a policy first?
13. How should an admin build a rule: a short guided flow, several configuration commands, importing a file, or an existing minimal web editor? Which settings deserve an advanced section?
14. What real templates would you configure for your own server? Give two or three with their reasons, thresholds, outcomes, evidence requirements, and notification/appeal choices.
15. Are template edits immediately active? Do admins need preview/simulation, duplication, or a way to compare versions before saving?
16. Should staff be able to issue a historical/no-action case for someone who has left, or manually ban a departed scammer? How should that fit the template model?
17. Is Discord's current permission model enough? For tickets/appeals, may helpers participate without moderation powers, and may every moderator void or review every case?

## Creating and managing cases

18. Preferred entry points: message context action, user context action, `/case add`, or another command name? Which should be fastest?
19. Should creating a case always show a preview, only confirm kick/ban or escalated outcomes, or act immediately? What information must be visible before confirmation?
20. Should case commands work in any channel, only designated staff channels, or anywhere with private replies? Where should the permanent moderation result appear?
21. What belongs on the compact result: member, rule, case number, outcome/duration, moderator, DM status, evidence status? Which details should require opening the case?
22. Which controls should be on a case: View evidence, View history, Void, Replace, Retry, Undo timeout/ban, Review appeal? Which controls need an additional reason or confirmation?
23. Should moderators be able to fix context typos or add evidence after creation without voiding the moderation decision? If so, which changes must remain visible in history?
24. When a form fails or times out, should Quack reopen it with answers intact, save a resumable draft, or start over? How long should an unfinished case remain resumable?
25. When multiple moderators work on the same incident, should Quack warn about a recent similar case, prevent duplicates, or simply use the latest history and show the resulting escalation?

## Evidence and visibility

26. What does “upload evidence” mean to you: capture the selected Discord message, paste several links, attach screenshots/files directly, record off-server evidence, or all of these?
27. Should evidence be optional globally, required for particular templates, or selectable per incident? Can an urgent case proceed before evidence is attached?
28. Should a case include other people's messages for conversation context or witness reports, even when they did not author the offending message?
29. Should selecting a message capture just that message, several surrounding messages, or a moderator-selected collection? How should edited/deleted messages appear?
30. What would convince you that preservation succeeded: a thumbnail and captured text, a saved-file count, a preview of the exact record, or another explicit receipt?
31. If a file cannot be copied, should Quack stop, ask to continue with a warning, or proceed and flag the missing file? Does the answer differ for a ban versus a warning?
32. If the source message is already gone, what must the moderator provide to proceed? Is a written explanation mandatory, and should manual evidence be marked differently?
33. Should affected members see every context answer and piece of evidence? Do you need confidential reporter identity, staff-only evidence, private notes, or redaction with a recorded reason?
34. Should `quack-evidence` be hidden bot storage or a channel moderators can browse? Should admins choose its category/name, and what should happen if they delete it?
35. Which file types/sizes matter, and for how long must evidence remain usable? Must archived evidence survive deletion of its Discord copy or removal of the bot?
36. Should staff be able to download an evidence bundle/transcript for a case, or is an in-Discord detail view enough for launch?

## Actions, errors, and member messages

37. If the selected punishment requires a permission the moderator lacks, block the whole case, ask a senior moderator to approve it, or handle it another way?
38. If enforcement fails after the case is created, should the case still count toward escalation? Where should staff be alerted, and who is responsible for resolving it?
39. If Discord's response is uncertain, what should staff see before retrying? Should Quack first check whether the ban/timeout already happened where possible?
40. Does voiding a case automatically remove an active timeout/ban, offer an explicit reversal, or leave punishment unchanged? Is appeal acceptance different?
41. Should every case send a DM unless a template disables it? What should happen when DMs are blocked or a ban prevents delivery?
42. What must a member notification say? Give your preferred wording for a warning, a 24-hour timeout, and a ban; should the moderator's identity be shown?
43. Do members need a Discord command or DM menu to view their own history even if they never received the original notification?
44. For long records, prefer pagination, expandable sections/buttons, an attached text file, or a web link? The current implementation falls back to `message.txt`.

## Appeals

45. Should a member submit an appeal with a DM button/modal, a bot DM conversation, a separate appeal server, or a minimal web page? How should a banned member find it after losing the original DM?
46. Is an appeal always about one case, or do you also need general ban/server-access appeals? Which outcomes should be appealable by default?
47. Do you want one short “Why should this be reviewed?” answer or configurable questions? Are deadlines, cooldowns, or repeated submissions needed?
48. Should staff review an appeal in a single updated message, a private thread, or a queue command? Do you need claiming/assignment so two moderators do not review it simultaneously?
49. Can the moderator who issued the case decide its appeal? Do some decisions require a second person or administrator?
50. Should requesting more information start a conversation, reopen a form, or accept one additional reply? Which replies should notify staff/members?
51. What exactly should Accept, Reject, and Close mean? Should accepting offer “void only” and “void + remove punishment” as distinct explicit choices?
52. What should members receive after each decision, and should staff identities remain hidden? Is an invite/rejoin link useful after unbanning?

## Tickets

53. What kinds of support belong in tickets? Do you need categories and opening questions, or one generic support conversation?
54. Should opening a ticket create a private thread or a private channel? Should members just type normally, and what should the first message contain?
55. Who should see and answer tickets: all moderators, configured support roles, assigned staff, or different staff by category?
56. Should members close/cancel their own tickets? Can they reopen them? What should happen to the Discord conversation after closure and after reopening?
57. Which workflow controls matter: claim, transfer, add participant, close reason, inactivity reminder, auto-close, priority? Which should be omitted to keep tickets simple?
58. Who gets the transcript, what content/files must it contain, how long should it remain, and should the member receive a copy automatically?
59. What limits prevent abuse without frustrating normal support—one open ticket, daily limits, cooldowns? Should failed setup attempts consume a member's allowance?

## Honeypots and general Discord logging

60. Should honeypot setup create a trap channel and warning message like v4, or use an existing channel? Do you want a live caught-user counter and configurable warning text?
61. Should any human message trigger the selected template, or do some message types/roles bypass it? Exactly which staff permissions should exempt someone?
62. Should repeated trap messages from one member produce one incident/case or several escalating cases? Should Quack capture evidence and then delete every trigger message?
63. Do you want a test mode that reports what would happen without punishing anyone, including when testing as an admin? How should disabled/broken trap configuration be reported and repaired?
64. Which general Discord events need logging, and should they go to one channel or different channels? Should edits/deletions include full content and attachments?
65. If Quack itself bans someone, should that appear in both general Discord logs and Quack's moderation log, or be represented once when destinations overlap?

## Audit and operations

66. Beyond case created/voided and user timeout/kick/ban/unban, which changes belong in audit: template edits, module/settings changes, appeal decisions, ticket open/close, evidence added, failed punishment, DM failure?
67. Should one case with a successful ban produce two entries (case created + member banned) or one combined moderation entry? How should retry/reversal outcomes be grouped?
68. What fields should an audit entry show by default: actor, affected member, case number, rule, reason, outcome, timestamp? What belongs only in expanded details?
69. For existing noisy audit records, should normal views hide technical entries while retaining them separately? Do any operator diagnostics need a developer-only UI, or are structured process logs enough?
70. Which staff statistics would you actually use? Should reads/page visits have no influence on activity statistics, and do you want summaries in Discord before the dashboard exists?

## Migration, maintainability, and acceptance

71. Which current v5 records are disposable test data, and which must survive the rewrite? Which v4 cases, appeals, tickets, settings, and honeypot history must migrate?
72. Must old case numbers, old DM buttons, and existing ticket panels keep working? Is a clean cutover acceptable, or will v4 and v5 overlap?
73. Which code feels hardest to follow? Do you prefer cohesive feature packages, flatter files, fewer interfaces, or specific conventions? Give an example if possible; implementation choices can otherwise be proposed from the review.
74. Is a single Go process with MySQL/Redis still the desired operational shape? How many running bot instances should the first release support?
75. Which test guild, bot application, and test accounts should later real Discord acceptance use? What timeout/kick/ban/evidence/ticket actions may that rehearsal perform?
76. What concrete demonstration would make you say “this is ready”? Name the journeys you want to walk through personally before replacing v4.

## Decisions already given

- Preserve the main template-based v5 idea while making it smooth to use.
- Honeypot, appeals, tickets, and evidence need working user journeys.
- User-facing audit history should contain meaningful moderation events, not services firing.
- The dashboard is a later step once the bot is ready.
- Review first, then ask detailed questions, then implement the agreed rewrite.
- Preserve existing changes in conventional, logical commits before new work; keep future commits bounded by coherent changes.
- The `quack` tmux session may be used to inspect and work with the running processes.

After answers, revise the product definition, write example interactions and concrete acceptance scenarios, and implement in logical commits. Unanswered questions are not permission to silently change established policy.
