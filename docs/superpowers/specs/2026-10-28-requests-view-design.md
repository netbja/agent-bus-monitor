# busmon Requests view: who owes whom an answer — design

Status: draft for review (coder, 2026-10-27). Task `requests-view`, step 1 (mock-up only, no code),
branch `docs/requests-view-spec`, review subject `requests-view-spec`. ui-kimi was to draw it and is
held by Kimi's weekly quota.

## The problem

Bernard's screenshot of the Requests tab (2026-10-27, `Screenshot_agentbus-Requests.png`): "39
between agents", one tracked request, then dozens of lines like

```
directive  coder → master  Report 1793042904702-0 for you (full text: agentbus reports 1793042904702-0): busmon-charm-a (step A) done — feat/busmon-…
  asked 23h ago
```

each on two rows. It is hard to read for four reasons, all visible in today's data:

1. **Boilerplate first.** `report --to` sends the receiver a directive whose text is
   `Report <id> for you (full text: agentbus reports <id>): <first line>` (`reportNotice`,
   `cmd/agentbus/reports.go`). The first 70 columns of every such row are the same.
2. **Everything is "asked", nothing is grouped.** A report notice, a dispatcher quota warning, a
   review challenge and the person's own question all render as one `directive … asked Nh ago`
   line, sorted by age. The 13 challenges coder sent reviewer-codex are 13 rows (26 lines).
3. **"No reply" is not "owed".** `openThreads` (`cmd/busmon/tracking.go`) lists every directive or
   challenge with no reply or verdict *on its thread*. Most notices never get one by design: a report
   notice is read and acted on; a quota warning is information. Master answers most of the person's
   messages by acting, not by `reply`.
4. **No direction.** The person cannot tell at a glance what waits for *them*, what *they* wait
   for, and what is between agents.

Today's numbers (agent-bus-monitor, cmd stream read at `1793135354534`, read-only): 68 threads
started in the last 24 h with no reply or verdict on their thread, 77 answered; 1 tracked request
waiting. Of the 68: bernard → master 22 directives, coder → reviewer-codex 13 challenges,
coder → master 10 report notices, dispatch → sentinel 6 and dispatch → master 5 notices,
sentinel → master 6, master → sentinel 2, master → foureyes 1, master → coder 1, coder → ui-kimi 1,
dispatch → coder 1.

## What the view is for

**Who owes whom an answer, now.** One question per section, in this order:

1. **Owed by you**: tracked requests to you, threads addressed to you with no reply (asks stay in
   For you, which already ranks them; this section links to it, never duplicates its rows).
2. **You wait on**: what you (the person, `humanIdentity`) sent with no reply on its thread,
   grouped by recipient.
3. **Tracked requests** between agents still waiting (the board's facts: accepted, blocked, deadline).
4. **Reviews without a verdict**: challenges whose thread holds no verdict, grouped by reviewer,
   listing their subjects.
5. **Directives between agents** with no reply on their thread, grouped by sender → target.
6. **Notices**, folded: report notices and the dispatcher's informational directives (`from =
   dispatch`). Nobody owes an answer; they are listed only so nothing is hidden.
7. **Answered or older than 24 h**, folded to one count (`o` shows them).

The title counts what the first two sections and the between-agents sections hold: `N for you ·
you wait on M · K between agents`. Notices and folded threads never count (today: 0 · 22 · 21
instead of "39 between agents").

## Rules

- **A thread is the unit**, as now (`threadID`: its `ref`, else its root id). **A row is a group**
  of threads with the same section, sender, target and kind; its count is the number of threads
  and its age the oldest one's. A group of one shows that thread's own text. Groups of more show
  the newest thread's text and, for reviews, the subjects (`[subject S]` from the gate meta, else
  the first word of the challenge) joined by ` · `. Enter on a group expands it in place into its
  threads; Enter on a thread opens it (below).
- **Kinds** (shown in the KIND column at ≥110 columns): `request` (tracked, from the board),
  `challenge`, `directive`, `reports` (report notices), `notices` (`from = dispatch`, or a cmd with
  `wake=no`). A report notice is recognised by the exact prefix `reportNotice` writes, and only that.
- **The subject is the first line, without the boilerplate.** For a report notice, the text after
  `): ` (the report's own first line, already ≤300 runes); for a tracked request, its task; for a
  challenge, its subject when stored, else its first line; otherwise the first line of the message.
  `Preview` sanitising as today; the ellipsis is the glyph set's.
- **State, from facts busmon already reads**, never inferred from silence:
  `unread` (the entry is above the target group's `last-delivered-id`, or still in its pending
  list: one `XINFO GROUPS` and one `XPENDING` per target with open threads, read with the
  dispatcher facts every 5th tick; `Obligation` gives only counts), `read` (delivered and
  acknowledged, no reply), `accepted`/`blocked`/deadline (a
  tracked request's board facts), `held quota` (the target's provider is held), `replied` (folded).
  At ≥160 columns it has its own column; below, it is the row's marker (the `unread.go` marks: ●
  unread, ○ read; `▸` only marks the selection, as in every list).
- **Folding.** Threads older than `bus.DefaultHorizon` (24 h) and answered threads are one line at
  the bottom (as the count already ignores them, `recentThreads`). Notices are one row per sender →
  target. `+ N older` rows collapse a group longer than 3 in "you wait on". `o` toggles the folded
  section open, `Enter` on a folded row expands it.
- **Tracked requests first among the agents' sections**: they are the only ones with recorded
  acceptance; their facts row (`accepted · 1.1h · no deadline`) moves into the STATE and AGE
  columns, so each request is one line.
- **Order inside a section**: unread before read, then oldest first (what waits longest is owed
  most), except "you wait on", newest first (the person reads their own latest message first).

## What Enter opens

| Row | Enter |
|---|---|
| a group | expands it in place (Enter again on the group folds it) |
| a thread | the reader's thread page: `Bus.Thread` (every entry of the thread, chronological), as `agentbus thread <id>` prints it |
| a report notice | the report itself, full text (`agentbus reports <id>`), markdown rendered as in Activity |
| a tracked request | its request page (today's `req:` page) |
| a review group | the subject's verdicts roll-up (`agentbus verdicts --subject S`) plus its open gates |
| a notice group | the list of those notices, one line each |

`d` then `y` on a thread still closes it with a `human=yes`, `wake=no` reply (`Bus.HumanClose`), as
today; on a group, it asks once for all its threads (`y` closes N).

## Mock-ups

Drawn from today's data by a script (exact widths: every line is 190, 120 or 80 cells). Texts are
the real first lines, sanitised, French kept as written. Three tiers, as `shellLayoutFor`: wide
(≥120: FROM → TO, KIND, STATE, SUBJECT, N, AGE at ≥160; STATE folds into the marker below 160),
medium, narrow (no KIND, shorter title).

### 190 columns

```
┌─ 1 Activity  2 Board  [3 Requests]  4 Plan ─────────────────────────────────────────────────────────────────────────────────────────────── 0 for you · you wait on 22 · 21 between agents ─┐
│   FROM → TO                  KIND      STATE       SUBJECT / FIRST LINE                                                                                                         N      AGE │
│ owed by you  ·  0                                                                                                                                                                          │
│ you wait on master  ·  22 directives, read, no reply on their thread  (oldest 2.0h)                                                                                                        │
│ ▸ bernard → master           directive read        à noter dans les features, le systeme de déploiement de texte dans activity n'est pas très "jolie"                           1      now │
│ ▸ bernard → master           directive read        Pour moi c'est off jusqu'a demain, je te laisse avancé tant que possible.                                                    1       6m │
│   bernard → master           directive read        + 20 older  (Enter expands)                                                                                                 20     2.0h │
│ tracked requests  ·  1 waiting                                                                                                                                                             │
│ ▸ master → coder             request   accepted    busmon-group-a                                                                                                               1     1.1h │
│ reviews without a verdict  ·  1 reviewer                                                                                                                                                   │
│ ▸ coder → reviewer-codex     challenge held quota  lifecycle-spec · gate-subject-close · a-pending-pane · a-health · a-identity · a-nav · a-plan-tab · a-look                  13     1.0h │
│ directives between agents, no reply on the thread  ·  4 pairs                                                                                                                              │
│ ▸ master → sentinel          directive read        Reload of Claude Code 2.1.311 happens now (Bernard's go): coder, foureyes, you, master last.                                 2      14h │
│ ▸ coder → ui-kimi            directive unread      busmon-charm-b4: your finding (the wrap cost per key, and per fade frame) is fixed on feat/busmon-charm-b4                   1      12h │
│ ▸ master → foureyes          directive read        Bernard reloads Claude Code 2.1.311 now: he will /exit your pane and master relaunches with agentbus up                      1      12h │
│ ▸ sentinel → master          directive read        Claude Code 2.1.312 is installed; 4 agents still run 2.1.311: coder, foureyes, master, sentinel.                             6      48m │
│ notices  ·  folded, nobody owes an answer  (Enter lists them)                                                                                                                              │
│ · coder → master             reports               gate-subject-close PR #144: foureyes APPROVED 3fc10f7; reviewer-codex pending                                               10      now │
│ · dispatch → sentinel        notices               client update: claude-code 2.1.312 installed; still on an older one: coder, foureyes, master, sentinel                       6      48m │
│ · dispatch → master          notices               openai is held until Wed 28/10 01:51 (4h, 5h window).                                                                        5      42m │
│ · dispatch → coder           notices               context 80%: at your next stopping point commit WIP and reply to master with a hand-off                                      1      24m │
│ answered or older than 24 h  ·  77 folded  (o shows them)                                                                                                                                  │
└────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### 120 columns

```
┌─ 1 Activity  2 Board  [3 Requests]  4 Plan ───────────────────────── 0 for you · you wait on 22 · 21 between agents ─┐
│   FROM→TO                KIND      SUBJECT / FIRST LINE                                                       N  AGE │
│ owed by you  ·  0                                                                                                    │
│ you wait on master  ·  22 directives, read, no reply on their thread  (oldest 2.0h)                                  │
│ ▸ bernard→master         directive à noter dans les features, le systeme de déploiement de texte dans acti…   1  now │
│ ▸ bernard→master         directive Pour moi c'est off jusqu'a demain, je te laisse avancé tant que possibl…   1   6m │
│   bernard→master         directive + 20 older  (Enter expands)                                               20 2.0h │
│ tracked requests  ·  1 waiting                                                                                       │
│ ▸ master→coder           request   busmon-group-a                                                             1 1.1h │
│ reviews without a verdict  ·  1 reviewer                                                                             │
│ ▸ coder→reviewer-codex   challenge lifecycle-spec · gate-subject-close · a-pending-pane · a-health · a-ide…  13 1.0h │
│ directives between agents, no reply on the thread  ·  4 pairs                                                        │
│ ▸ master→sentinel        directive Reload of Claude Code 2.1.311 happens now (Bernard's go): coder, fourey…   2  14h │
│ ▸ coder→ui-kimi          directive busmon-charm-b4: your finding (the wrap cost per key, and per fade fram…   1  12h │
│ ▸ master→foureyes        directive Bernard reloads Claude Code 2.1.311 now: he will /exit your pane and ma…   1  12h │
│ ▸ sentinel→master        directive Claude Code 2.1.312 is installed; 4 agents still run 2.1.311: coder, fo…   6  48m │
│ notices  ·  folded, nobody owes an answer  (Enter lists them)                                                        │
│ · coder→master           reports   gate-subject-close PR #144: foureyes APPROVED 3fc10f7; reviewer-codex p…  10  now │
│ · dispatch→sentinel      notices   client update: claude-code 2.1.312 installed; still on an older one: co…   6  48m │
│ · dispatch→master        notices   openai is held until Wed 28/10 01:51 (4h, 5h window).                      5  42m │
│ · dispatch→coder         notices   context 80%: at your next stopping point commit WIP and reply to master…   1  24m │
│ answered or older than 24 h  ·  77 folded  (o shows them)                                                            │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### 80 columns

```
┌─ 1 Act 2 Brd [3 Req] 4 Plan ─────────────────────────────────────── 22 · 21 ─┐
│   FROM→TO           SUBJECT                                           N  AGE │
│ owed by you  ·  0                                                            │
│ you wait on master  ·  22 directives, read, no reply on their thread  (oldes…│
│ ▸ bernard→master    à noter dans les features, le systeme de déplo…   1  now │
│ ▸ bernard→master    Pour moi c'est off jusqu'a demain, je te laiss…   1   6m │
│   bernard→master    + 20 older  (Enter expands)                      20 2.0h │
│ tracked requests  ·  1 waiting                                               │
│ ▸ master→coder      busmon-group-a                                    1 1.1h │
│ reviews without a verdict  ·  1 reviewer                                     │
│ ▸ coder→reviewer-c… lifecycle-spec · gate-subject-close · a-pendin…  13 1.0h │
│ directives between agents, no reply on the thread  ·  4 pairs                │
│ ▸ master→sentinel   Reload of Claude Code 2.1.311 happens now (Ber…   2  14h │
│ ▸ coder→ui-kimi     busmon-charm-b4: your finding (the wrap cost p…   1  12h │
│ ▸ master→foureyes   Bernard reloads Claude Code 2.1.311 now: he wi…   1  12h │
│ ▸ sentinel→master   Claude Code 2.1.312 is installed; 4 agents sti…   6  48m │
│ notices  ·  folded, nobody owes an answer  (Enter lists them)                │
│ · coder→master      gate-subject-close PR #144: foureyes APPROVED …  10  now │
│ · dispatch→sentinel client update: claude-code 2.1.312 installed; …   6  48m │
│ · dispatch→master   openai is held until Wed 28/10 01:51 (4h, 5h w…   5  42m │
│ · dispatch→coder    context 80%: at your next stopping point commi…   1  24m │
│ answered or older than 24 h  ·  77 folded  (o shows them)                    │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Out of scope, noted

- **`report` takes `--ref` as text.** Today's report notices start `--ref 1793131308998-0 …`
  because coder passed `--ref` to `report`, which has no such flag and joined it into the message.
  The view would show it as the first words. Fix separately: `report` should refuse an unknown
  `--flag` (or accept `--ref` as the notice's thread). Not part of this view.
- **Answers by action.** Master answers most of the person's messages by acting, not by
  `reply`. The view does not guess: it shows `read`, which is a fact. Whether `report --to <person>`
  should carry the thread so it counts as a reply is a protocol question for later.

## Tests (step 2, the code)

Pure functions over fixtures built from today's shapes, plus the shell goldens:

- `requestGroups(cmds, reqs, obligations, self)`: section and kind per entry (report notice by exact
  prefix only, `dispatch` and `wake=no` as notices); groups by section, sender, target, kind; counts
  and oldest age; 24 h and answered folding; order rules.
- `reportSubject`: strips exactly `reportNotice`'s prefix and nothing that merely resembles it.
- the title counts (`0 · 22 · 21` on today's fixture; notices never counted).
- goldens at 190, 120 and 80 columns from the same fixture as the mock-ups; `TestShellAlignmentSuite`
  over the new rows (hostile texts, wide clusters).
- Enter on each row kind opens the page in the table; `d`/`y` on a group closes each thread once.

## Open questions for review

1. Should the person's own unanswered messages ("you wait on") count in the title, or only
   "owed by you"? Proposed: shown, counted separately, never in the attention colour.
2. Should notices be hidden entirely rather than folded? Proposed: folded, one row per pair, so a
   dispatcher warning is never lost.
3. Is `read` (acknowledged, no reply) worth a state, given that drains acknowledge everything?
   Proposed: yes, it separates "never reached" (unread) from "reached, not answered".
