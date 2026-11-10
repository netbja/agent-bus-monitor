# Requests and Plan, redesigned: who owes what (mock-ups)

Status: **mock-ups for the person's choice, no code.** Task `requests-plan-redesign` (master,
2026-11-09). Three layouts below, each at 160 and 80 columns; the person picks one (or parts of
several) before anything is built. The figures are invented for the mock-up.

## 1. The problem

The person: "I cannot tell what is expected of whom", and the decisions in waiting (the plan's
tasks owned by the person, titled `Decide: …` by habit), the open asks and the plan's tasks are
not on one page. Each is shown today, but in a different place:

| What | Where it shows today |
|---|---|
| Asks, waits, gates for the person | the **For you** panel (top left), one line each |
| Merges waiting for the person | For you, and the plan's task line (`merge PR N`) |
| Requests and reviews | the **Requests** tab (3), in sections by direction: *Owed by you*, *You wait on*, *Between agents*; For you's empty state names the *Owed by you* count |
| The plan's tasks and the progress line | the **Plan** tab (4): NOW / NEXT / LATER / DONE, and the header's progress line |
| The plan's tasks owned by the person (`Decide: …` is only a title habit; the code reads the owner, `♙ person`) | **not** For you rows since the person's choice of 2026-11-07 (home-inbox-drawer, `nextPlanTask`): counted in the header's plan item (`♙ N`), with one muted pointer in For you, `Next for you: <task>`, and every one listed as a row of the Plan tab |
| What each agent is doing | the **Agents** panel's message column (free text) |
| Ideas recorded in docs | nowhere in busmon (`docs/memory/open-questions/`, specs' questions) |

The common question, "who must act next, on what", has no page of its own: the person's plan tasks
are a count and a pointer, the asks and merges are rows of For you, the requests owed are a section
of another tab.

**Keys in every layout** (master, 2026-11-10): the footer shows the request-priority keys
`+ -` (raise or lower a tracked request's priority, request-priority §6), as today's Requests
footer does; they act on the rows that are tracked requests.

## 2. The blocks every layout shows

- **For you** (a section of the new page, not the For you panel): everything that waits on the
  person. An open ask (answer), an approved PR (merge), a plan task owned by the person (choose),
  and a gate that concerns the person. Each row says **why** it waits on the person. The For you
  **panel** keeps the rule of 2026-11-07 in layouts A and C (plan tasks a count and one pointer, not
  rows); the page lists them as rows, which is what the person now asks for. Part 8, Q5.
- **In progress, by whom**: each agent at work, its task in hand (the request it accepted, linked
  to its plan task), and the **progress line** (`started 14:12 ━━━━━━──── ~60% · ends ~15:20`,
  PR 384's). Reviews in flight show the reviewer and how long the gate has been open.
- **Next**: the plan's next tasks, then the requests not in the plan, each with its owner.
- **Ideas not decided**: the person's plan tasks not yet due, and the open questions recorded in the repository
  (`docs/memory/open-questions/`). A specs' unanswered questions could join later.
- **Folded lines**: the traffic between agents that waits on nobody, and what is done.

## 3. Layout A: one "Work" page, by who owes what (recommended)

One tab replaces Requests (3) and Plan (4): **YOU**, then the **TEAM** (one row per agent, with
its progress line), then **NEXT**, then **IDEAS**. The question "what is expected of whom" is the
page's order.

```
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 1 decide · 2 merge                                                                   15:04:05 
 plan v0.29  6 of 11 done  ━━━━━━━━━━━━──────────  55%  ·  now: compose-at-file (coder)  ·  next: requests-plan                                                 
╭─ Work   (3) who owes what ──────────────────────────────────────────────────────────────────────────────────────── 5 for you · 4 at work · 3 next · 2 ideas ─╮
│  YOU  5                                                                                    why it waits on you                                               │
│  ♙ decide  master     review lot 1? (2 questions)                                     4m   an ask: answer in the reader (enter)                              │
│  ◆ merge   PR 391     idle-breathing · approved by reviewer-codex and foureyes        2m   both approved the same head: merge it                             │
│  ◆ merge   PR 392     compose-at-file step 1 · approved                               1m   both approved the same head: merge it                             │
│  ♙ idea    Decide:    self-repair watcher (8 questions, spec PR 389)                  2h   a plan task of yours: choose, then master plans it                │
│  ♙ idea    Decide:    notifications order                                             1d   a plan task of yours                                              │
│                                                                                                                                                              │
│  TEAM  4 at work · 2 idle                                                                  task in hand                          review                      │
│  ● coder           compose-at-file step 2 (trigger, insert)   plan #t7                     started 14:12  ━━━━━━──── ~60%  ·  ends ~15:20   —                │
│  ● foureyes        reviewing compose-at-file-home             for coder                    gate open 12m                         —                           │
│  ● reviewer-codex  reviewing compose-at-file-home             for coder                    gate open 12m                         —                           │
│  ● master          pushing PR 391                                                          started 15:01  no estimate yet       —                            │
│  ○ sentinel        idle   ·   ○ ui-kimi  idle                                                                                                                │
│                                                                                                                                                              │
│  NEXT  (the plan, then requests not in it)                                                                                                                   │
│  ○ requests-plan redesign      coder        plan #t8 · after compose-at-file                                                                                 │
│  ○ v0.29 tag                   ♙ you        plan #t11 · waits on t7, t8, t9                                                                                  │
│  ○ kimi-quota follow-up        coder        request · requested 2h · not in the plan                                                                         │
│                                                                                                                                                              │
│  IDEAS  not decided yet                                            where it lives                                                                            │
│  ◇ ~ in compose (@~/)          addendum in review (2 questions for you)   docs/superpowers/specs/2026-11-09-…home-addendum.md                                │
│  ◇ DeepSeek super-agent ideas  3 ideas, waiting for you                   docs/memory/open-questions/2026-11-03-…md                                          │
│                                                                                                                                                              │
│  ── between agents · 9 reviews, 11 directives, nobody waits on you  (enter shows them) ──                                                                    │
│  ── done · 14 this week  (d shows them) ──                                                                                                                   │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter open  + - priority  a answer  ·  between agents and done: enter on their line                                                                 
```

At 80 columns the same sections, with the progress line on its own row:

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 1 decide · 2 merge   15:04 
 v0.29  6/11  ━━━━━━──── 55%  ·  now: compose-at-file (coder)                   
╭─ Work  (3) ────────────────────────────────────────────────── 5 · 4 · 3 · 2 ─╮
│  YOU  5                                                                      │
│  ♙ decide master  review lot 1? (2 questions)            4m                  │
│  ◆ merge  PR 391  idle-breathing · approved             2m                   │
│  ◆ merge  PR 392  compose-at-file 1 · approved          1m                   │
│  ♙ idea   self-repair watcher (8 questions)             2h                   │
│  ♙ idea   notifications order                           1d                   │
│  TEAM  4 at work · 2 idle                                                    │
│  ● coder     compose-at-file 2 · t7                                          │
│      started 14:12  ━━━━━━──── ~60%  ·  ends ~15:20                          │
│  ● foureyes  reviewing compose-at-file-home  12m                             │
│  ● r-codex   reviewing compose-at-file-home  12m                             │
│  ● master    pushing PR 391                                                  │
│  NEXT  3                                                                     │
│  ○ requests-plan redesign   coder   t8                                       │
│  ○ v0.29 tag                ♙ you   t11 · waits on 3                         │
│  ○ kimi-quota follow-up     coder   not in the plan                          │
│  IDEAS  2                                                                    │
│  ◇ ~ in compose (@~/)  addendum in review                                    │
│  ◇ DeepSeek ideas      3, waiting for you                                    │
│  ── between agents 20 · done 14  (enter) ──                                  │
╰──────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter open  + - priority  a answer                                  
```

**Replaces**:
- the **Requests** tab (3) and the **Plan** tab (4), merged into tab 3, Work;
- the For you panel keeps its place as the short summary, and enter on it jumps to Work;
- the Requests tab's full list stays reachable as the folded `between agents` line (enter
  opens it), and so does the plan's DONE (`d`).

- **For it**: one page answers the question; the person reads it top-down and stops at YOU.
- **Against it**: a long plan pushes IDEAS below the fold on a short screen. It scrolls, like
  today's tabs.

## 4. Layout B: a board of four columns

**For you · In progress · Next · Ideas**, side by side at 160 columns. Below 120 columns they
become a 2×2 grid, each column cut to its first rows (enter opens a column full-screen).

```
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 1 decide · 2 merge                                                                   15:04:05 
 plan v0.29  6 of 11 done  ━━━━━━━━━━━━──────────  55%  ·  now: compose-at-file (coder)  ·  next: requests-plan                                                 
╭─ For you  5 ─────────────────────────╮╭─ In progress  4 ─────────────────────╮╭─ Next  3 ────────────────────────────╮╭─ Ideas  2 ───────────────────────────╮
│♙ decide · master          4m         ││● coder · compose-at-file 2           ││○ requests-plan redesign              ││◇ ~ in compose (@~/)                  │
│  review lot 1? (2 q)                 ││  t7 · started 14:12                  ││  coder · t8                          ││  addendum in review                  │
│◆ merge · PR 391          2m          ││  ━━━━━━──── ~60%                     ││○ v0.29 tag                           ││  2 questions for you                 │
│  idle-breathing, approved            ││  ends ~15:20                         ││  ♙ you · waits on 3                  ││◇ DeepSeek super-agent                │
│◆ merge · PR 392          1m          ││● foureyes · reviewing                ││○ kimi-quota follow-up                ││  3 ideas, waiting                    │
│  compose-at-file 1                   ││  compose-at-file-home 12m            ││  coder · not in the plan             ││                                      │
│♙ idea · self-repair      2h          ││● reviewer-codex · reviewing          ││                                      ││                                      │
│  8 questions, PR 389                 ││  compose-at-file-home 12m            ││                                      ││                                      │
│♙ idea · notif. order     1d          ││● master · pushing PR 391             ││                                      ││                                      │
│                                      ││  no estimate yet                     ││                                      ││                                      │
│                                      ││○ sentinel · ○ ui-kimi idle           ││                                      ││                                      │
│                                      ││                                      ││                                      ││                                      │
│                                      ││                                      ││                                      ││                                      │
│                                      ││                                      ││                                      ││                                      │
╰──────────────────────────────────────╯╰──────────────────────────────────────╯╰──────────────────────────────────────╯╰──────────────────────────────────────╯
 ←→ column  ↑↓ item  enter open  + - priority  a answer   ·   between agents 20 (b)   done 14 (d)                                                               
```

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 1 decide · 2 merge   15:04 
 v0.29  6/11  ━━━━━━──── 55%  ·  now: compose-at-file (coder)                   
╭─ For you  5 ─────────────────────────╮╭─ In progress  4 ─────────────────────╮
│♙ decide master  4m                   ││● coder  compose 2                    │
│◆ merge PR 391   2m                   ││  ━━━━━━──── ~60%                     │
│◆ merge PR 392   1m                   ││● foureyes  review                    │
│♙ idea self-repair                    ││● r-codex   review                    │
│♙ idea notif. order                   ││● master  push 391                    │
╰──────────────────────────────────────╯╰──────────────────────────────────────╯
╭─ Next  3 ────────────────────────────╮╭─ Ideas  2 ───────────────────────────╮
│○ requests-plan  t8                   ││◇ ~ in compose                        │
│○ v0.29 tag  ♙ you                    ││◇ DeepSeek ideas                      │
│○ kimi-quota (req)                    ││                                      │
╰──────────────────────────────────────╯╰──────────────────────────────────────╯
 ←→ column  ↑↓ item  enter  + - priority  b agents  d done                      
```

**Replaces**: the Requests and Plan tabs, by one tab, Board. The For you panel becomes the board's
first column, and the top-left panel gives its room to Agents. That **reverses the choice of
2026-11-07**: the person's plan tasks would be For you rows again (part 8, Q5).

- **For it**: the four answers are at one glance, and it reads well on a wide screen.
- **Against it**: titles are cut at 38 cells, at 80 columns each column shows about 3 rows, and
  moving between columns adds a key (`←→`).

## 5. Layout C: the plan as the spine

The Plan tab grows. Each task carries its requests, reviews and PRs under it (a small tree), and
what waits on the person is marked at the right of its row (`◆ merge: you`, `♙ decide: you`). Work
the plan does not name gets its own section, then ideas. The Requests tab becomes **Log**: the raw
traffic between agents.

```
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 1 decide · 2 merge                                                                   15:04:05 
 plan v0.29  6 of 11 done  ━━━━━━━━━━━━──────────  55%  ·  now: compose-at-file (coder)  ·  next: requests-plan                                                 
╭─ Plan  v0.29   6 of 11 done   ━━━━━━━━━━━━──────────  55% ────────────────────────────────────────────────────────────── you: 2 merge · 2 decide · 1 answer ─╮
│  NOW                                                                                                                                                         │
│  ● t7  compose-at-file                         coder        started 14:12  ━━━━━━──── ~60%  ·  ends ~15:20                                                   │
│        ├ step 1  PR 392  approved by both                     ◆ merge: you                                                                                   │
│        ├ step 2  request accepted 14:12                       in hand                                                                                        │
│        └ ~ addendum   compose-at-file-home  in review          foureyes · reviewer-codex, 12m                                                                │
│  ◆ t6  idle-breathing                          coder        PR 391 approved by both            ◆ merge: you                                                  │
│  ♙ t5  Decide: self-repair watcher             ♙ you        spec PR 389 merged, 8 questions    ♙ decide: you                                                 │
│                                                                                                                                                              │
│  NEXT                                                                                                                                                        │
│  ○ t8  requests-plan redesign                  coder        after t7                                                                                         │
│  ○ t11 v0.29 tag                               ♙ you        waits on t7, t8, t9                                                                              │
│                                                                                                                                                              │
│  NOT IN THE PLAN  (work the plan does not name)                                                                                                              │
│  ♙ ask      master → you   review lot 1? (2 questions)                     4m   ♙ answer: you                                                                │
│  ○ request  master → coder kimi-quota follow-up · requested                 2h                                                                               │
│                                                                                                                                                              │
│  IDEAS  not decided                                                                                                                                          │
│  ◇ DeepSeek super-agent ideas (3)        ◇ notifications order                                                                                               │
│                                                                                                                                                              │
│  DONE  6   d shows      ·      messages between agents: the Log tab (3)                                                                                      │
╰──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter open  + - priority  d done  ·  messages between agents: the Log tab                                                                           
```

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 1 decide · 2 merge   15:04 
 v0.29  6/11  ━━━━━━──── 55%  ·  now: compose-at-file (coder)                   
╭─ Plan v0.29  6/11  55% ───────────────────────────────────────────── you: 5 ─╮
│  NOW                                                                         │
│  ● t7 compose-at-file           coder                                        │
│       started 14:12  ━━━━━━──── ~60%  ends ~15:20                            │
│       ├ step 1  PR 392 approved    ◆ merge: you                              │
│       ├ step 2  in hand                                                      │
│       └ ~ addendum  in review 12m                                            │
│  ◆ t6 idle-breathing  PR 391      ◆ merge: you                               │
│  ♙ t5 Decide: self-repair          ♙ decide: you                             │
│  NEXT                                                                        │
│  ○ t8 requests-plan redesign    coder                                        │
│  ○ t11 v0.29 tag                ♙ you · waits on 3                           │
│  NOT IN THE PLAN                                                             │
│  ♙ ask master: review lot 1?     ♙ answer: you                               │
│  ○ req kimi-quota follow-up      coder                                       │
│  IDEAS  ◇ DeepSeek (3)  ◇ notif. order                                       │
│  DONE 6 (d)  ·  messages: Log tab (3)                                        │
╰──────────────────────────────────────────────────────────────────────────────╯
 ↑↓ select  enter  + - priority  d done                                         
```

**Replaces**:
- the Plan tab (4) is extended;
- the Requests tab (3) is renamed Log and keeps only the traffic between agents;
- For you stays as it is.

- **For it**: it keeps the plan as the person's map, and every request finds its place under a
  task.
- **Against it**: without a loaded plan most of the page is "not in the plan"; the person's items
  are spread along the tree (marked, not grouped).

## 6. The Projects page (M)

All three layouts can add the same two numbers to each row of the Projects page: **for you**
(the count above) and **at work** (agents with a task in hand). The person then sees which project
needs them without opening it:

```
  project            state     for you   at work   plan            services
◂ agent-bus-monitor  live      5         4         v0.29 6/11      dispatch · missions
  demo               live      0         1         —               dispatch
  myproject          quiet     2         0         sec8 3/9        —
```

## 7. Recommendation

**Layout A.** It is the person's own question turned into the page's order: what waits on me,
who does what, what comes next, what is still an idea. It works at 80 columns without cutting
titles, and it keeps For you as the summary the person already knows. C is the better choice only
if every piece of work goes through a loaded plan, which today it does not.

## 8. Questions for the person

1. **Which layout**: A (recommended), B or C, or parts of several?
2. **Ideas from the repository**: show `docs/memory/open-questions/` notes in IDEAS (read-only,
   one line each), or the plan's `Decide:` tasks only?
3. **The Projects page counts** (part 6): yes or no?
4. **Keys**: in A, `a` answers an ask from its row (the reader's answer, opened in place). A merge
   key would be **new behaviour**. busmon merges nothing today, and under the forge-delivery
   decision the person keeps the merge: master pushes the reviewed head, and `deliver` never
   merges. So `m` would run **your own** merge from busmon (the forge's CLI, for example
   `gh pr merge`), refused unless the PR's head is the one both reviewers approved. Add `m` on
   that basis, or keep today's way, merging on the forge yourself?
5. **The person's plan tasks as rows**: since 2026-11-07 they are a header count and one pointer
   in For you, not rows. The new page's YOU section lists them as rows (layouts A and C), and the
   For you panel keeps the pointer; layout B makes them For you rows again. Is that what you want
   now?

## 9. The person's answer (2026-11-10) and what is built

The person's answer:
- **Layout**: A below 130 columns of busmon's pane, the Kanban B at 130 or more. A setting in
  Shift+C chooses `auto` (the default), always A, or always Kanban.
- **Corrections**: the eight listed in master's directive `requests-plan-redesign-impl`.
- **Q2, Q3 and Q5**: yes, as proposed.
- **Q4** (the `m` key): deferred until after v0.29.

The work is built in steps, one PR each.

**Step 1, the Work model and one counter** (`cmd/busmon/work_model.go`). One producer,
`workOf`, feeds the header's strip, the For you panel's label and the idle view now. The Work
page and the Kanban will read it too.

- **The sort rule** (correction 2):
  - **YOU** holds what waits on the person:
    - **answer**: an open ask, a waiting agent or its client's prompt, or a request or
      directive owed by the person;
    - **merge**: a change both reviewers approved on its pushed head, from the merge queue,
      which lists no other head (correction 4 holds by construction);
    - **decide**: a plan task of the person's that nothing blocks;
    - **review**: a gate that concerns the person;
    - **check**: the stall guard.
  - **NEXT** holds the plan's tasks not started, in file order, the person's blocked tasks
    among them, then the tracked requests that no plan task names. A request the person owes
    is in YOU only, never in NEXT; when a plan task names it, the request's answer row stands
    for that task, which is then not also a decide or NEXT row. The sections never list one
    thing twice.
  - **IDEAS** holds the open questions in `docs/memory/open-questions/` with status
    `proposed` or `active` that no plan task covers. A task covers a note when its board or
    its id is the note's id, or when its title without `Decide:` is the note's title. The
    notes are read, never written, from the project template's `working_dir` on busmon's
    host. A failed read keeps the last list and is flagged.
  - **TEAM** holds the agents at work: the task in hand (the accepted tracked request and its
    plan task), the gates the agent holds as a reviewer, and the reviewers of its own work,
    each with the gate's age (correction 5). The idle agents go on one line.
- **The verbs** (correction 1): For you's rows use the same words. An ask reads `answer`, never
  `ask`; a waiting agent reads `answer`; a gate reads `review`; the stall guard reads `check`.
  A merge row keeps its text, `merge PR N`.
- **One counter** (correction 3): the strip's `decide` is every YOU row except the merges, so
  decide + merge is YOU's count, and the For you label shows the same words. The person's
  unblocked plan tasks are now counted there. The For you panel still shows them as one
  pointer, not as rows (Q5). The header's separate `N requests waiting` item is gone: those
  requests are in `decide`.

**Step 2, the Work tab (layout A)** (`cmd/busmon/work_view.go`, `work_actions.go`). Tab 3 is
**Work**, in place of the Requests and Plan tabs. The tab bar reads `1 Activity 2 Board 3 Work`.

- **Keys:**
  - `4` is an alias: it opens Work on NEXT's first row (correction 8).
  - At the narrow tier, `5` is Work and `6` is the same alias.
- **YOU:** each row carries its verb, who, the text, the age and, at 100 columns and more,
  "why it waits on you". Enter opens what For you's row would.
- **TEAM:**
  - Only the agents at work: working, or holding a task or a review gate. Each row shows the
    task in hand and its plan task, then the progress line under it.
  - It shows the gate the agent holds as a reviewer, and who reviews its own work, with the
    gate's age (correction 5). A gate whose ref is not a stream id shows no age.
  - The free agents (idle or done) go on one line, at every width (correction 6). A waiting,
    blocked or held agent is in neither, even when it holds a task or a gate: the Agents panel
    says which.
  - An agent with both a task in hand and a gate shows its task on its row and the gate on a
    line under it.
- **NEXT and IDEAS:** each row says where it comes from (`plan #t8 · waits on t7`,
  `request · not in the plan`). Enter on an idea says where it lives, and `y` copies the path.
- **The folded lines:**
  - **between agents** opens today's Requests list as Work's sub-view
    (`Work → between agents`); Esc returns to Work.
  - **done** (and `d` on Work) opens the plan drawer with DONE shown. With no plan loaded, the
    line says so instead.
- **The frame's label** reads `5 for you · 4 at work · 3 next · 2 ideas`, or with glyphs below
  100 columns: `♙5 ●4 ○3 ◇2` (correction 6).
- **`+` and `-`** act on a row that stands for a tracked request (a request row, or a plan task
  whose board names one). On any other row the footer says "only a tracked request has a
  priority" (correction 7).
- **The decide chip:** a click on it selects For you's first decide row, else Work's first
  non-merge YOU row. The chip counts both (foureyes, step 1).

**Step 3, the Kanban (layout B)** (`cmd/busmon/work_kanban.go`). The same Work model, drawn as
four columns: **For you · In progress · Next · Ideas**.

- **When:**
  - `display.work_layout` is a Display setting (`C`, `config/keys.go`): `auto` (the default),
    `list` or `kanban`.
  - `auto` draws the Kanban when busmon's own width is 130 columns or more. That width is the
    herdr pane's, since busmon runs in it, not the terminal's.
- **At 130 columns** (checked): the lower frame's inside is 128 cells, so each column is 31 cells
  with one blank column between them.
  - Too narrow for four columns of at least 24 cells, the columns stack two per band, then one,
    and the bands share the height. That is what `kanban` draws on a narrow screen.
- **A card** is two lines:
  - The first line holds the mark, the verb or the owner, and the age.
  - The second holds the title, indented two cells and cut with `…` at the column's width,
    which leaves 29 cells at 130 columns. A card never wraps to a third line.
  - Each column scrolls on its own, keeping the selected card in view, and ends with `+N more`
    when the cards do not fit.
- **Folded lines:** the free agents are the last card of In progress. The between-agents and
  done lines are the last cards of Ideas, and Enter opens them as on the list.
- **Keys:**
  - On a selected card, `←→` move between columns, to the same rank among their cards (else the
    last), and the footer reads `←→ column`.
  - Past the first or last column, or with no card selected, `←→` step the lower panel's tabs as
    everywhere else, so the arrows never trap the person in Work.
  - `↑↓` walk the cards in reading order.
  - Enter, `y`, `+ -` and a click (select, then open) act as on the list's rows, by the same
    keys.
- **For you panel:** it keeps its place and its pointer. Part 4's idea of making it the board's
  first column, which would reverse 2026-11-07, is not built: the person's Q5 kept the pointer.
