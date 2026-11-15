# Plan view by chantier

Status: draft r1, 2026-11-15. Author: architect. Asked by the person through master (task
plan-view-by-chantier); keep it small: simple, fast, reliable.

## 1. Problem

busmon's Plan board (Plan tab, `p`/`P` page; `cmd/busmon/planboard.go`) splits a plan's tasks into
NOW, NEXT, LATER and DONE (DONE folded to a count). v0.29 has 41 tasks from about six unrelated
efforts: herdr exit, self-repair, the Work tab, decisions, the person's own steps. NOW mixes them, so
the person cannot see how far one effort is, and a step that is approved and only waits for a merge
reads as not done like any other.

Wanted: tasks grouped by **chantier** (an effort, an epic), one header per chantier with a
`done / total` bar and its step in hand first, finished rows folded, a short note per row with the
detail in the task.

## 2. Recommendation: an explicit field, not the graph

**An explicit `group` per task, with the chantiers' names listed once in the plan.** Derived from
the dependency graph (`after`), the groups would be wrong in three ways:

- a graph has no names: "Leave herdr" must be written somewhere anyway;
- a task with no `after` (every Decide, every person step, a lone fix) becomes its own group of one:
  v0.29 would show about 20 groups;
- one cross-effort dependency merges two efforts (`self-repair-7` waits for `roles-required-lines`;
  a herdr step could wait for a self-repair one).

Master writes the plan file already; one word per task is cheap and reviewed with it.

### 2.1 Schema (`plan/plan.go`)

```json
{
  "plan": "v0.29",
  "goal": "…",
  "groups": [
    {"id": "herdr-exit", "title": "Leave herdr"},
    {"id": "self-repair", "title": "Self-repair watcher"}
  ],
  "tasks": [
    {"id": "herdr-9-dispatcher", "title": "…", "owner": "coder", "group": "herdr-exit", "after": ["…"]}
  ]
}
```

- `File.Groups []Group` (`id`, `title`), optional; `Task.Group string`, optional.
- Validate: at most 12 groups; a group id is a `bus.ValidName`, unique; a title is one line of 1 to
  40 characters; a task's `group` names a listed group. Groups listed and no task in one: refused
  (a typo would hide it).
- A plan with **no `groups`** is drawn exactly as today: nothing changes for older plans.

### 2.2 Compatibility

`plan.Parse` refuses unknown fields, in `agentbus plan load` and in busmon alike. So an older
`agentbus` refuses to load a grouped plan (a safe guard), and an older busmon fails to parse a
grouped plan another `agentbus` loaded. Master adds `groups` to a plan file only once the release
carrying this is installed; the operator note fragment says so.

## 3. The board with groups

When the plan has groups, the board's body is one block per group instead of NOW, NEXT and LATER;
the plan's progress line and its head line above stay as they are.

```
Leave herdr         ▰▰▰▰▰▰▰▰▱▱  8/10 · in progress: herdr-9-dispatcher · waits for you: herdr-last-release-tag
  ● herdr exit 9: the dispatcher on termhost and agentstate…   coder   in review
  ◆ Tag the current release as the last with herdr…            ♙ person waiting
  ○ herdr exit 10: cutover; every caller on Host…              coder   after 2
Self-repair watcher ▰▰▰▰▰▰▰▰▰▱  9/10 · in progress: self-repair-10
  …
Other               ▰▰▱▱▱▱▱▱▱▱  2/9
  …
DONE  29   d shows
```

- **Header** per group: the title, a bar of thin cells (the plan bar's glyphs and width rule),
  `done/total`, then, when present, `in progress: <id>` (the group's task in hand by
  `planInHand`'s rule, applied to its rows) and `waits for you: <id>` (its first row in `Waiting`).
  Too wide: `waits for you` stays, `in progress` goes first (the person's action wins).
- **The bar shows "nearly done"**: cells for `Done` in the done colour, then cells for rows
  `InReview` whose verdict is `Approved` (approved, waiting for a merge) in the in-review colour,
  then the rest muted. The count stays honest: `done/total` counts `Done` only, so 8/10 never claims
  a merge that has not happened.
- **Rows** under the header: the group's rows that are not done, in today's order (NOW states by
  `plan.Order`, then NEXT, then LATER), drawn by `planBoardRow` unchanged. The row's details already
  end with `Note` (`planRowDetails`); it stays one clipped line, and the side panel and Enter keep
  the full text: "detail in the task".
- **Finished rows folded**: a group's done rows are not drawn; its count is in the header. One
  `DONE  N   d shows` line at the end, as today; `d` shows every done row under its group.
  A group with every task done is one header line (`▰▰▰ 10/10 · done`), drawn after the open groups.
- **Order of groups**: a group with a row waiting for the person first, then one with a task in
  hand, then the rest, each in the `groups` list order; tasks with no group form `Other`, last
  before the finished groups.
- **Selection** stays per task row (headers are not items), so `j`/`k`, Enter, `s` and the side
  panel behave as today.
- **Narrow tier**: the header is `Leave herdr 8/10`, no bar.
- **Unknown reads** (`unknownOnly`): today's single muted list, no groups (a failed read must not
  look like progress).

## 4. Other readers

Nothing else changes: the idle view's next frame, the recap, `plan-drift` and `deliver` read rows
and tasks, not groups. `planInHand` for the whole plan is unchanged.

## 5. v0.29, proposed groups (master's edit of `docs/plans/v0.29.json`)

| Group | Title | Tasks |
|---|---|---|
| `herdr-exit` | Leave herdr | herdr-exit-spec, herdr-1 … herdr-10, herdr-security-review, herdr-last-release-tag |
| `self-repair` | Self-repair watcher | self-repair-1 … self-repair-10, roles-required-lines |
| `work-tab` | Work tab | requests-plan-4, kanban-separators, work-columns-wrap, work-stable-order |
| `low` | Later, low priority | master-conversation, agent-led-setup, default-view |
| `decide` | Decisions for the person | decide-*, idea-* |
| `person` | The person's steps | discord-webhook, github-token-auto-merge, old-board-cleanup |

`foureyes-callers` stays in `Other`.

## 6. Tests

- Parse: groups accepted; unknown group on a task, duplicate id, a 41-character title, 13 groups,
  an empty listed group each refused; a plan without groups parses as today.
- Board, golden lines: a grouped plan draws headers with `done/total`, the approved-in-review cells
  in the bar, `in progress` and `waits for you`; the narrow header; `waits for you` kept when the
  header is too wide; done rows folded and shown under their group with `d`; a finished group is one
  line after the open ones; ungrouped tasks under `Other`; an ungrouped plan's board is
  byte-identical to today's (golden); `unknownOnly` draws no groups.
- Selection: item indexes skip headers; Enter on a row opens the same task as today.

## 7. Out of scope

- Groups across plans, nested groups, a group's own state or owner, editing groups in busmon.
- Inferring groups from the graph or from titles.
- A per-row short title: the row keeps the task's title, clipped as today.
