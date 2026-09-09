# Quack v5 product interview

These questions follow the [implementation review](v5-product-review.md). They are unanswered decisions, not requirements. Short answers, examples, and “recommend something” are all useful. We should resolve contradictions and walk through sample conversations before rewriting the code or updating `v5.md` as the agreed specification.

## First: audience, scope, and feel

1. When you say the dashboard is later, must admins configure everything and members appeal entirely inside Discord, or should a minimal setup/appeal website remain available while the full dashboard waits?

> No i just meant that we will code the dashboard later soon. after the bot.

2. Is Quack primarily for your own servers, a few known communities, or a public bot that strangers should set up without your help? What sizes of communities and staff teams should the first release handle comfortably?

> its a public bot. v4 is currently in ~850 servers ~200,000 members. the main server that its used in the most has 80k members

3. Describe the ideal spam incident from noticing a message to being done. What does the moderator click/type, and what should they see at each step?

> for some incident, they can type /case add where they specify the template for whatever rule is being broken, it can be created without context then the user can click a button to add context such as a message link or some shit. the user could also do the flow where they left click a message and go apps>quack>add case where it would grab that message and use it as the context. thats all they need to do the bot handles enforcement the mod just tells the bot "hey this guy is breaking this rule"

4. Which v4 interactions felt good enough to preserve? Which specific v5 interactions or messages annoyed you most beyond the audit screenshot?

> v4 and v5 are 2 VERY different things. the features im porting over to v5 are the private ticket system in a thread where the user just clicks a button and it creates a ticket for them, the general logging system where we log deleted messages, edits, joins, leaves, etc, the honeypot system here when its setup it will create a honeypot template which just bans the bot then applies that template whenever someone falls for the honeypot, the admin can also edit that template if they dont like the default autoban behavior.

5. Should Quack feel like a terse moderation tool, a calm helpful assistant, or something with light duck personality? Where is personality inappropriate?

> I wouldnt mind it having a bit of personality. honestly eventually this may pivot to an automated ai moderation platform so personality is good.

6. Which names should users actually see: template, rule, infraction, case, warning, level, outcome? In particular, what should a case with no punishment be called?

> for members who break a rule, they should just see the template name, the default reason/what that template means, case #, and the action the bot took. they dont need to know the level name or any of that shit. mods should see primarily template name, custom context/evidence added, level executed, actions, case #. a case with no punishment is a warning.

7. Do you want the current custom icons/plain-text direction preserved, a return to embeds, or a mix? What should determine which style is used?

> preserved i like the look it makes it feel more personal and embeds are tacky.

8. What is the smallest feature set that makes you comfortable replacing v4? Rank cases/templates, evidence, appeals, tickets, honeypot, general logging, and utilities by launch priority.

> the core moderation engine (templates, cases, action queue) with evidence features, appeals, tickets, honeypot, general logging, audit log mirror.

## Moderation rules and administrator setup

9. Keep the rule that moderators choose a template and Quack chooses the outcome, with no punishment, reason, or level overrides? Are there any exceptional actions administrators must be able to perform?

> no keep the boundary as is. the admins can make templates that are just autobans if they want or the mods can just use the discord built in stuff. for now im not planning on paritying the legacy /ban /timeout add commands.

10. Keep escalation all-time and confined to the same rule? For example, should a third spam case after two years still trigger the third-case outcome?

> there should be a case decay thing. cases should stop counting after some time. but im not sure how. im not sure if it would be cleaner if it was attached directly to the template or a global/guild wide thing... some opt-in decay would be nice but default behavior should be all-time lookup.

11. What should happen once the highest level is reached, and when an old case is voided after newer cases already exist? Should only future decisions change?

> yes for everything changes only affect the future a change should never trigger a new action on an old case.

12. Should a new guild immediately get the current starter policy—two warnings, 24-hour timeout on cases three/four, ban from five—or require an admin to review/activate a policy first?

> Giving a default template is nice but thats a feature that is easy to change later. i think adding templates on join if none exist is nice it helps setup start quicker the system that adds/registers the default template configs should be easy to modify if i want to add more than one default template or change it or whatever.

13. How should an admin build a rule: a short guided flow, several configuration commands, importing a file, or an existing minimal web editor? Which settings deserve an advanced section?

> i want admins to be able to build a rule EXTREMELY easily the ui should be very inutiive. the easies interface for this is the web dashboard (it needs to be cleaned up though) but a discord focused modal could be nice like maybe they run /template create and it shows a modal where they can configure it all. if that is too much for a discord modal to handle we can do it in a command flow system or something.

14. What real templates would you configure for your own server? Give two or three with their reasons, thresholds, outcomes, evidence requirements, and notification/appeal choices.

> Some real ones i would do are NSFW Chatting where the first is a timeout + warn up to 2 cases then the second is a ban on 3rd offense or something. evidence it would be nice to have a log of the message whatever it was. though with evidence if the user gets banned their message gets deleted supplying the discord message link is not enough the message will be deleted most likely so the evidence part needs refinement.

15. Are template edits immediately active? Do admins need preview/simulation, duplication, or a way to compare versions before saving?

> immediately active that would make it way too complicated and confusing. remember discord users are not always very smart so this platform needs to feel almost native to discord.

16. Should staff be able to issue a historical/no-action case for someone who has left, or manually ban a departed scammer? How should that fit the template model?

> hmm im not sure. part of me wants to say thats too much in the minusha, this platform is meant to be HIGHLY customizable every aspect of it is so we should just let the admins figure out that. part of me also sees the issue and feels like building a default handler for something like that may be nice. not sure, need feedback.

17. Is Discord's current permission model enough? For tickets/appeals, may helpers participate without moderation powers, and may every moderator void or review every case?

> the current is enough, though later i may make it so the permissions needed for different systems can be customized by the admin. for now though just stick with all permissions being derived from discord and using moderate members as our base gate.

## Creating and managing cases

18. Preferred entry points: message context action, user context action, `/case add`, or another command name? Which should be fastest?

> /case add is likely the main entrypoint. message/user context action is just a way to do it without typing out the case and pasting the user id/name

19. Should creating a case always show a preview, only confirm kick/ban or escalated outcomes, or act immediately? What information must be visible before confirmation?

> act immediately the mod creates the case the bot queues the appropriate actions and reports back. the mod shouldnt have to confirm an action.

20. Should case commands work in any channel, only designated staff channels, or anywhere with private replies? Where should the permanent moderation result appear?

> that is too fine grained, the bot shouldnt are where its being executed from. admins can setup channel locks in discord later if they dont want commands run there. bot doesnt gaf

21. What belongs on the compact result: member, rule, case number, outcome/duration, moderator, DM status, evidence status? Which details should require opening the case?

> the result after a case was created to show the mod should have member, template name/rule, case number, level selected, actions executed/queued, any errors, if a notification was sent, if they can appeal

22. Which controls should be on a case: View evidence, View history, Void, Replace, Retry, Undo timeout/ban, Review appeal? Which controls need an additional reason or confirmation?

> view evidence button, view user button (shows users case history), void. void should take a reason.

23. Should moderators be able to fix context typos or add evidence after creation without voiding the moderation decision? If so, which changes must remain visible in history?

> yes. they should be able to update the context or add evidence, we dont need to preserve the history just audit log that the case was updated by who

24. When a form fails or times out, should Quack reopen it with answers intact, save a resumable draft, or start over? How long should an unfinished case remain resumable?

> if we can recover it without adding a bunch of saving logic then yes but if its compicated at all then just open blank.

25. When multiple moderators work on the same incident, should Quack warn about a recent similar case, prevent duplicates, or simply use the latest history and show the resulting escalation?

> duplication detection and mitigation are not on the bot if mods accidently create duplicate cases then they need to resolve that the bot should just act accordingly to what the mods tell it to do.

## Evidence and visibility

26. What does “upload evidence” mean to you: capture the selected Discord message, paste several links, attach screenshots/files directly, record off-server evidence, or all of these?

> honestly the nicest thing would just be screenshots. since we already have the idea for the persisitent evidence channel to preserve images we can just use that as storage for images and save the links. this is the best form of evidence and is the easiest. however, not all servers may like that so text based evidence may also be needed. im not sure the evidence part im struggling with and need feedback on how to make it seamless.

27. Should evidence be optional globally, required for particular templates, or selectable per incident? Can an urgent case proceed before evidence is attached?

> probably optional globally if the mod can add evidence great if not then we still should act as normal

28. Should a case include other people's messages for conversation context or witness reports, even when they did not author the offending message?

> no thats too complicated i think

29. Should selecting a message capture just that message, several surrounding messages, or a moderator-selected collection? How should edited/deleted messages appear?

> likely just that message

30. What would convince you that preservation succeeded: a thumbnail and captured text, a saved-file count, a preview of the exact record, or another explicit receipt?

> what? idk what this means?

31. If a file cannot be copied, should Quack stop, ask to continue with a warning, or proceed and flag the missing file? Does the answer differ for a ban versus a warning?

> proceed and flag always

32. If the source message is already gone, what must the moderator provide to proceed? Is a written explanation mandatory, and should manual evidence be marked differently?

> evidence shouldnt be mandatory to proceed. again not sure what a clean evidence system looks like yet. need feedback

33. Should affected members see every context answer and piece of evidence? Do you need confidential reporter identity, staff-only evidence, private notes, or redaction with a recorded reason?

> no for now the end-member would only see the default/template-level reason/description.

34. Should `quack-evidence` be hidden bot storage or a channel moderators can browse? Should admins choose its category/name, and what should happen if they delete it?

> it can be public browsable. creating our own object/s3 store is too much right now. it should be auto-created and if deleted, remade but mods can move it wherever and change who can view it and change the name if they want we only care that that channel ID still exists.

35. Which file types/sizes matter, and for how long must evidence remain usable? Must archived evidence survive deletion of its Discord copy or removal of the bot?

> yea so thats what i was thinking with the evidence channel its basically a free s3 store we just upload the files given by mods ad copy the message link/file link and as long as the quack-sent copy exists mods will be able to view the evidence (i think?)

36. Should staff be able to download an evidence bundle/transcript for a case, or is an in-Discord detail view enough for launch?

> if its in the channel they can download it themselves we dont need to have a separate system for that.

## Actions, errors, and member messages

37. If the selected punishment requires a permission the moderator lacks, block the whole case, ask a senior moderator to approve it, or handle it another way?

> block the case probably and say what happened and what to do

38. If enforcement fails after the case is created, should the case still count toward escalation? Where should staff be alerted, and who is responsible for resolving it?

> if an action fails then we send a message to the audit log saying it failed with a button to retry. when that button is pressed the bot checks the presser's permissions and if they can execute that action and if so they try again, error otherwise. this should be able to be done in the dashboard or in discord and the mods should be able to view a queue of actions that need review

39. If Discord's response is uncertain, what should staff see before retrying? Should Quack first check whether the ban/timeout already happened where possible?

> what do you mean by 'uncertain'? if the message fails to send thats ok the audit log will keep a log of what happens.

40. Does voiding a case automatically remove an active timeout/ban, offer an explicit reversal, or leave punishment unchanged? Is appeal acceptance different?

> voiding a case should undo any active punishment, report if its unable to do so with retry buttons.

41. Should every case send a DM unless a template disables it? What should happen when DMs are blocked or a ban prevents delivery?

> yes by default all template levels have notify on. if a notification cant happen thats fine just record that a notifaction could not be sent.

42. What must a member notification say? Give your preferred wording for a warning, a 24-hour timeout, and a ban; should the moderator's identity be shown?

> Moderator identity should not be shown. You have been banned from X<server> for X <template name>. is the general heading. the rest of it im not sure but keep it minimal the user shouldnt see evidence screenshots just what happened, why, what they can do

43. Do members need a Discord command or DM menu to view their own history even if they never received the original notification?

> i dont want members to be able to view their case history in discord they can go to the dashboard if they want whenever the dashboard supports that.

44. For long records, prefer pagination, expandable sections/buttons, an attached text file, or a web link? The current implementation falls back to `message.txt`.

> prefer pagination and include a button to go to the web equiv.

## Appeals

45. Should a member submit an appeal with a DM button/modal, a bot DM conversation, a separate appeal server, or a minimal web page? How should a banned member find it after losing the original DM?

> DM button into a modal or on the webpage. if they dont have the DM they can do it on the website. but appeals are not a conversations. members can state their case say their appology and thats it then the mods read the apology and decide its not a back and forth kinda thing

46. Is an appeal always about one case, or do you also need general ban/server-access appeals? Which outcomes should be appealable by default?

> 1 appeal = 1 case. a template says if cases used under it can be appealed. thats the behavior we should stick with.

47. Do you want one short “Why should this be reviewed?” answer or configurable questions? Are deadlines, cooldowns, or repeated submissions needed?

> for now just one general form no deadlines 1 appeal per 1 case, later i may make it so that admins can configure their servers appeal form

48. Should staff review an appeal in a single updated message, a private thread, or a queue command? Do you need claiming/assignment so two moderators do not review it simultaneously?

> so legacy handled this by using a dedicated channel where it would send new appeal embeds with accept and reject buttons. i liked that but obviously using the new ui language not embeds.

49. Can the moderator who issued the case decide its appeal? Do some decisions require a second person or administrator?

> yes, for now. these small rules can be a guild setting.

50. Should requesting more information start a conversation, reopen a form, or accept one additional reply? Which replies should notify staff/members?

> no, there is no request more info. the user has one shot. (for now) i dont feel like making a whole 'mod mail' system for the user to talk to the mods through.

51. What exactly should Accept, Reject, and Close mean? Should accepting offer “void only” and “void + remove punishment” as distinct explicit choices?

> accepting voids and attempts to revert punishment, rejecting closes and keeps the punishment. close is the same as reject

52. What should members receive after each decision, and should staff identities remain hidden? Is an invite/rejoin link useful after unbanning?

> They should see that their appeal was accepted an invite link in a link button can be added like "Rejoin Server". mod identities are always hidden from members

## Tickets

53. What kinds of support belong in tickets? Do you need categories and opening questions, or one generic support conversation?

> no openning a ticket just creates a private thread we dont need "What are you here for?" or anything like that. the legacy opened a thread pinged the user and said "A mod will be here soon, feel free to tell us what's up while you wait" basically.

54. Should opening a ticket create a private thread or a private channel? Should members just type normally, and what should the first message contain?

> private thread. no special syntax or first message requirements.

55. Who should see and answer tickets: all moderators, configured support roles, assigned staff, or different staff by category?

> i forget exactly how legacy worked (feel free to check in ./Legacy) but i think you can just create a private ticket and ping the user then mods can see the links in the ticket queue and join the thread i dont think there was any customization that had to be done for who could see it.

56. Should members close/cancel their own tickets? Can they reopen them? What should happen to the Discord conversation after closure and after reopening?

> members can close and mods can close. they cant reopen them. the thread gets deleted because the ticket system should save a transcript like legacy did where it saved message logs in a redis hash while the ticket was opened then formatted them into a txt file and sent it with the updated embed in the queue channel. this can be changed if needed but i like saving a transcript.

57. Which workflow controls matter: claim, transfer, add participant, close reason, inactivity reminder, auto-close, priority? Which should be omitted to keep tickets simple?

> none of these. you are making it too complicated. open. close. thats it. read the legacy ticket system

58. Who gets the transcript, what content/files must it contain, how long should it remain, and should the member receive a copy automatically?

> again. look at legacy. it used to be a ticket queue channel like the appeal queue that just sent embeds when new tickets were made.

59. What limits prevent abuse without frustrating normal support—one open ticket, daily limits, cooldowns? Should failed setup attempts consume a member's allowance?

> one open ticket at a time. thats it.

## Honeypots and general Discord logging

60. Should honeypot setup create a trap channel and warning message like v4, or use an existing channel? Do you want a live caught-user counter and configurable warning text?

> yes it should closely parity legacy

61. Should any human message trigger the selected template, or do some message types/roles bypass it? Exactly which staff permissions should exempt someone?

> mods and higher can send messages there everyone else gets the template.

62. Should repeated trap messages from one member produce one incident/case or several escalating cases? Should Quack capture evidence and then delete every trigger message?

> one incident/case probably. multiple bans are fine i guess but if we could debounce it itd be nice

63. Do you want a test mode that reports what would happen without punishing anyone, including when testing as an admin? How should disabled/broken trap configuration be reported and repaired?

> well since its applying an editable template it doesnt matter we can change the level to just dm to test and if the action fails that will show in the audit log.

64. Which general Discord events need logging, and should they go to one channel or different channels? Should edits/deletions include full content and attachments?

> i want them to all go to one channel. the events should parity legacy pretty closely and the behavior should too. just one channel now instead of multiple thatll make configuration easier.

65. If Quack itself bans someone, should that appear in both general Discord logs and Quack's moderation log, or be represented once when destinations overlap?

> mod log probably. probably only log other bans if they happen not by quack

> September 8 live-test clarification: Keep logging every message edit, including
> Quack editing its own public case/status messages. Suppress the separate ban
> event when Quack performed the ban.

## Audit and operations

66. Beyond case created/voided and user timeout/kick/ban/unban, which changes belong in audit: template edits, module/settings changes, appeal decisions, ticket open/close, evidence added, failed punishment, DM failure?

> template edits, settings changes, appeals, ticket open/close, failed actions.

67. Should one case with a successful ban produce two entries (case created + member banned) or one combined moderation entry? How should retry/reversal outcomes be grouped?

> yes 2 each event is its own log even if they have the same "request id"/originator

68. What fields should an audit entry show by default: actor, affected member, case number, rule, reason, outcome, timestamp? What belongs only in expanded details?

> actor, affected member, case #, rule, outcome/level selected, timestamp. no expanded

69. For existing noisy audit records, should normal views hide technical entries while retaining them separately? Do any operator diagnostics need a developer-only UI, or are structured process logs enough?

> we dont need those technical logs we can just get rid of them im not sure why they were added. we dont care when.

70. Which staff statistics would you actually use? Should reads/page visits have no influence on activity statistics, and do you want summaries in Discord before the dashboard exists?

> staff statistics thrive because they are derived from other values. we track who closes tickets, who reviews appeals, who opens cases, who retries what actions. from those signals we can derive activity and statistics. we shouldnt need a separate system to track these things.

## Migration, maintainability, and acceptance

71. Which current v5 records are disposable test data, and which must survive the rewrite? Which v4 cases, appeals, tickets, settings, and honeypot history must migrate?

> all data in the local database right now is disposable. the schema can be edited without creating mutations (which by the way the mutations stuff in the store package is shit we need to rework that). when launching i will need to transfer over legacy cases, they wont contribute to the new tempalate driven system but it will still be useful to mods to see legacy cases in the user views at least. id like to translate old distributed module settings to the new guild settings but if some resetup is needed thats ok too i guess. as automated as we can make the migration over as possible but im not stressed

72. Must old case numbers, old DM buttons, and existing ticket panels keep working? Is a clean cutover acceptable, or will v4 and v5 overlap?

> a clean cutover is fine for mostly everything besides cases. the overlap stuff i said in the last one applies here too

73. Which code feels hardest to follow? Do you prefer cohesive feature packages, flatter files, fewer interfaces, or specific conventions? Give an example if possible; implementation choices can otherwise be proposed from the review.

> i like self-contained purpose built packages. i like smaller files but i dont like thousands of files either. everything feels very spaghetti right now so cleaner interfaces would be nice.

74. Is a single Go process with MySQL/Redis still the desired operational shape? How many running bot instances should the first release support?

> right now yes its a single binary. only one instance running right now. itll be easy to split up components later if we need to.

75. Which test guild, bot application, and test accounts should later real Discord acceptance use? What timeout/kick/ban/evidence/ticket actions may that rehearsal perform?

> use the Quack's pond discord server (1005778938108325970). my account is @dickey (489264179472236557) my test account is @monkey (498380784323919893) the test bot i @Beta Bot (819019613371236432). i want to use that to extensively test everything.

76. What concrete demonstration would make you say “this is ready”? Name the journeys you want to walk through personally before replacing v4.

> template creation -> case creation. audit review/retries, ticket subsystem, appeals subsystem. v4 is very depended on in a few large servers so i want the whole thing to feel perfect. we arent under a time crunch.

## Decisions already given

- Preserve the main template-based v5 idea while making it smooth to use.
- Honeypot, appeals, tickets, and evidence need working user journeys.
- User-facing audit history should contain meaningful moderation events, not services firing.
- The dashboard is a later step once the bot is ready.
- Review first, then ask detailed questions, then implement the agreed rewrite.
- Preserve existing changes in conventional, logical commits before new work; keep future commits bounded by coherent changes.
- The `quack` tmux session may be used to inspect and work with the running processes.

After answers, revise the product definition, write example interactions and concrete acceptance scenarios, and implement in logical commits. Unanswered questions are not permission to silently change established policy.
