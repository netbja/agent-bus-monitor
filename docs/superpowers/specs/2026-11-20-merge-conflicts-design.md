# Fewer merge conflicts between parallel agent PRs

Status: spec r4, for review (subject `merge-conflicts`; r4, the person via master (cmd `1795248111715-0`): §5 Q3 states the owner rule first, and §3's measure also counts breaches of the four rules, enforced in `agentbus plan` if any; file sets read with `--no-renames` so a rename lists its old path too (reviewer-codex); #638 is 57 files, and a plan `group` is only a hint for siblings (foureyes); r2: the open #668 counted apart, totals out of 17, the fetch for reproduction; r3: the collided-with column recomputed by §1's method for #489, #494, #495, #499, #500; reviewer-codex). Not built. Base: origin/dev `07c52081`.
Author: architect, from the person's open question of 2026-11-20 (thread `1795205976983-0`),
relayed by master (cmd `1795206037042-0`). The constraint is the person's: no new external tool
or service. Go in agentbus or plain git, on any forge or none.

## 1. The facts: 17 conflicts, 4 causes, and one open now

**How they were counted.** A PR closed unmerged with a "superseded / rebased" comment is a
conflict the team paid for in full: a new PR, often a new review. For each one,
`git merge-tree --write-tree --name-only <base tip at close> <PR head>` names the conflicting
files. `git log <merge-base>..<base tip> -- <file>` names the merge that collided with it. The
window is 2026-11-09 to 2026-11-20 (346 PRs opened since 2026-11-06, 324 merged). To reproduce
it, fetch each head first: `git fetch origin pull/<n>/head:refs/remotes/pr/<n>`.

**One open PR is listed apart, not counted:** #668 does not meet the rule (it is open), but the
forge reports it `CONFLICTING` today, and `merge-tree` against `origin/dev` `07c52081` names its
files. It is shown below the line as a current example; every total in this spec is out of 17.

| PR | Conflicting files | Collided with | Cause |
|---|---|---|---|
| #376 | `docs/operator-notes.md` | another PR's note | A: hot file |
| #474 | `bus/keykinds.go` | #473 (ideas 1) | A: hot file (registry) |
| #480 | `AGENTS.md`, `docs/memory/INDEX.md` | pop-retire-auto 5 | A: hot file (index and its copy) |
| #489 | `cmd/agentbus/configcmd_test.go` | #488 (slash palette 1b) | A: hot file (the row count) |
| #500 | `configcmd_test.go`, `config/config_test.go`, `config/keys.go` | #498 (herdr 6c1, #489 rebased) | A: hot file (the row count, the key list) |
| #495 | `configcmd_test.go`, `settings_page.go`, `settings_ui.go`, `config/group.go`, operator notes | #488; the notes #491, #492 | A: hot file (four maps per key) |
| #494 | `cmd/busmon/tea_model.go`, operator notes | #488 (`tea_model.go`); #491, #492 (the notes) | A + C |
| #499 | `cmd/busmon/testdata/overlay/help.txt` | #496 (slash palette class C) | A: generated golden |
| #400 | `cmd/busmon/projects_admin.go` | projects-admin steps 6 and 7 | B: sibling steps |
| #448 | `dispatch/herdr.go` | herdr exit step 1 | B: sibling steps |
| #451 | `cmd/busmon/settings_ui.go`, its test | self-repair step 9's own first part | B: sibling steps |
| #434 | `cmd/busmon/tea_workkanban_test.go` | foureyes' Kanban test fix | C: one file, two changes |
| #526 | `dispatch/loop.go` | login-updates 2.1, herdr exit step 9 | C: one file, two changes |
| #639 | `cmd/agentbus/mergequeuechore_test.go` | #638 (P8b, a test-fixture move over 57 files) | C: a broad move in flight |
| #656 | `README.md`, `docs/OPERATIONAL-GUIDE.md` | #655 (the README rewrite) | C: a rewrite in flight |
| #591 | `cmd/busmon/personborrowed_test.go` | #590, its own parent, squash-merged | D: stacked |
| #637 | `mergequeue/ci_test.go`, `mergequeue/forge.go` | #636, its own parent (the lib), squash-merged | D: stacked |
| *#668, open, not counted* | `agentstate/background.go`, its test, `dispatch/hostruntime.go`, `internal/dispatchsvc/dispatchconfig.go` | background-work steps 1 and 2 (`6c9b067c`, `07c52081`) | B (`dispatchconfig.go` also moved in #643: C) |

The causes:

- **A, hot files: 8 of 17.** One of them is already fixed. Operator notes became fragments
  (`docs/operator-notes.d/`, 139 files today). Their last conflict was #495 on 2026-11-14.
  - **The settings registry is still open.** `configcmd_test.go:193` still reads
    `len(doc.Rows) != 100`, with a comment that lists every key. Every pair of settings PRs
    collides on that one line. It is step 1 of the
    [hot-files note](../../memory/open-questions/2026-11-14-hot-files.md), proposed on
    2026-11-14 and never built.
  - The other hot files: the key-kind and memory-index registries, and a generated golden.
- **B, sibling steps of one spec built in parallel: 3,** and #668 open now. Steps of one spec touch the same
  functions by construction.
- **C, two changes to one file, or a broad move or rewrite in flight: 4,** plus a share of #494. A move or a rewrite touches everything, so any parallel PR collides with it.
- **D, a branch stacked on an unmerged parent, which then squash-merged: 2.** The child still
  carries the parent's original commits. Replaying them onto the squashed copy conflicts with
  itself. "No stacked PRs" is already in both skills (`skills/agent-bus/SKILL.md:310`,
  `skills/agent-bus-master/SKILL.md:672`). It was broken anyway.

**What this count does not see.** A PR rebased in place, force-pushed and never closed, leaves
no supersede comment. The forge's force-push events (2230 on 346 PRs) mix rebases with review
fixes and routine re-pushes, so they do not measure conflicts either. The 17 are a floor.
Rebases in place are mostly the same causes, judging from the files. That is inferred, not
counted.

## 2. Which of the person's five points prevents which

| Point | Prevents | Notes |
|---|---|---|
| 1. Remove hot files | A: 8 (operator notes already done) | The settings count is the cheapest certain win. `merge=union` would have fixed #376 (already fixed by fragments) and #480's `INDEX.md`, but not its `AGENTS.md` copy. |
| 2. Schedule by files | B and C: 7, plus part of #494 (and #668, open) | The master skill already says "touches no package of the work in review". Sibling steps and moves slipped through it because nothing names them. |
| 3. Small, short-lived branches | Fewer collisions overall, unmeasured | The **no-stacking** part prevents D: 2. Smaller tasks alone would not have prevented B or C: they are about *which* files, not how many. |
| 4. Merge queue rebases and tests on the real head | **0 of 17** | It moves who rebases, and it catches a merge that is clean but broken. That break is real but not in this count. |
| 5. rerere + conflicts back to the owner | Repeats only: the #489→#500 and #637→#639→#641 chains re-resolved the same hunks | `rr-cache` lives in the common `.git` (`git rev-parse --git-path rr-cache`), so every worktree shares it. Today the conflicting PR goes to master (`MergeQueueRecord.Conflicting`; the ci-loop spec §8 Q6 kept it there). Decided 2026-11-21: it goes back to its owner ([conflict-owner spec](2026-11-21-conflict-owner-design.md), not built yet). |

## 3. The smallest first step

One small code PR, one skill PR and one config line. Together they target 11 of the 17: the
row count (#489, #500) and the rules (B 3, C 4, D 2), and #668's kind.

1. **The row count goes (point 1, hot-files step 1).** `configcmd_test.go` compares
   `len(doc.Rows)` with `len(config.Keys)` and checks that the names match, with no literal and
   no comment listing keys. This removes the conflict every settings PR has today.
2. **Four scheduling rules in the master skill (points 2 and 3; text only).** The rules go in
   `skills/agent-bus-master/SKILL.md`, beside "touches no package of the work in review":
   - **Sibling steps run in series.** Step N+1 of a spec is never in flight beside step N.
     It is sent once N is merged. Meanwhile the implementer takes an item from another spec.
   - **A move or a rewrite runs alone.** Examples: a package move, a fixture move, or a
     README/guide rewrite. While one is in flight, master sends nothing that touches its files.
     A pending one waits until in-flight PRs on those files merge. Master names it as such in
     the plan task.
   - **Two settings PRs may run in parallel only after point 1 above lands.** Until then they
     are siblings for this rule.
   - **No stacking, and the recovery when it happened anyway.** A child branch whose parent
     squash-merged is rebased with `git rebase --onto origin/<base> <parent's old head>`. That
     replays only the child's own commits. Today a plain `git rebase` replays the parent's
     originals too (D). The same text goes in the agent-bus skill, for the coder.

   Before sending a task, master compares it with the files of the work in flight, with plain
   git. This command gives those files for one in-flight branch:

   ```sh
   git diff --name-only origin/<base>...origin/<branch>
   ```

   The skill gives this command. No new code. (r4: as built, it lacks `--no-renames`, so a rename
   lists only its new path and a task editing the old path looks clear. The skill's next edit
   adds the flag.)
3. **rerere on, repo-wide (point 5, first half).** Master runs `git config rerere.enabled true`
   once in the shared repository. It is not `rerere.autoUpdate`: a replayed resolution stays
   unstaged, so the agent still sees it, and tests still check it. An operator-note fragment
   records it.

**Measure, with no new tooling.** Two weeks after the rules landed, architect reruns §1's count
over the next 14 days and compares it by cause. That count is the supersede comments plus
`merge-tree`.

The same window also counts **breaches of the four rules**, conflict or not. A breach that
happened to merge cleanly is still a breach: the rule is what keeps the next one clean. Each
rule is checked from the forge's PR list (`createdAt`, `mergedAt` or `closedAt`, `baseRefName`,
`headRefName`, title) and the plan files (`docs/plans/*.json`: `group`, `after`, `board`):

| Rule | A breach is | Read from |
|---|---|---|
| Sibling steps in series | Two PRs of one spec (matched by the spec the task links or the "<spec>, step N" title, or an `after` chain between their tasks; a plan `group` is only a hint, since groups such as `busmon` hold unrelated tasks) open at the same moment: one opened before the other merged or closed | the open intervals of the PRs of each group |
| A move or rewrite alone | A PR open while a move or rewrite PR is open, whose files intersect the move's. Both sides' files are `git diff --no-renames --name-only <merge-base>..<head>`: without `--no-renames` a rename lists only its new path, and a PR editing the old path would not intersect | the plan task named a move or rewrite (§3.2), else a PR with a rename (an `R` line in `git diff --name-status`) or 50 changed files or more (a proposed threshold; #638 changed 57) |
| Settings PRs in parallel | Two PRs touching `config/keys.go` open at once before step 1 merged. Step 1 (#676, `c81cffc7`) merged before the rules (#677, `62c66b42`), so the rule is already lifted for the whole window: it is reported as lifted, with no count | the PRs' files and intervals |
| No stacking | A PR whose `baseRefName` is not the project base, or whose head contains a commit of another open PR's head not yet in the base | `baseRefName`; `git merge-base --is-ancestor` per open pair |

The report gives each breach with the two PRs and whether it conflicted.

**If any rule was broken even once, it is enforced in code, in `agentbus plan`, not only in the
skills.** A skill rule that slipped once will slip again; §1's D already broke a rule both
skills stated. The enforcing change is specified then, from the breaches found; its shape:

- **Sibling steps:** `plan.Validate` refuses two tasks of one `group` with no `after` path
  between them, unless the task says it shares no file with its siblings (an explicit
  `parallel` flag master sets and the plan view shows).
- **A move or rewrite alone:** a task flagged `alone` must come `after` every task in flight
  that shares its files, and a task sent while an `alone` task is in flight is refused when
  their branches' files intersect (the §3.2 check, run by `plan` with `--no-renames` so a
  rename's old path counts).
- **No stacking:** `plan link` refuses a board request whose branch contains a commit of another
  in-flight task's branch that is not yet in the project base (`git merge-base --is-ancestor`).
- **Settings PRs:** nothing to enforce while the row count is derived.

The rules that showed no breach stay in the skills.

## 4. What not to do now

- **Do not build the conflict-aware-dispatch spec** (#508, `2026-11-14-conflict-aware-dispatch-design.md`)
  now. It specifies footprints, an overlap check, `delivery.hot_paths`, a queue rebase, patch-id
  approval carry and `{p}:rebases` stats. That machinery targets the same B and C cases as
  §3.2's rules. Revisit it if the measure shows B and C persisting under the rules.
- **No queue rebase or test (point 4) for conflicts.** It prevents none of the 17. Its own case
  is a merge that is clean but broken. It needs its own evidence first: a red `dev` after a clean
  merge.
- **No agent that resolves conflicts automatically,** and no `rerere.autoUpdate`.
- **No `merge=union`** on Go files, generated goldens (`help.txt`, settings goldens: rebase, then
  regenerate with `-update`) or `AGENTS.md`, whose index is a copy that `agentbus mem sync`
  rebuilds. `INDEX.md` alone had 1 conflict in the window. That is not enough to justify a
  merge driver whose failure mode is a silently duplicated line.
- **Do not split `config/keys.go` into one file per group** (hot-files step 3). `keys.go` itself
  conflicted once (#500), beside the count that step 1 removes. Hot-files step 2 (`Group`,
  `Label` and `Help` as fields of `config.Key`, which removes #495's maps) is the next candidate
  if settings conflicts persist.

## 5. Questions for the person

Answered 2026-11-21 (thread `1795217745802-0`, relayed by master): all four recommendations,
yes. Rules first, the machinery parked; rerere on repo-wide, never `autoUpdate`; README and
the guide as today. Q3's answer is no longer current: **a conflicting approved PR goes back to
its owner**, with escalation to master after a bounded time or number of hand-offs (the person,
2026-11-21; specified in [2026-11-21-conflict-owner-design](2026-11-21-conflict-owner-design.md),
not built yet). Built: step 1 (config-rows), steps 2 and 3
(the skills' rules and `docs/operator-notes.d/rerere.md`).

1. **Rules first, machinery parked?** Do §3 now, and park the conflict-aware-dispatch spec until
   the two-week count is in. Recommended: yes.
2. **rerere on for every worktree?** Its cache is shared, so one agent's resolution replays for
   another. Recommended: yes, without auto-staging.
3. **A conflicting approved PR: does it go back to its owner, or stay with master?** "Owner"
   means point 5's second half, using the CI loop's owner rule (`mergequeue.CIOwner`).
   Recommended: it stays with master for now. Decide after the count.
   **Replaced (the person, 2026-11-21, thread `1795222170445-0`):** a conflicting approved PR
   goes back to its owner, found from the board, with master when no single owner is found. The
   conflict is confirmed with `git merge-tree` against the base. It is one directive per episode,
   and master gets it after a bounded number of hand-offs or a bounded time
   ([2026-11-21-conflict-owner-design](2026-11-21-conflict-owner-design.md)).
4. **README and the operations guide.** A feature PR edits them today. Should it go on doing
   that, with §3.2's "a rewrite runs alone" rule, or should only the docs agent edit them at a
   release, as with operator notes? Recommended: keep it as today. The window shows one
   collision, and it came from a rewrite.

## 6. Tests

- Step 1: the existing `configcmd_test` still fails when a registry key is missing from the
  rows, or when a row has no key. This is a mutation check, run once: drop one row from the
  printer, and the test must fail.
- Step 2: no code. foureyes reads the skill diff against §3.2.
- Step 3: an operator-note fragment. `agentbus mem check` passes.
