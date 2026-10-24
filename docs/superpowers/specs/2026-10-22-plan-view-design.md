# A plan view in busmon — design

Status: draft for review (coder, 2026-10-22), revision 2 after foureyes' review of `d67ba1b`.
Task `plan-view-spec`, branch `docs/plan-view-spec` from `origin/main` `2929854`. Spec only; no
code in this change.

## The problem

Bernard cannot see the plan or its progress. The plan lives in master's conversation: which batch
is under way, which tasks it has, what is in review, what waits for him. A new master session, a
context compaction or a quota hold loses it, and busmon shows only its fragments (board tasks,
Activity, asks), never the whole.

## Facts that shape the design

- **busmon reads Redis only**, and may run on a laptop through the SSH tunnel (README, deployment
  topology): it cannot read a file of the repository. A plan must be *published* to Redis.
- **Missions already load a versioned file from Git at a commit** (`mission load
  <path>@<commit>`, `git show`): a file can be versioned without being merged first.
- **Missions are an execution engine**: per task `allowed_paths`, a `test`, a reviewer, the
  conductor checks scope and tests and merges into `change/<c>` (`mission/model.go`). The work this
  team actually does is different: master sends tracked requests, coder delivers a branch,
  foureyes leaves a verdict, master opens a PR, the person merges.
- **How this team's facts actually read** (the board and ledger of 2026-10-22):
  - a board task turns `done` when its owner runs `request done`, i.e. on **delivery**, before
    review, PR or merge (`reload-impl` and `roles-impl` read `done` while approved and unmerged);
  - foureyes' verdict subjects are the **board task names** (`busmon-ux-impl`, `roles-impl`,
    `reload-impl`), not `pr:<n>`;
  - review gates are opened on the **reviewer**, with review refs, not on the task's owner.
  So no fact on the bus says "merged": that one is recorded, and shown as a claim.
- **The other progress facts already exist**: the board (`{p}:board`: owner, state, request), the
  verdict ledger (`{p}:verdicts`), asks (`{p}:asks`). What is missing is the list that ties them
  together, the PR numbers and the merges.

## Options

| | Option | Verdict |
|---|---|---|
| A | Reuse missions (`docs/changes/<c>/missions.json`) for every plan | No: it forces `allowed_paths`/`test` on a spec or a review task and hands execution to the conductor; the team does not work that way today |
| B | A plan file in the repository, published to Redis at a commit (`agentbus plan load`), its progress **derived** from board, verdicts and asks, plus the few facts master records | **Recommended** |
| C | A plan only in Redis, written by commands | Not versioned, not reviewable, lost with the project's keys; Bernard asked for a file in the repository |
| D | busmon reads GitHub for PR state | busmon must not call out (its only network call is the opt-out update check); and GitHub is reachable from the host, not always from the laptop |

## Recommended design (B)

### The file: `docs/plans/<name>.json`

Strict JSON (unknown fields refused), like `missions.json`; small:

```json
{
  "plan": "v0.22",
  "goal": "busmon UX, specialist roles, client reload",
  "tasks": [
    {"id": "busmon-ux", "title": "busmon UX items 5-8", "owner": "coder", "board": "busmon-ux-impl"},
    {"id": "roles",     "title": "Specialist roles",    "owner": "coder", "board": "roles-impl",
     "after": ["busmon-ux"]},
    {"id": "ux-spec",   "title": "busmon UX spec",      "owner": "coder", "board": "busmon-ux-spec",
     "merge": false},
    {"id": "release",   "title": "Tag and install",     "owner": "person", "after": ["roles"]}
  ]
}
```

- `id`, `title` (one line), `owner` (an agent name, or `person` for the person's own steps).
- `board` (optional): the board task it follows.
- `review` (optional): the verdict subject of its review; **default: its `board` task**, which is
  how this team names subjects. A linked PR adds `pr:<n>` (below).
- `merge` (optional, default `true` for an agent's task, ignored for `person`): `false` for a task
  that ends without a merge of its own (a spec merged later with its batch, a review, an
  investigation).
- `after` (optional): ids it waits for. `mission` (optional, `<change>/<id>`): a task run by the
  conductor (below).
- At most 50 tasks; names follow `ValidName`; `after` must name tasks of the file, without cycles.
- **Who writes it: master**, in its own worktree, on a branch `plan/<name>`, committed. A plan is
  reviewed like any document when it merges with the batch's docs; it need not merge to be shown.

### Publishing: `agentbus plan …` (writes Redis only, never the file)

| Command | Writes |
|---|---|
| `agentbus plan load <path>@<commit>` | `{p}:plan` (the parsed file, its path, commit, Redis `TIME` of the load). Replaces the active plan; one active plan per project |
| `agentbus plan link <task> --pr <n>` | `{p}:plan:facts` field `<task>`: the PR number |
| `agentbus plan done <task>` | the same record: `done` with Redis `TIME`. Master runs it when the person says they merged (a merge task) or did their step (a `person` task); master learns it from the person or `gh pr view`; busmon never calls GitHub |
| `agentbus plan note <task> <text>` | a one-line note shown with the task (≤ 200 characters) |
| `agentbus plan show [--json]` / `plan clear` | read / delete both keys |

Facts for a task id not in the loaded plan are refused.

**Keys.** `{p}:plan` and `{p}:plan:facts` are **not** in `projectKinds`: a plan alone never makes
a project appear in `busmon --list`, and `plan load` is refused for a project that has no key of a
`projectKinds` kind (a mistyped project would otherwise get keys nobody lists). They are deleted
with the project (`busmon --delete` takes every `{p}:*` key). They count in
`HasKeysBesidesDispatch` like any key a person's command writes: the dispatcher never writes them.

### States: derived, never typed

One pure function, `plan.Derive(task, inputs) State`, where `inputs` holds the board, the facts,
the verdicts of the task's subjects, the asks and the missions as already read. No I/O in it.
`agentbus plan show` and busmon both call it. First match wins:

| State | When |
|---|---|
| **done** | `done` recorded (shown as **`merged (recorded 2h ago)`** for a merge task, `done (recorded …)` otherwise: a claim by master, never presented as a GitHub fact); or `merge: false` and its board request is `done` and its review rolls up APPROVED (or it has no verdict at all and no `review` was set); or a `person` task whose board task is done |
| **waiting for you** | an open ask whose structured `task` field is this task's id (below); or a merge task whose review rolls up APPROVED and no `done` is recorded (the merge is the person's); or a `person` task whose `after` tasks are all done |
| **blocked** | its board request is `blocked` (the reason is shown) |
| **in review** | its board request is `done` (delivered), or its board state is `review`, or a PR is linked, and its review does not roll up APPROVED. The row's verdict column says which: `no verdict yet`, `pending`, `changes asked` (REJECTED) |
| **in progress** | its board request is `accepted`, or its board state is `working` (what `board claim` sets) |
| **upcoming** | none of the above; `waiting on <ids>` while an `after` task is not done |
| **`?`** | a read the deciding rule depends on has never succeeded (see "Failed reads") |

**The review roll-up** is `bus.RollUp` over the union of the verdicts on the task's subjects: its
`review` subject (default its `board` task) and `pr:<n>` when a PR is linked, in ledger order. So
APPROVED means the latest independent approve on either subject is newer than any reject on
either: an approval of the branch followed by a reject on the PR reads `changes asked`.

**An ask names its task by a field, never by a substring.** `AskDef` gains an optional, additive
`task` (a `ValidName`; `agentbus ask … --task <id>`), stored with the definition like `title`. An
ask without it is attention as today and belongs to no plan task. A `task` naming no task of the
loaded plan is shown in "Expected from you" as usual and ignored by the plan. Title and context
are never searched.

**Gates are not read.** A 4-eyes gate is opened on the reviewer under a review ref, so it says
nothing reliable about the task's owner; the review state comes from the verdict ledger only.

A task with `mission` derives from `{p}:missions:<change>` instead (states of `mission/state.go`):
done = `integrated`; in review = `in_review`, `approved` or `integrating`; in progress =
`requested`, `accepted`, `delivered` or `checking`; blocked = `blocked`; upcoming = `pending` or
`ready`; `aborted` reads done with the note "aborted".

**Missions stay separate, linked.** A plan may list a mission as one task (or a change as one
task); the conductor keeps running it. Merging the two formats would weigh every plan task with
execution fields, or weaken missions' validation.

### Failed reads

- **busmon** keeps the last good value of every read, as it already does (`pollOnce`, the queue's
  `N?`). A task derived from a kept value shows `?` after its age; a task whose deciding read has
  never succeeded since busmon started reads **`?`**, never `upcoming`. The header count then
  reads `3/9?`.
- **`agentbus plan show`** reads once: a failed read of the board, the facts, the ledger, the asks
  or a mission makes the affected tasks `?` with the error named, and the command exits non-zero.
- Missing data that was read successfully (no board entry, no verdict) is not a failure: it reads
  as the table says, and never as done.

### Where busmon reads

- In `pollOnce`, every `requestPoll` tick (5 s): `GET {p}:plan`, `HGETALL {p}:plan:facts`, the
  verdict ledger **once** (`XREVRANGE` of the newest 1000 entries, filtered locally on the plan's
  subjects; a subject without a verdict in that window reads `no verdict yet`, while `plan show`
  reads the full ledger), and `{p}:missions:<change>` once per distinct change of the plan's
  mission tasks. Nothing of this is read when no plan is loaded (one `GET`).
- Board and asks are already read every tick; the plan reuses those values.
- `Derive` runs in `pollOnce` after the reads and stores the derived rows in `shared`; `Draw`
  only renders them, does no I/O and no derivation.

### busmon

- **Header**: `plan v0.22 3/9` (done/total, `RoleMuted`), and `♙ 1` in the plan part when tasks
  wait for the person (`RoleAttention`, counted with the existing attention, never rung twice).
  Absent when no plan is loaded.
- **`P` opens the Plan overlay** (the modal layer, like the requests and recap overlays), in every
  tier: the goal and the plan's commit and age on the first line, then the tasks grouped by state,
  in this order: waiting for you, blocked, in review, in progress, upcoming, `?`, done (collapsed
  to a count, `Enter` on it expands). One row per task: state glyph, id, title (`Fit`), owner, PR
  (`#102`), verdict (`approved`, `changes asked`, `pending`, `no verdict yet`), age of its last
  fact. A done merge task reads `merged (recorded 2h ago)`.
  - Glyphs and roles from `theme.go` only: waiting `GlyphHuman` `RoleAttention`; blocked
    `GlyphBlocked` `RoleError`; in review `GlyphDotEmpty` `RoleDispatch`; in progress
    `GlyphWorking` `RoleAccent`; upcoming `GlyphIdle` `RoleMuted`; `?` the ASCII `?` `RoleMuted`;
    done `GlyphDone` `RoleDone`. The glyph differs per state, so the 16-colour and no-colour
    palettes stay readable.
  - Keys (from `keys.go`, so footer and help follow): `j/k ↑↓` move, `Enter` opens the task's
    detail (board entry, request thread, verdicts per subject, the ask, the note, the facts), `y`
    copies the plan as text, `Esc` closes. Narrow tier: owner, PR and verdict fold under the title.
- **Recap (`R`)**: a "Plan" section after "Expected from you": the tasks that changed state since
  the last visit (`from → to`), and the plan's tasks waiting for the person in "Expected from you".
  States are computed now from current facts; a transition is shown only when the recap's retained
  history proves it (a fact's time after the last visit), never guessed.

### Staleness

- The plan line shows its load age: `plan v0.22 · loaded 3d ago (a1b2c3d)`; past 7 days, `RoleMuted`
  with `stale` and the hint "master: agentbus plan load".
- A recorded `done` always shows its age (`merged (recorded 2h ago)`), so a merge master forgot to
  record reads as a task stuck waiting for you, and a wrong record reads as a dated claim.
- A task **in progress** whose newest fact (board update, request event, verdict, ask) is older
  than 24 h shows `quiet 2d`. A hint, never a state change.
- The sentinel's daily review mentions a stale plan to master in one line; it never edits it.

### Master's part (agent-bus-master skill, new section "The plan")

- When a batch is agreed with the person, write `docs/plans/<name>.json` in a worktree on
  `plan/<name>`, commit it, `agentbus plan load …@<commit>`.
- Keep it true: add or drop a task with a new commit and `plan load`; `plan link --pr` when opening
  a PR; `plan done` when the person says they merged or did their step; an ask about a task carries
  `--task <id>`.
- It replaces the "status of the batch" paragraphs in reports: the person reads the plan.

## Tests

- `plan.Parse`: valid file; unknown field; unknown `after`; a cycle; too many tasks; invalid names;
  `merge` on a `person` task ignored.
- `plan.Derive` table, from the team's real cases:
  - board request `done`, no verdict, merge task → **in review** (`no verdict yet`), never done;
  - verdict APPROVED on the board-task subject, no PR, no `done` recorded → **waiting for you**;
  - the same with `done` recorded → **done**, labelled as recorded;
  - approve on the board-task subject then reject on `pr:<n>` → **in review**, `changes asked`;
  - `merge: false`, request done, approved → **done**;
  - an open ask with `task` = the id → **waiting for you**; an ask whose title or context contains
    the id but whose `task` is empty → no effect;
  - the order when several apply: an approved, unrecorded merge task whose board request is also
    `blocked` reads **waiting for you** (first match);
  - `after` waiting; a `person` task whose `after` are done → waiting for you;
  - the mission branch for every state of `mission.AllStates`;
  - a failed (never successful) board read → `?`, never upcoming or done.
- `agentbus plan load` from a commit (test repository, as the mission load tests do); refused for a
  project with no `projectKinds` key; `link`, `done`, `note` refuse an unknown task; `plan show`
  exits non-zero and marks `?` on a failed read; on the test broker only.
- `bus`: `AskDef.Task` round-trips and is validated; a plan load does not make `bus.Projects` list
  the project.
- busmon: `pollOnce` reads the plan keys only every `requestPoll` tick and nothing else when no
  plan is loaded (the `busReader` fake counts calls); a failed read keeps the last rows and marks
  them `?`; header item with and without a plan; the overlay's grouping and order;
  `TestAlignmentSuite` fixtures with a plan in every state at every size and palette; the recap
  section from a fixture history (transition shown only with a proving fact); the stale, recorded
  and quiet hints.
- Skills test: master's "The plan" section names `agentbus plan load`, `plan done` and `--task`.

## Open questions for review

1. One active plan per project. (Agreed in review: one; a new batch is a new file loaded over it,
   the old file stays in Git.)
2. `done` recorded by master, shown as a dated claim. (Agreed in review; `agentbus plan sync` via
   `gh` on the host later, if master forgets too often.)
3. JSON, parsed strictly. (Agreed in review.)

Changes from revision 1 (foureyes, review of `d67ba1b`): done needs a recorded merge (or
`merge: false`), a delivered task is in review; the review reads the board-task subject plus
`pr:<n>`; asks name a task by a structured `task` field; gates are not read; failed reads show `?`;
reads and cadence placed in `pollOnce`, `Derive` pure; `merged` shown as a recorded claim; the plan
keys stay out of `projectKinds`; `plan merged` became `plan done` (it also closes `person` tasks).

## Implementation notes (plan-view-impl)

Where the implementation departs from the text above, and why:

- **`merge: false` needs an approval.** The "no verdict at all and no `review` set" case cannot be
  told apart from an unreviewed task, since the review subject defaults to the board task: a
  delivered `merge: false` task is done only once approved, or when master runs `plan done`.
- **A task with a verdict and no board entry reads in review** (after the in-progress rule), so a
  task reviewed outside a tracked request (a fix with its own subject) is not `upcoming`.
- **The recap lists `<task> → <state now>`, without the earlier state.** busmon does not store
  plan states between visits, so "from" cannot be proven; the change itself is listed only when
  a fact (board, request, verdict, ask or a recorded fact) is newer than the last visit.
- **A mission task has no `board` and is not the person's**; the plan name may hold dots
  (`v0.22`: `^[a-z0-9][a-z0-9._-]{0,31}$`), task ids and owners follow `ValidName`.
- **The verdict ledger is read incrementally** (foureyes' note): `VerdictsAfter` from the last
  seen id, at most 4 pages of 500 per tick; busmon keeps only the roll-up fields.
- **`?` per row** follows the inputs a row depends on: board, facts, verdicts and asks for a
  board task, missions for a mission task.
