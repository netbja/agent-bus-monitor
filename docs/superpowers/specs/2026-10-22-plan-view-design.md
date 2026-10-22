# A plan view in busmon — design

Status: draft for review (coder, 2026-10-22). Task `plan-view-spec`, branch `docs/plan-view-spec`
from `origin/main` `2929854`. Spec only; no code in this change.

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
- **The facts of progress already exist**: the board (`{p}:board`: owner, state, request), the
  verdict ledger (`{p}:verdicts`, by subject `pr:<n>` or a free subject), asks (`{p}:asks`), pane
  observation. What is missing is the list that ties them together, and the PR numbers.

## Options

| | Option | Verdict |
|---|---|---|
| A | Reuse missions (`docs/changes/<c>/missions.json`) for every plan | No: it forces `allowed_paths`/`test` on a spec or a review task and hands execution to the conductor; the team does not work that way today |
| B | A plan file in the repository, published to Redis at a commit (`agentbus plan load`), its progress **derived** from board, verdicts and asks, plus a few facts master records | **Recommended** |
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
    {"id": "busmon-ux",  "title": "busmon UX items 5-8", "owner": "coder", "board": "busmon-ux-impl"},
    {"id": "roles",      "title": "Specialist roles",    "owner": "coder", "board": "roles-impl",
     "after": ["busmon-ux"]},
    {"id": "reload",     "title": "Client reload",       "owner": "coder", "board": "reload-impl"},
    {"id": "release",    "title": "Tag and install",     "owner": "person", "after": ["roles", "reload"]}
  ]
}
```

- `id`, `title` (one line), `owner` (an agent name, or `person` for the person's own steps),
  `board` (the board task it follows, optional), `after` (ids it waits for, optional),
  `mission` (`<change>/<id>`, optional: a task run by the conductor, below).
- At most 50 tasks; names follow `ValidName`; `after` must name tasks of the file, without cycles.
- **Who writes it: master**, in its own worktree, on a branch `plan/<name>`, committed. A plan is
  reviewed like any document when it merges with the batch's docs; it need not merge to be shown.

### Publishing: `agentbus plan …` (writes Redis only, never the file)

| Command | Writes |
|---|---|
| `agentbus plan load <path>@<commit>` | `{p}:plan` (the parsed file, its path, commit, Redis `TIME` of the load). Replaces the active plan; one active plan per project |
| `agentbus plan link <task> --pr <n>` | `{p}:plan:facts` field `<task>`: the PR number |
| `agentbus plan merged <task>` | the same record: `merged` with Redis `TIME`, written when the person says they merged (master learns it from the person or `gh pr view`; busmon never calls GitHub) |
| `agentbus plan note <task> <text>` | a one-line note shown with the task (≤ 200 characters) |
| `agentbus plan show [--json]` / `plan clear` | read / delete both keys |

Both keys are in `projectKinds`' sense ordinary project keys, deleted with the project. Facts for a
task id not in the loaded plan are refused.

### States: derived, never typed

One pure function, `plan.Derive(task, facts, board, verdicts, asks, plan) State`, used by
`agentbus plan show` and busmon, first match wins:

| State | When |
|---|---|
| **done** | `merged` recorded, or its board task is `done` and it has no PR, or (owner `person`) its board task is done |
| **waiting for you** | an open ask from the task's owner or from master names the task id in its title or context; or its PR is approved (`verdicts --subject pr:<n>` APPROVED) and not merged: the merge is the person's |
| **blocked** | its board request is `blocked` (the reason is shown) |
| **in review** | a PR is linked and not approved; or its board state is `review`; or an open challenge gates its owner with this task's ref |
| **in progress** | its board request is `accepted` (or the board state is `working`) |
| **upcoming** | none of the above; `waiting on <ids>` while an `after` task is not done |

A task with `mission` derives from `{p}:missions:<change>` instead (states of `mission/state.go`):
done = `integrated`; in review = `in_review`, `approved` or `integrating`; in progress =
`requested`, `accepted`, `delivered` or `checking`; blocked = `blocked`; upcoming = `pending` or
`ready`; `aborted` reads done with the note "aborted".

**Missions stay separate, linked.** A plan may list a mission as one task (or a change as one
task); the conductor keeps running it. Merging the two formats would weigh every plan task with
execution fields, or weaken missions' validation.

### busmon

- **Header**: `plan v0.22 3/9` (done/total, `RoleMuted`), and `♙ 1` in the plan part when tasks
  wait for the person (`RoleAttention`, counted with the existing attention, never rung twice).
  Absent when no plan is loaded.
- **`P` opens the Plan overlay** (the modal layer, like the requests and recap overlays), in every
  tier: the goal and the plan's commit and age on the first line, then the tasks grouped by state,
  in this order: waiting for you, blocked, in review, in progress, upcoming, done (collapsed to a
  count, `Enter` on it expands). One row per task: state glyph, id, title (`Fit`), owner, PR
  (`#102`), verdict (`approved`, `rejected`, `pending`), age of its last fact.
  - Glyphs and roles from `theme.go` only: waiting `GlyphHuman` `RoleAttention`; blocked
    `GlyphBlocked` `RoleError`; in review `GlyphDotEmpty` `RoleDispatch`; in progress
    `GlyphWorking` `RoleAccent`; upcoming `GlyphIdle` `RoleMuted`; done `GlyphDone` `RoleDone`.
    The glyph differs per state, so the 16-colour and no-colour palettes stay readable.
  - Keys (from `keys.go`, so footer and help follow): `j/k ↑↓` move, `Enter` opens the task's
    detail (board entry, request thread, verdicts, the ask, the note, the facts), `y` copies the
    plan as text, `Esc` closes. Narrow tier: owner, PR and verdict fold under the title.
- **Recap (`R`)**: a "Plan" section after "Expected from you": the tasks that changed state since
  the last visit (`from → to`), and the plan's tasks waiting for the person in "Expected from you".
  States are computed now from current facts; a transition is shown only when the recap's retained
  history proves it (a fact's time after the last visit), never guessed.

### Staleness

- The plan line shows its load age: `plan v0.22 · loaded 3d ago (a1b2c3d)`; past 7 days, `RoleMuted`
  with `stale` and the hint "master: agentbus plan load".
- A task **in progress** whose newest fact (board update, request event, verdict, ask) is older
  than 24 h shows `quiet 2d`. A hint, never a state change.
- The sentinel's daily review mentions a stale plan to master in one line; it never edits it.

### Master's part (agent-bus-master skill, new section "The plan")

- When a batch is agreed with the person, write `docs/plans/<name>.json` in a worktree on
  `plan/<name>`, commit it, `agentbus plan load …@<commit>`.
- Keep it true: add or drop a task with a new commit and `plan load`; `plan link --pr` when opening
  a PR; `plan merged` when the person says they merged; questions to the person name the task id.
- It replaces the "status of the batch" paragraphs in reports: the person reads the plan.

## Tests

- `plan.Parse`: valid file; unknown field; unknown `after`; a cycle; too many tasks; invalid names.
- `plan.Derive` table: each state's rule, and the order when several apply (an approved, unmerged
  PR whose board request is also blocked reads **waiting for you**, the first match); `after`
  waiting; the mission branch; missing facts read as upcoming, never as done.
- `agentbus plan load` from a commit (test repository, as the mission load tests do); `link`,
  `merged`, `note` refuse an unknown task; on the test broker only.
- busmon: header item with and without a plan; the overlay's grouping and order; `TestAlignmentSuite`
  fixtures with a plan in every state at every size and palette; the recap section from a fixture
  history (transition shown only with a proving fact); the stale and quiet hints.
- Skills test: master's "The plan" section names `agentbus plan load` and `plan merged`.

## Open questions for review

1. One active plan per project, or several (a batch per release)? (Proposal: one; a new batch is a
   new file loaded over it, the old file stays in Git.)
2. `merged` recorded by master, or derived from `gh pr view` run by `agentbus plan sync` on the
   host? (Proposal: recorded; `plan sync` later if master forgets too often.)
3. JSON or TOML for the file? (Proposal: JSON, like `missions.json`, parsed strictly; TOML is
   easier to write by hand but master writes it, not the person.)
