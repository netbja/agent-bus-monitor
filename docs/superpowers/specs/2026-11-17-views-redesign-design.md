# busmon views: one question per view (Work, Board, Plan)

Status: r1, spec and mock-ups only, no code. Base: origin/dev `35a0b7f2`. Author: coder2, from the
person's direction relayed by master on 2026-11-17 (task `views-redesign-spec`). It supersedes the
`ideas-triage-and-plan-spec` request given to coder. It builds on the requests-plan redesign, whose
§9 (decided 2026-11-10) is built: the Work tab, the Kanban and the Projects counts
([2026-11-09-requests-plan-redesign-mockups.md](2026-11-09-requests-plan-redesign-mockups.md)).
The mock-ups go to the person first; the questions in §8 come before any code.

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
- **An alert column only when something is wrong**, never a column of blanks:
  - `never delivered`: a tracked request still not delivered to its target (no `output_written`)
    after the delivery threshold;
  - `body lost`: the request's body is no longer in the stream (`availability: missing`);
  - `no reply 3h`: delivered or accepted, no response past `board.no_reply_after`, a new setting
    (default 2 h; 0 = off; Display group in `C`).
  
  Rows with an alert come first, oldest alert first, then the others by age. The column exists
  only while at least one row has an alert.
- **Enter opens the row's diagnostics**, today's Requests detail for that thread: the delivery
  cursor, the pending list, the request's facts, the thread's entries. The between-agents line of
  Work opens this Board, filtered to the rows between agents, in place of today's sub-view.
- **busmon never writes the bus here.** The row's actions are words in the footer, each naming who
  acts:
  - `resend`: the dispatcher's (it already re-wakes; a stuck delivery is its alarm);
  - `cancel`, `reassign`: master's (a directive to master, prefilled in Compose for the person to
    send);
  - missions flag their own stuck requests (the conductor).
  - **If master is dead** (no fresh status, no terminal): busmon offers the same act through the
    dispatcher, after a confirmation line, and writes one Activity entry saying what was done
    and by whom. This is the only path where busmon asks the dispatcher to act.
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

- **A roadmap, not a copy of For you:** sections **NOW**, **NEXT**, **APPROVED** and **LATER**, then
  DONE folded to a count (`d`).
  - NOW: in progress and in review.
  - APPROVED: both reviewers approved, waiting for delivery or the merge (out of NOW, its own
    section).
  - NEXT: not started, unblocked, by priority then file order.
  - LATER: blocked by another task (`waits on <id>`), then the low-priority tasks.
- **No `person · waiting for you` column.** A task the person owns shows `♙` before its owner; what
  the person must do now is Work's, not repeated here.
- **Dependencies** show as `after <id>` on the task's line (the first one, `+N` for more); the side
  panel (wide tier) lists all of them and the tasks that wait on this one.
- **Priority**: one glyph set, used in Plan and the Kanban: `▲` high, nothing for normal, `▼` low.
  `+` and `-` on a plan task raise or lower it (the person's, as `request priority`). NOW and NEXT
  sort by priority, then file order.
  - Plan tasks gain an optional `priority` field (`high`, `normal`, `low`; absent = normal) in the
    plan file; the three `LOW:` titles become `priority: low` and lose the prefix.
  - **The dispatcher hands out the highest unblocked task first**: master's `request next` already
    orders tracked requests by priority; a plan task's priority is copied to the request master
    sends for it.

### Mock-up: Plan at 160 columns

```
╭─ 4 Plan · v0.29 ─────────────────────────────────────────────────────────────────────── 28/43 done ▕████████████░░░░░░▏ ─╮╭─ quota-step-3 ───────────────────╮
│ NOW                                                                                                                      ││ owner   coder                    │
│ ● agent-screen-blocked       coder     working    after holder-g1                                                        ││ state   queued 3h                │
│ ◐ ask-refuses-person         coder2    review     foureyes 12m · reviewer-codex 12m                                      ││ after   quota-settings-step2 ✓   │
│ APPROVED                                                                                                                 ││ waits   quota-step-4 on it       │
│ ✓ human-not-an-agent-test    coder2    approved   deliver                                                                ││ prio    ▲ high (+ -)             │
│ NEXT                                                                                                                     ││ request quota-step-3 queued      │
│ ▲ quota-step-3               coder     queued     after quota-settings-step2                                             ││                                  │
│   holder-g1-part2            coder     queued                                                                            ││                                  │
│   decide-compose-undo        ♙ person  decide                                                                            ││                                  │
│ LATER                                                                                                                    ││                                  │
│   quota-step-4               coder     blocked    after quota-step-3                                                     ││                                  │
│ ▼ master-conversation        coder     low        after decide-master-conversation                                       ││                                  │
│ ▼ agent-led-setup            coder     low                                                                               ││                                  │
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
│ ▲ quota-step-3             coder     queued    after quota-settings-step2    │
│   holder-g1-part2          coder     queued                                  │
│   decide-compose-undo      ♙ person  decide                                  │
│ LATER 3 (l) · DONE 28 (d)                                                    │
╰─ + - priority · enter task ──────────────────────────────────────────────────╯
```

At 80 columns LATER folds to a count, as DONE; `l` unfolds it.

## 5. IDEAS: triage from the card

The ideas are notes in `docs/memory/open-questions/` (ideas-as-notes, `docs/memory/IDEAS.md`). On an
idea's card (Work's IDEAS row or the Kanban's Ideas card):

- `y` **yes**: master adds a plan task for it and queues it; the note becomes `resolved`, linking the
  task.
- `l` **later**: nothing moves; the note stays `proposed`, and the card shows `later` with the date.
- `n` **no**: the note becomes `superseded` with the person's "no" and the date, and leaves IDEAS.

busmon never edits the repository: each key sends master a directive (`idea <id>: yes|later|no`),
and master (or docs) writes the note in a PR. The card shows `sent` until the note changes.

This needs busmon to list the notes, which it does not do today: IDEAS shows the broker's ideas
(`agentbus idea`), and `memory.OpenQuestions` has no caller. Wiring it is step 1 (§7).

## 6. Fixes

- **"Nothing expected from you." while decisions wait.** The For you panel shows the person's plan
  tasks as one pointer, not as rows (§9, Q5), so with only plan decisions waiting its body is
  empty and reads "Nothing expected from you." while the label says `10 decide`. Fix: with no
  row and decisions waiting, the panel reads `10 decisions in the plan (3 Work)`, attention role;
  "Nothing expected" only when the strip counts nothing.
- **Kanban For you cards repeat `decide · you`.** Every card's first line is
  `<glyph> decide · you · 2h`: "you" is true of the whole column, and the glyph already says the
  verb. Fix: the first line is the glyph and the title; the second is the age and the place
  (`2h · #t8`). A merge card keeps `merge PR N` as its title.

## 7. Steps (after the person's answers)

1. IDEAS reads the notes (`memory.OpenQuestions` wired into the Work model, beside the broker's
   ideas), and the stale comment in `work_model.go` corrected.
2. The two fixes of §6.
3. Board and Requests merged (§3), the `board.no_reply_after` setting.
4. Plan as tab 4, the roadmap sections, `priority` in the plan file and the glyphs (§4).
5. IDEAS triage keys (§5).

## 8. Questions for the person

1. **Board alerts**: are the three alerts right (never delivered, body lost, no reply), and is
   2 hours the right default for "no reply"?
2. **Master dead**: may busmon ask the dispatcher to resend or cancel after a confirmation, when
   master has no fresh status? Or should busmon only say so?
3. **Plan sections**: NOW, APPROVED, NEXT, LATER, DONE: is APPROVED its own section as drawn, or a
   mark inside NOW?
4. **Priority glyphs**: `▲` high, `▼` low, nothing for normal: or `+`/`-` as text?
5. **80 columns**: LATER folded to a count on the Plan, as drawn, or listed?
6. **IDEAS keys**: `y` yes, `l` later, `n` no: `y` copies the note's path today on Work; move the copy
   to `c`?
