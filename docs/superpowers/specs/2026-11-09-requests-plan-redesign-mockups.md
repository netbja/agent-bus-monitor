# Requests and Plan, redesigned: who owes what (mock-ups)

Status: **mock-ups for the person's choice, no code.** Task `requests-plan-redesign` (master,
2026-11-09). Three layouts below, each at 160 and 80 columns; the person picks one (or parts of
several) before anything is built. The figures are invented for the mock-up.

## 1. The problem

The person: "I cannot tell what is expected of whom", and the decisions in waiting (the plan's
`Decide:` tasks), the open asks and the plan's tasks are never in one place. Today they are spread
over four places:

| What | Where it shows today |
|---|---|
| Asks, waits, gates for the person | the **For you** panel (top left), one line each |
| Merges waiting for the person | For you, and the plan's task line (`merge PR N`) |
| Requests and reviews between agents | the **Requests** tab (3): by direction (*You wait on*, *Between agents*), not by who must act |
| The plan's tasks and the progress line | the **Plan** tab (4): NOW / NEXT / LATER / DONE, and the header's progress line |
| `Decide:` tasks (ideas to decide) | the Plan tab, as ordinary NOW/LATER rows owned by `♙ person` |
| What each agent is doing | the **Agents** panel's message column (free text) |
| Ideas recorded in docs | nowhere in busmon (`docs/memory/open-questions/`, specs' questions) |

The common question, "who must act next, on what", has no page of its own.

## 2. The blocks every layout shows

- **For you**: everything that waits on the person. An open ask (answer), an approved PR (merge),
  a `Decide:` plan task (choose), and a gate that concerns the person. Each row says **why** it
  waits on the person.
- **In progress, by whom**: each agent at work, its task in hand (the request it accepted, linked
  to its plan task), and the **progress line** (`started 14:12 ━━━━━━──── ~60% · ends ~15:20`,
  PR 384's). Reviews in flight show the reviewer and how long the gate has been open.
- **Next**: the plan's next tasks, then the requests not in the plan, each with its owner.
- **Ideas not decided**: the `Decide:` tasks and the open questions recorded in the repository
  (`docs/memory/open-questions/`). A specs' unanswered questions could join later.
- **Folded lines**: the traffic between agents that waits on nobody, and what is done.

## 3. Layout A: one "Work" page, by who owes what (recommended)

One tab replaces Requests (3) and Plan (4): **YOU**, then the **TEAM** (one row per agent, with
its progress line), then **NEXT**, then **IDEAS**. The question "what is expected of whom" is the
page's order.

```
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 3 decide · 2 merge                                                                   15:04:05 
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
```

At 80 columns the same sections, with the progress line on its own row:

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 3 decide · 2 merge   15:04 
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
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 3 decide · 2 merge                                                                   15:04:05 
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
 ←→ column  ↑↓ item  enter open  m merge  a answer   ·   between agents 20 (b)   done 14 (d)                                                                    
```

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 3 decide · 2 merge   15:04 
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
 ←→ column  ↑↓ item  enter open  b agents  d done                               
```

**Replaces**: the Requests and Plan tabs, by one tab, Board. The For you panel becomes the board's
first column, and the top-left panel gives its room to Agents.

- **For it**: the four answers are at one glance, and it reads well on a wide screen.
- **Against it**: titles are cut at 38 cells, at 80 columns each column shows about 3 rows, and
  moving between columns adds a key (`←→`).

## 5. Layout C: the plan as the spine

The Plan tab grows. Each task carries its requests, reviews and PRs under it (a small tree), and
what waits on the person is marked at the right of its row (`◆ merge: you`, `♙ decide: you`). Work
the plan does not name gets its own section, then ideas. The Requests tab becomes **Log**: the raw
traffic between agents.

```
 agent-bus-monitor   ● bus ok   ● dispatch up   ● missions up   ♙ 3 decide · 2 merge                                                                   15:04:05 
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
```

```
 agent-bus-monitor  ● bus  ● dispatch  ● missions  ♙ 3 decide · 2 merge   15:04 
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
4. **Keys**: in A, `m` merges an approved PR from its row and `a` answers an ask. Or keep
   today's way, opening the reader and acting there?
