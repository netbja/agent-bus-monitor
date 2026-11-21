# busmon views: one question per view (Work, Board, Plan)

Status: r4, spec and mock-ups only, no code. Every question is answered (§8, §9, the person on 2026-11-17 and 2026-11-18): the spec is final. Base: origin/dev `35a0b7f2`. Author: coder2, from the
person's direction relayed by master on 2026-11-17 (task `views-redesign-spec`). It supersedes the
`ideas-triage-and-plan-spec` request given to coder. It builds on the requests-plan redesign, whose
§9 (decided 2026-11-10) is built: the Work tab, the Kanban and the Projects counts
([2026-11-09-requests-plan-redesign-mockups.md](2026-11-09-requests-plan-redesign-mockups.md)).
The person answered §8's questions on 2026-11-17; the spec and the mock-ups follow the answers.

## 1. Problem

- **Two views answer the same question.** The For you panel, Work's YOU and the Plan tab each say
  what the person owes. The person reads "Nothing expected from you." in For you while its label
  and the header say `10 decide` (§6).
- **The Board and the Requests view are two lists of the same work.** The Board lists tasks (task,
  owner, state, age). Today's Requests sub-view (Work → between agents) lists the same tracked
  requests as threads, with the diagnostics. The person has to look in two places to see one
  stuck request.
- **The Plan tab is a copy of For you.** Key `4` opens Work on NEXT's first row (§9 step 2), and the
  plan drawer (`p`) repeats the person's tasks with a `person · waiting for you` column. It shows no
  order, no dependencies and no priority, so it is not a roadmap.
- **Priority is text.** Low-priority plan tasks carry `LOW:` in their title (v0.29.json: three
  tasks). Nothing sorts by it, and plan tasks have no priority field (`plan.Task`).

## 2. One question per view

| Key | View | Its one question | Counts it shows |
|---|---|---|---|
| 1 | Activity | What happened? (unchanged) | none |
| 2 | Board | Who holds which task, and is anything stuck? | tasks, anomalies |
| 3 | Work | What is owed, by whom, now? | the ONLY `for you` counts |
| 4 | Plan | What comes next in v0.29, in which order? | progress (done/total) |

- **Work is the only producer of "for you"** (`workOf`, §9 step 1). The header's strip, the For you
  panel and Work's YOU read it; Board and Plan never show a `for you` count, in their titles or rows.
- `p` and `P` keep opening the plan (as the drawer today); `4` becomes the Plan tab itself (§4).

## 3. Board (2): Board and Requests merged

One list, one line per task, Board's format: **task, owner, state, age**, newest first.

- **Done is hidden by default**: `d` shows the done tasks (as the plan's DONE). The frame's label says
  `12 open · 31 done (d)`.
- **An alert column only when something is wrong**, never a column of blanks (the person's Q1):
  - `no acceptance 40m`: a tracked request not accepted past `board.no_accept_after` (a new
    setting, default 30 minutes; 0 = off). A request never delivered is not accepted either: its
    diagnostics (Enter) say which;
  - `no reply 3h`: accepted, no response past `board.no_reply_after` (a new setting, default
    2 hours; 0 = off);
  - `body lost`: the request's body is no longer in the stream (`availability: missing`), shown
    at once.
  
  Both settings are in Shift+C's Display group and apply at once.

  Rows with an alert come first, oldest alert first, then the others by age. The column exists
  only while at least one row has an alert.
- **Enter opens the row's diagnostics**, today's Requests detail for that thread: the delivery
  cursor, the pending list, the request's facts, the thread's entries. The between-agents line of
  Work opens this Board, filtered to the rows between agents (no request to or from the person);
  Esc returns to Work on that line. **The Requests sub-view stays, every thread** (the person's
  choice (a), §9 Q9): the untracked threads, the notices, the directives and the open asks, with
  its keys `d`, `o`, `+` and `-`. `r` and `F2` open it from anywhere, the Board included, in place
  of the former read-only Requests page, which is retired: one Requests view. It remembers the tab
  it was opened from for Esc, the tab bar and the arrows only; `d`, `o`, `+` and `-` mean the same
  whatever opened it.
- **busmon never changes a request itself.** The row's actions are words in the footer, each
  naming who acts:
  - `resend`: the dispatcher's (it already re-wakes; a stuck delivery is its alarm);
  - `cancel`, `reassign`: master's (a directive to master, prefilled in Compose for the person to
    send);
  - missions flag their own stuck requests (the conductor).
  - **If master is dead** (no fresh status, no terminal; the person's Q2 and Q8): the row says
    `master is down; self-repair relaunches it`. A resend needs nothing from busmon: the
    dispatcher already wakes the target again on its own. A cancel or a reassign waits for master,
    whom the dispatcher's self-repair relaunches first (decision 2026-11-09). busmon asks the
    dispatcher for nothing, and no request changes while master is down.
- **No `for you` count in its title**: a request the person owes is in Work (YOU), and its Board
  row has no special mark.

### Mock-up: Board at 160 columns (one alert)

```
╭─ 2 Board ──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────── 12 open · 31 done (d) ─╮
│ ALERT              TASK                                                         OWNER            STATE        AGE                                            │
│ ⚠ no reply 3h      quota-step-3                                                 coder            accepted     3h                                             │
│                    ask-refuses-person                                           coder2           review       12m                                            │
│                    agent-screen-blocked                                         coder            working      40m                                            │
│                    views-redesign-spec                                          coder2           accepted     5m                                             │
│                    holder-g1-part2                                              coder            queued       1h                                             │
│                    braille-graphs-feasibility                                   coder            queued       2h                                             │
╰─ enter diagnostics · d done · resend (dispatch) · cancel/reassign (master) ──────────────────────────────────────────────────────────────────────────────────╯
```

At 160 columns the free space goes to TASK, which is never cut before OWNER.

### Mock-up: Board at 80 columns (no alert: no alert column)

```
╭─ 2 Board ──────────────────────────────────────────── 12 open · 31 done (d) ─╮
│ TASK                             OWNER      STATE      AGE                   │
│ ask-refuses-person               coder2     review     12m                   │
│ agent-screen-blocked             coder      working    40m                   │
│ views-redesign-spec              coder2     accepted   5m                    │
│ holder-g1-part2                  coder      queued     1h                    │
╰─ enter diagnostics · d done ─────────────────────────────────────────────────╯
```

At 80 columns with an alert, the alert takes the first 12 cells (`⚠ no reply 3h` cut to
`⚠ no reply…`), and TASK gives up its room first.

## 4. Plan (4): the v0.29 roadmap

`4` opens the Plan tab (no longer an alias to Work's NEXT); `p` and `P` still open it as today.

- **A roadmap, not a copy of For you:** sections **NOW**, **APPROVED**, **NEXT** and **LATER**, then
  DONE folded to a count (`d`).
  - NOW: in progress and in review.
  - APPROVED: both reviewers approved, waiting for delivery or the merge: its own section between
    NOW and NEXT, folded away when empty (the person's Q3).
  - NEXT: not started, unblocked, by priority then file order.
  - LATER: blocked by another task (`waits on <id>`), then the low-priority tasks, blocked or not:
    an unblocked low-priority task is in LATER, never in NEXT.
- **No `person · waiting for you` column.** A task the person owns shows `♙` before its owner; what
  the person must do now is Work's, not repeated here.
- **Dependencies** show as `after <id>` on the task's line (the first one, `+N` for more); the side
  panel (wide tier) lists all of them and the tasks that wait on this one.
- **Priority**: `↑` high, `↓` low, nothing for normal, the marks requests already carry
  (`priorityMark`), in Plan, the Kanban and the requests alike (the person's Q7). `▲` and `▼` keep
  meaning blocked (`GlyphBlocked`) and down (`GlyphDown`). Their colour is never the header's
  yellow warning; a future ASCII setting (the braille-graphs idea,
  `docs/memory/open-questions/2026-11-17-braille-graphs.md`) covers them too: `^` and `v`.
  `+` and `-` on a plan task raise or lower it (the person's, as `request priority`). NOW and NEXT
  sort by priority, then file order.
  - Plan tasks gain an optional `priority` field (`high`, `normal`, `low`; absent = normal) in the
    plan file; the three `LOW:` titles become `priority: low` and lose the prefix.
  - **Master hands out the highest unblocked task first**: `request next` already orders tracked
    requests by priority; a plan task's priority is copied to the request master sends for it.

### Mock-up: Plan at 160 columns

Superseded at the wide tier by §4.1 (2026-11-21): the side panel became three rows under the
list, and the drawer went under For you.

```
╭─ 4 Plan · v0.29 ─────────────────────────────────────────────────────────────────────── 28/43 done ▕████████████░░░░░░▏ ─╮╭─ quota-step-3 ───────────────────╮
│ NOW                                                                                                                      ││ owner   coder                    │
│ ● agent-screen-blocked       coder     working    after holder-g1                                                        ││ state   queued 3h                │
│ ◐ ask-refuses-person         coder2    review     foureyes 12m · reviewer-codex 12m                                      ││ after   quota-settings-step2 ✓   │
│ APPROVED                                                                                                                 ││ unblocks quota-step-4            │
│ ✓ human-not-an-agent-test    coder2    approved   deliver                                                                ││ prio    ↑ high (+ -)             │
│ NEXT                                                                                                                     ││ request quota-step-3 queued      │
│ ↑ quota-step-3               coder     queued     after quota-settings-step2                                             ││                                  │
│   holder-g1-part2            coder     queued                                                                            ││                                  │
│   decide-compose-undo        ♙ person  decide                                                                            ││                                  │
│ LATER                                                                                                                    ││                                  │
│ ↑ quota-step-4               coder     blocked    after quota-step-3                                                     ││                                  │
│ ↓ master-conversation        coder     low        after decide-master-conversation                                       ││                                  │
│ ↓ agent-led-setup            coder     low                                                                               ││                                  │
│ DONE 28 (d)                                                                                                              ││                                  │
╰─ + - priority · d done · enter task ─────────────────────────────────────────────────────────────────────────────────────╯╰─  ───────────────────────────────╯
```

### Mock-up: Plan at 80 columns (no side panel)

```
╭─ 4 Plan · v0.29 ──────────────────────────────────────────────── 28/43 done ─╮
│ NOW                                                                          │
│ ● agent-screen-blocked     coder     working   after holder-g1               │
│ ◐ ask-refuses-person       coder2    review                                  │
│ APPROVED                                                                     │
│ ✓ human-not-an-agent-test  coder2    deliver                                 │
│ NEXT                                                                         │
│ ↑ quota-step-3             coder     queued    after quota-settings-step2    │
│   holder-g1-part2          coder     queued                                  │
│   decide-compose-undo      ♙ person  decide                                  │
│ LATER 3 · 1 ↑ (enter) · DONE 28 (enter)                                      │
╰─ + - priority · enter task ──────────────────────────────────────────────────╯
```

At 80 columns LATER folds to a count that Enter opens, as DONE does, and the count keeps a
high-priority task visible: `LATER 3 · 1 ↑` (the person's Q5).

### 4.1 Layout r2: the person's review of the Plan view (2026-11-21)

The person's review of v0.31's Plan view (bus thread `1795220188690-0`, relayed by master as cmd
`1795220210959-0`), four points, built in one PR (`feat/plan-tab-layout`). The plan's groups
(plan-chantier §3) are the blocks below; a plan without groups keeps §4's sections.

1. **For you sized to its content.** The drawer (`p`, `P`) stood right of For you, which kept the
   top row's full height for three lines, while the drawer's lower half was empty. Now For you
   takes the whole width, its own rows only (its items and the plan pointer, at most the top
   row's height); the drawer takes the whole width under it, down to the footer. Agents is under
   the drawer, as before. The narrow and medium tiers do not change.
2. **The task's detail below the list**, inside the Plan frame, in place of the side panel (34
   columns that cut every title, state and value). Three rows at the bottom of the frame:
   - a rule naming the task, `enter task page` at its right;
   - `state`, `for you`, `owner`, `prio`, `review`, `PR` (what matters to the person first, so a
     short width cuts the least);
   - `after`, `unblocks`, `reason`.

   Each row keeps its fields whole and cuts its last one when the width is short. The list scrolls
   above the three rows; the selected task stays in view. The tab and the drawer alike, at the wide
   tier (as the side panel was).
3. **A finished group is one line**: `✓ <title>  <done>/<total> done`, grey, no bar, and the
   finished groups follow one another with no blank line between them. DONE's count line follows.
4. **A group's bar in the colour of its state**, the colours of the tasks' own glyphs (`planLook`):
   - orange (`RoleAttention`): a task of the group waits for the person;
   - blue (`RoleAccent`): a task is in progress or in review, none waits for the person;
   - grey (`RoleMuted`): finished (point 3: no bar is drawn, the line is grey);
   - the text colour: nothing started yet.

   The approved part of the bar keeps its own colour (plan-chantier §3). The roles are the theme's,
   so the light palette's contrast rule holds (`theme_contrast_test.go`). Without colour
   (`NO_COLOR`, the no-colour palette) the header's words say the same: `waits for you: <id>`,
   `in progress: <id>`, `done`.

#### Mock-up: the drawer at 140 columns

The golden `plan-groups-140x40-dark.txt`, header and footer left out. Leave herdr's bar is orange
(h2 waits for the person), Self-repair watcher's and Other's in the text colour (nothing
started), the approved cell in the review colour, the rest of each bar the grey track; Work tab's
line is grey.

```
╭─ For you ───────────────────────────────────────────────────────────────────────────────────────────────────────────────────── 4 decide ─╮
│  ● answer master    review lot 1? (2 questions)                                                                                        4m│
│  ♙ answer foureyes  need a decision on the width rule                                                                                  6m│
│  ◆ review coder     by charlie  look at b1                                                                                               │
│  Next for you: title h2                                                                                                                  │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
╭─ Plan ─────────────────────────────────────────────────────────────────────────────────────────────────────────────── y copy · esc back ─╮
│ v0.29  3 of 9 done  ━━━━━━━━━━━━━━━━━━━━  33%  ·  now: title h1 (coder)  ·  next: title s1                                               │
│ the inbox · loaded 0s ago (abcdef0)                                                                                                      │
│                                                                                                                                          │
│ Leave herdr         ━━━━━━━━━━━━━━━━━━━━  1/5 · in progress: h1 · waits for you: h2                                                      │
│ ▶ ● title h1  coder     in progress                                                                                                      │
│   ✓ title h5  coder     approved                                                                                                         │
│     title h2  ♙ person  decide                                                                                                           │
│     title h3  coder     blocked                                                                                                          │
│                                                                                                                                          │
│ Self-repair watcher ━━━━━━━━━━━━━━━━━━━━  1/2                                                                                            │
│     title s1  coder     upcoming                                                                                                         │
│                                                                                                                                          │
│ Other               ━━━━━━━━━━━━━━━━━━━━  0/1                                                                                            │
│     title o1  coder     upcoming                                                                                                         │
│                                                                                                                                          │
│ ✓ Work tab  1/1 done                                                                                                                     │
│                                                                                                                                          │
│ DONE  3   d shows                                                                                                                        │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│ ── Task h1 ───────────────────────────────────────────────────────────────────────────────────────────────────────────── enter task page │
│ state in progress · for you nothing · owner coder · prio normal · review — · PR —                                                        │
│ after — · unblocks — · reason —                                                                                                          │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

#### Mock-up: the tab at 140 columns

The golden `plantab-groups-140x40-dark.txt`'s lower panel (`4`): the same board and the same three
rows, For you and Agents above it as before.

```
╭─  1 Activity   2 Board   3 Work   4 Plan   · v0.29 ──────────────────────────────────────────────────────── 3/9 done ██████             ─╮
│  Leave herdr         ━━━━━━━━━━━━━━━━━━━━  1/5 · in progress: h1 · waits for you: h2                                                     │
│▶ ● title h1  coder     in progress                                                                                                       │
│  ✓ title h5  coder     approved                                                                                                          │
│    title h2  ♙ person  decide                                                                                                            │
│    title h3  coder     blocked                                                                                                           │
│                                                                                                                                          │
│  Self-repair watcher ━━━━━━━━━━━━━━━━━━━━  1/2                                                                                           │
│    title s1  coder     upcoming                                                                                                          │
│                                                                                                                                          │
│  Other               ━━━━━━━━━━━━━━━━━━━━  0/1                                                                                           │
│    title o1  coder     upcoming                                                                                                          │
│                                                                                                                                          │
│  ✓ Work tab  1/1 done                                                                                                                    │
│                                                                                                                                          │
│  DONE  3   d shows                                                                                                                       │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│                                                                                                                                          │
│ ── Task h1 ───────────────────────────────────────────────────────────────────────────────────────────────────────────── enter task page │
│ state in progress · for you nothing · owner coder · prio normal · review — · PR —                                                        │
│ after — · unblocks — · reason —                                                                                                          │
╰─ + - priority · d done · enter task ─────────────────────────────────────────────────────────────────────────────────────────────────────╯
```

The goldens `plan-groups-*` and `plantab-groups-*` (every size, dark and light, text and colours)
are these screens as built.

## 5. IDEAS: triage from the card

The ideas are notes in `docs/memory/open-questions/` (ideas-as-notes, `docs/memory/IDEAS.md`). On an
idea's card (Work's IDEAS row or the Kanban's Ideas card):

Enter opens a small triage panel; `y`, `l` and `n` act only inside it, so a task is created by two
key presses, never one (the person's Q6). Everywhere else `y` keeps copying.

- `y` **yes**: master adds a plan task for it and queues it; the note becomes `resolved`, linking the
  task.
- `l` **later**: nothing moves; the note stays `proposed`, and the card shows `later` with the date.
- `n` **no**: the note becomes `superseded` with the person's "no" and the date, and leaves IDEAS.
- Esc closes the panel, nothing sent.

```
╭─ Idea · language-setting ──────────────────────────╮
│ A language setting for busmon and the CLI          │
│ docs/memory/open-questions/2026-11-17-language-…   │
│                                                    │
│   y  yes: a plan task, queued by master            │
│   l  later: the note stays                         │
│   n  no: the note is closed                        │
╰─ esc close ────────────────────────────────────────╯
```

busmon never edits the repository: each key sends master a directive (`idea <id>: yes|later|no`),
and master (or docs) writes the note in a PR. The card shows `sent` until the note changes.

This needs busmon to list the notes, which it does not do today: IDEAS shows the broker's ideas
(`agentbus idea`), and `memory.OpenQuestions` has no caller. Wiring it is step 1 (§7).

## 6. Fixes

- **"Nothing expected from you." while decisions wait.** The For you panel shows the person's plan
  tasks as one pointer, not as rows (requests-plan §9, Q5). With no row, `forYouEmptyRows` writes
  "Nothing expected from you." whenever nothing is owed (`owed == 0`), then the plan pointer
  (`planPointer`, the person's first waiting, blocked or upcoming task) under it, while the label
  says `10 decide`. Fix: when the strip counts decisions, that first line reads
  `10 decisions in the plan (3 Work)`, attention role, and the pointer stays under it;
  "Nothing expected from you." only when the strip counts nothing.
- **Kanban For you cards repeat `decide · you`.** Every card's first line is
  `<glyph> decide · you · 2h`: "you" is true of the whole column, and the glyph already says the
  verb. Fix: the first line is the glyph and the title; the second is the age and the place
  (`2h · #t8`). A merge card keeps `merge PR N` as its title.

## 7. Steps (after the person's answers)

1. IDEAS reads the notes (`memory.OpenQuestions` wired into the Work model, beside the broker's
   ideas), and the stale comment in `work_model.go` corrected.
2. The two fixes of §6.
3. Board and Requests merged (§3), the `board.no_accept_after` and `board.no_reply_after` settings.
4. Plan as tab 4, the roadmap sections, `priority` in the plan file and the glyphs (§4).
5. IDEAS triage keys (§5).

## 8. The person's answers (2026-11-17)

1. **Board alerts:** two settings in Shift+C, "no acceptance" (30 minutes) and "no reply" (2 hours),
   both adjustable (§3).
2. **Master dead:** busmon first proposes waiting for master's self-repair (§3); Q8 below narrowed
   the rest to resend only.
3. **APPROVED:** its own section between NOW and NEXT, folded when empty (§4).
4. **Priority:** a glyph per level, nothing for normal, in a colour distinct from the header's
   yellow warning; an ASCII setting may cover them (§4). Q7 below chose the glyphs.
5. **80 columns:** LATER folds to a count that Enter opens, keeping a high-priority task visible,
   `LATER 3 · 1 ↑` (§4).
6. **IDEAS:** `y` stays copy everywhere; Enter on an idea opens a triage panel where `y`, `l`, `n`
   act (§5).

## 9. The person's re-decisions (2026-11-18)

7. **Priority glyphs:** `↑`/`↓` everywhere, the requests' marks; `▲`/`▼` stay blocked and down
   (the earlier `▲`/`▼` choice did not know they were taken).
8. **Master down:** resend only, which the dispatcher already does. No cancel while master is down:
   cancel stays master's; no new board state, no new dispatcher act, no security review (§3).
9. **The Requests sub-view (2026-11-20):** (a), it stays, reached by `r`/`F2` from anywhere, the
   Board included; the old read-only Requests page goes (§3). Work's between-agents line opens the
   Board filtered to the rows between agents.
