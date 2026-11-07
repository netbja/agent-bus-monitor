# busmon's home as an inbox, the plan in a drawer (home-inbox-drawer)

Status: spec, for review (subject `home-inbox-drawer`). Not built. Nothing is implemented before
the person's go. busmon only: no bus key, no stream field, no new Redis read, no write.

The person's complaint (2026-11-07 brief): busmon is hard to use, and For you in particular is
disturbing, because **plan tasks stay there permanently**. Four directions were drawn as mock-ups
(the HTML page and the agents' notes of 2026-11-07). The person chose **direction 1, "Inbox +
drawer"**:

- For you becomes an inbox that can empty;
- the plan opens in a drawer (`p`) or in tab 4;
- the Plan tab and the Requests tab are redrawn.

It builds on the queue-triage spec (`2026-11-06-busmon-queue-triage-design.md`, merged), whose
three steps are approved and await their merge. §8 says how the two compose.

## 1. The cause, in the code

`planItems` (`tea_foryou.go`) adds one For you row (`rankPlan`) for **every** task the plan
derives as `plan.Waiting` with `Owner == plan.Person`. In `plan.Derive` (`plan/derive.go`), a
person task that is not a mission task, not done (no `done` board entry, no recorded completion)
and not answered (`answeredAsk`) falls through to `Waiting` as soon as its dependencies are done
(`len(waitingOn) == 0`), whether or not the person can act on it today. Several such tasks sit in For you for the life of a batch. That is
the permanent panel. They also count in the `for you` badge (the strip's `decide` chip after
queue-triage), in the frame label and in the reminder (`decisions`, `dueAttention`).

## 2. What changes

| Surface | Today | After |
|---|---|---|
| For you | asks, stall rows, waits, prompts, gates, plan tasks, merges, unread lines | asks, stall rows, waits, prompts, gates, merges; at most **one** plan pointer row |
| unread lines | rows of For you | the strip's `unread` chip, `u` and the walk (`T`) reach them; no For you row (§3.2) |
| the plan | tab 4, `P` (a page) | tab 4 redrawn (NOW · NEXT · LATER · DONE, progress, a side panel); `p` opens the same view as a drawer |
| Requests (tab 3) | seven sections | three: **Owed by you**, **You wait on**, **Between agents**; age on each row; one action per row |

## 3. For you: an inbox that can empty

### 3.1 The rows it keeps

`forYou` keeps its ranks `rankAsk`, `rankHealth`, `rankWait`, `rankGate`, `rankMerge` and
`rankUnread` unchanged: it stays the one derivation (busmon Charm spec, decision 2) that the strip,
the walk, the reminder and the recap read. What changes is **what it is given** (§3.3) and
**what the panel draws** (§3.2).

### 3.2 Unread lines leave the panel, not the model

The For you panel draws `forYou` **minus `rankUnread`**: `forYouPanelItems(items)`. An unread
line is not something to decide. It is reached three ways, all already there:

- the strip's `unread` chip, which counts `unreadItems(forYou)` as today, and a click on it;
- `u` (`ActNextUnread`), the next unread line in Activity;
- the walk (`T`), which still walks `rankUnread` rows last (queue-triage §4.1).

An ask whose line is unread keeps its mark on its own row (`it.Unread`), as today.

The frame label follows the strip: `2 decide · 1 merge` (each part at 0 left out), in place of
`4 for you · 2 unread`. The For you count and the strip can then never disagree (foureyes' note on
busmon-queue-strip).

### 3.3 Plan tasks leave For you; one pointer stays

`planItems` is removed: no `rankPlan` row any more, so `rankPlan` leaves `decisions`, the
`decide` chip, the reminder and the walk.

One **pointer row** may stay, drawn at the bottom of the panel, below the decisions:

```
  p  plan: next for you  "write v0.29 notes"
```

- **Which task.** `nextPlanTask(planState)`: the first task in the plan file's order whose row is
  `Owner == plan.Person` and `State == plan.Waiting` (what `planStartable` already accepts). With
  none, the first person task `Blocked`, then `Upcoming`, as `forYouEmptyRows`' "Next for you"
  line chooses today. With none at all, or no plan loaded, no pointer.
- **Not a decision.** The pointer is muted. It is not counted in the `decide` chip, the frame
  label, the reminder or the walk's `N/M`, and it never rings. The header's `planSeg` keeps
  `plan v0.29 6/11 ♙ 2`: what waits for the person in the plan (every `Waiting` row, its merges
  and open asks included, `plan.go:199-201`) is counted there, beside the plan it belongs to.
- **Its key** is `plan-next:<task>`. Enter opens the task page (`ovPlanTask`, with its Action
  row, `s` starts it with master), as `empty:plan:` does today. `rowHint` says `task`.
- **One producer.** The empty state's "Next for you" line and the pointer are the same row, from
  `nextPlanTask`. Empty For you reads `Nothing expected from you.` (or the owed-requests line), then
  the pointer. `empty:plan:` is renamed `plan-next:`.

The earlier decision that a plan task waiting for the person is "a decision like any other,
counted in the title and the badge" (`planItems`' comment: "0 for you under a Waiting for you line
read as a contradiction") is **superseded** by the person's choice here. The contradiction cannot
come back: no "Waiting for you" line remains in For you, and the plan's own `♙ N` sits in the
header's plan item.

## 4. The plan: one view, a tab and a drawer

### 4.1 One producer

`planBoard(p planState, sel int, showDone bool, now time.Time, width int) planBoardView` gives the
rows, their keys and the side panel's lines. Tab 4 and the drawer both draw it. `planOverlay`
(the `P` page) and `planTabItems` give way to it.

### 4.2 Sections

| Section | Rows (`plan.State`) | Order |
|---|---|---|
| **NOW** | `Waiting`, `Answered`, `Blocked`, `InReview`, `InProgress` | `plan.Order` |
| **NEXT** (ready to start) | `Upcoming` with `WaitingOn` empty | file order |
| **LATER** | `Upcoming` with `WaitingOn` not empty, and `Unknown` beside known rows | file order |
| **DONE** | `Done` | folded to one line, `DONE  6   d shows` |

**A failed read.** When the facts, board, verdicts or asks read fails, `plan.Derive` makes every
task `Unknown` (`derive.go:111-118`), except a mission task (derived on its own) and, only when
the facts read itself succeeded, a task recorded done (`planState.partial` is set too). LATER would then hold nearly the whole plan, so when every row
not `Done` is `Unknown` the board draws no NOW, NEXT or LATER: one muted line `a read failed:
states unknown`, then those tasks in file order with the `Unknown` glyph, then DONE as usual.
`Unknown` goes to LATER only beside known rows.

Each row: the state's glyph (`planLook`, unchanged), the title, the owner, and the state's word
or reason (`in review`, `merge PR 338 (you)`, `waits on c1`). A section with no row is not drawn.

Above the sections, the progress: `v0.29  6/11 done`, then a bar of `done/total` (thin bar cells,
the same glyphs as the gauges, never animated), `55%`.

### 4.3 The side panel

At the wide tier, the selected task's facts sit in a frame beside the list:

```
 state    in review
 owner    coder
 review   approved
 reason   merge PR 338
 waits on —
 for you  nothing            (or: "start it: enter, then s")

 enter task page
```

It reads only the `plan.Row` already derived (`State`, `Task.Owner`, `Verdict`, `Reason`, `PR`,
`WaitingOn`). `Verdict` is one roll-up label (`pending`, `approved`…), not one line per reviewer:
the reviewers' names would need the verdicts read (`Inputs.Verdicts`), which this panel does not
do. The task page keeps them, as today. Below the wide tier there is no side panel: Enter opens the task page as today.

### 4.4 Tab 4 and the drawer

- **Tab 4** draws `planBoard` in the lower zone, as the Plan tab does today. The `toggle` row goes:
  `d` shows or folds DONE.
- **The drawer** is `p` on the main screen (`ActPlanDrawer`, ModeShell). It is a reader page
  (`ovPlan`, flagged `drawer`) drawing the same `planBoard`. Esc closes it.

  | Tier | The drawer's frame |
  |---|---|
  | wide (≥ 120) | right of For you, from the top panels' row to the footer: `Rect{fw, top.Y, w-fw, footer.Y-top.Y}`. For you stays visible on the left; Agents and the lower tab are not drawn under it |
  | medium (80–119) | the reader's frame (the lower zone), as every page |
  | narrow (< 80) | the reader's frame (the view), as every page |

  At the wide tier For you stays drawn, but the keyboard is the drawer's (ModeOverlay, as every
  page), and a click on For you closes the drawer first, as a click outside any page does today
  (`click` → `closeReader`), then lands on For you.

  A page opened from the drawer (Enter: the task page) is drawn in the drawer's frame and Esc
  returns to the drawer. `readerRect` reads the bottom page's `drawer` flag for the whole stack.
- **`P`** stays, as an alias of `p`: one way to the plan, two keys.

## 5. Requests: three sections

### 5.1 Sections

| Section | From today's | Row kind word |
|---|---|---|
| **Owed by you** | the `secOwed` groups (the `asks` key has no producer: only `openRequestRow` names it) | as today |
| **You wait on** | `secWait` | as today |
| **Between agents** | `secTracked`, `secReviews`, `secDirectives`, then the notices line (`secNotices`) | `request`, `challenge`, `directive`, `notices` |

The folded threads (`secFolded`, `o`) stay as they are. `reqSection` keeps its values: only
`reqSectionNames` and the headings change. Three headings are drawn in place of five, each with
its count: `Owed by you  2`. Within "Between agents", the order stays tracked, reviews,
directives, notices. The mock-ups wrote "Owed to you", which inverts the section: `secOwed` holds
the threads whose sender waits for the person's answer (`requests_view.go:22`, `:427`; the empty
For you says `Owed by you: N (Requests)`). Today's heading, "Owed by you", is kept (foureyes).

### 5.2 Age and the action

- **Age** is on every row already (`reqLayout.row`, the oldest thread of a group). Unchanged.
- **One action per row**: Enter's word, from `rowHint` (busmon-row-actions), in the footer. No
  second hint line under the selected row: it would move every row below when the selection
  moves (the strip's fixed-place rule, queue-triage §3.1). See §11 Q3.

The mock-up's `a` (accept as the person), `n` (nudge) and `f` (filter by agent) would write to the
bus or need a new filter. They are out of scope (§10).

## 6. Tiers

| Screen | wide (≥ 120) | medium (80–119) | narrow (< 80) |
|---|---|---|---|
| home | strip; For you (left, decisions and the pointer) beside Agents; the lower tab | as today: For you's four rows when unfocused, then `+N more` | the view as today; the strip compressed (`♙2 ◆1 ●3`) |
| plan (tab 4) | sections, progress, side panel | sections, progress; no side panel | sections; the progress line only (`6/11 55%`) |
| plan (drawer) | right of For you (§4.4) | the reader's frame | the reader's frame |
| Requests | three sections, full columns (`reqLayoutFor`) | same, columns as today | same, columns as today |

`TestShellAlignmentSuite` and the golden sizes (`goldenSizes`) cover all three.

## 7. Keys (keys.go, the one table)

| Key | Mode, panel | Action | Footer |
|---|---|---|---|
| `p` | ModeShell | `ActPlanDrawer`: open the drawer | `p plan`, after `T triage` |
| `P` | ModeShell | the same action (alias) | not shown |
| `d` | ModeShell, `PanelPlan` | `ActShowDone`: show or fold DONE | `d done` |
| `d` | ModeOverlay, the plan page or drawer | the same | `d done` |
| Esc | the drawer | close (Esc only: `p` inside the drawer is ModeOverlay's `ActPrevItem`) | as every page |

`p` is free in ModeShell today (`n`/`p` are bound in ModeOverlay only). `d` on `PanelPlan` does not
collide with `ActDelete`, which is bound to Agents and Requests only. `TestNoKeyCollisionPerMode`
holds it. F1 lists `p`, `P` and `d` from the table. The tour's "Useful keys" page gains `p` "the
plan, in a drawer"; its welcome page's For you line becomes "what needs you: questions, waits,
merges". `tourVersion` is bumped.

## 8. How it composes with queue-triage

| queue-triage | After this spec |
|---|---|
| strip `decide` = ranks `rankAsk`…`rankPlan` | `rankPlan` no longer exists: `decide` = asks, stall rows, waits, gates. `decide + merge = decisions` still holds |
| strip `unread` = `unreadItems(forYou)` | unchanged: the model keeps `rankUnread` (§3.2) |
| chip click "first row of a decide rank" | unchanged; an unread chip click is `u`, as before |
| walk table: `rankPlan` → task page; terminal act "the Action row's message to master" | the row is gone, so is its terminal act. The walk never visits the pointer |
| walk end page: "the next decision" row (`empty:plan:`) | the pointer's row (`plan-next:`), same producer |
| `rowHint`: `plan:` → `task`, `empty:plan:` → `task`, Plan `toggle` → `expand` | `plan-next:` → `task`; `plan:` and `toggle` leave the table (no producer) |
| `TestRowHintEveryRowAndItsOutcome` | its fixtures lose the `plan:` row and the toggle, and gain `plan-next:` |

The walk still reaches unread lines. With them gone from the panel, the walk and `u` are the
two ways to read them in order.

## 9. Tests

- **Membership.** `forYou` over a plan with three person tasks `Waiting`: no `rankPlan` item;
  `decisions` counts none of them; `nextPlanTask` is the first in file order; with none `Waiting`,
  the first `Blocked`, then `Upcoming`; none, or no plan: no pointer.
- **The panel.** The For you list has no `unread:` row and ends with the `plan-next:` row; its
  frame label equals the strip's words (`decide`, `merge`) for the same items.
- **The pointer is not a decision.** The reminder (`dueAttention(decisions(...))`) and the walk
  never include it; Enter on it opens `ovPlanTask` with that task.
- **Plan sections.** One fixture per state lands in its section (NOW, NEXT, LATER, DONE), with
  `Upcoming` split by `WaitingOn`; `d` folds and shows DONE on the tab and in the drawer.
- **The drawer.** `p` opens it; at the wide tier its frame is right of For you and For you is
  still drawn; Enter opens the task page in the same frame; Esc returns, Esc closes; at medium and
  narrow it is the reader's frame. `P` does the same.
- **Side panel.** At the wide tier its lines are the selected row's fields; it follows `j`/`k`;
  there is no side panel below the wide tier.
- **Requests.** The three headings, in order, each with its count; tracked, reviews and
  directives rows under "Between agents"; folded threads unchanged.
- **Keys.** `TestNoKeyCollisionPerMode`, `TestTourKeysExist`; the footer of the main screen holds
  `enter`, `T`, `p`, F1, F5 at the narrow width before any other key (the keep rule of
  queue-triage §4.1, widened to `p`).
- **Every golden is regenerated once per step, and only where the step means it** (below).

## 10. Steps, each its own subject for 4-eyes

1. **For you membership** (`home-inbox`): §3 and §8's walk and `rowHint` changes. Goldens: For
   you's rows and label, the empty state, the walk's end page.
2. **The plan board** (`plan-board`): §4, tab 4, the drawer and its geometry, `p`/`P`/`d`, the side
   panel. Goldens: the Plan tab, the `P` page, a new drawer golden per tier.
3. **Requests sections** (`requests-sections`): §5. Goldens: the Requests tab.

Each step leaves busmon whole, after the three queue-triage steps are merged.

**Out of scope**: the kanban home (direction 2) and the focus-mode home (direction 4); the
Requests view grouped by agent (direction 3); accepting a request as the person, nudging, a filter
by agent (they write to the bus or need a filter of their own); moving plan tasks by hand.

## 11. Open questions for the person

Each has a recommendation; the person's go on this spec decides them.

1. **Unread lines in For you.** Recommended: they leave the panel (§3.2) and are reached by the
   chip, `u` and `T`. The direction-1 mock-up still drew one (`● msg codex: APPROVE…`); master's
   directive lists what For you keeps without them. Keep them as rows instead?
2. **The pointer in the `decide` count.** Recommended: not counted (it is not something to do
   now). Count it?
3. **A hint line under the selected row** (the mock-up's `enter answer · 1 yes 2 not now`).
   Recommended: no, the footer names Enter's outcome (busmon-row-actions) and a second line would
   move the rows below. Draw it?
4. **The drawer at the wide tier.** Recommended: right of For you, over Agents and the lower tab
   (§4.4). Or the reader's lower frame, as every page?
5. **`s` on the plan view** ("tell master", in the mock-up). `s` is F3's alternate on the main
   screen, so the plan keeps it on the task page only (as today). Agreed?
