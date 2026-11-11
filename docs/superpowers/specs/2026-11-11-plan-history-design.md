# Plan history: every plan kept, one active, progress and staleness at a glance

- Status: draft, for review (coder, foureyes, reviewer-codex) and for the person's answers (§8)
- Date: 2026-11-11
- Author: architect
- Task: `plan-history-spec`
- Base: `origin/main` at 394a7261
- Busmon key: `h` is free on the Plan tab at this base (no binding found in `cmd/busmon`)
- Builds on: the plan view (spec 2026-10-22, v0.22): `{p}:plan`, `{p}:plan:facts`,
  `agentbus plan load|show|link|done|note|clear`, busmon `P`, the header's `plan v0.22 3/9`

## 1. Problem

Only one plan exists at a time. `agentbus plan load` replaces `{p}:plan` and drops the facts of
every task the new file does not hold (`planLoad` script, `bus/plan.go`); `plan clear` deletes
both keys. So:
- loading the next batch erases the previous one: what it held, which tasks were recorded done
  and when, is gone from the bus (the JSON stays in Git, the facts do not);
- an unfinished plan cannot be put aside and taken up again: loading it back starts every task
  from zero;
- the person has no list of past plans, and no percentage per plan;
- "stale" means only "loaded more than 7 days ago" (`plan.StaleAfter`, a constant); a plan whose
  done count stopped moving while work merged is not flagged.

Constraint (the person): simple, fast, reliable. Out (the person, via master): **no automatic
progress deduction** from the board or the PRs; loading stays one command from a file
`<path>@<commit>`, no PR needed to load.

## 2. What exists (read at 394a7261)

- `bus/plan.go`: `PlanRecord{Path, Commit, LoadedAt, Tasks, File}` in `{p}:plan` (a string),
  `PlanFact{PR, Ref, Linked, Done, Note, At}` per task in `{p}:plan:facts` (a hash). Writes are
  Lua scripts using Redis `TIME`. Neither key is in `projectKinds` ("a plan alone never makes a
  project appear in `--list`").
- `cmd/agentbus/plancmd.go`: `plan load <path>@<commit>` resolves the commit in the local repo
  and reads the file with `git show` — **any commit the repo has**, merged or not. The "PR first"
  habit lives only in the master skill (commit the file on `plan/<name>`); nothing in the code
  requires a PR. This spec keeps the code as it is and fixes the skill's wording (§3.6).
- `plan/derive.go`: a task is **done** when master recorded it (`plan done`, fact `done`) or its
  mission is integrated or aborted. Every other state is derived for display; nothing is ever
  marked done automatically. `plan.Count` is done/total over the derived rows.
- Stale: `plan.StaleAfter = 7 days` since `LoadedAt`, shown by `plan show` (`"stale": true`),
  busmon's plan header and drawer; sentinel's daily review tells master when it is true.

## 3. Design

### 3.1 Identity: a plan is its name

A plan file already has a name (`"plan": "v0.27"`). The name is the plan's **id** in the
history (checked with `ValidName`-like rules extended to dots: `^[a-z0-9][a-z0-9._-]{0,47}$`;
a file whose name fails is refused at load). Loading a new commit of the same name is a new
**revision** of the same plan, not a new plan — exactly what the master skill already does when
a task is added ("a new commit and a new `plan load`; facts of the tasks kept stay").

### 3.2 Storage: one archive hash, the active keys unchanged

- `{p}:plan` and `{p}:plan:facts` stay as they are: every reader of the active plan (busmon,
  `plan show`, the derivation, the merge queue, sentinel) is untouched.
- New hash `{p}:plans`: field = plan id, value = JSON
  `{id, goal, first_loaded, last_loaded, loads: [{commit, path, at}], record, facts, done,
  total, closed}`: the last `PlanRecord` (file bytes included), the facts as they stood when the
  plan stopped being active, the done/total computed then, and `closed` (§3.4). `loads` keeps
  the last 50 revisions. A plan file is a few KB; a hundred plans stay under 1 MB.
- **Never erased by the plan commands.** No command deletes an entry. `busmon --reset` trims
  streams only, so the hash survives it. When the herdr-exit spec's explicit `survivesReset` list
  is built (§6.5 there; it does not exist in the code yet), `plans` joins it beside `resume`, so
  its guard test covers it; until then a Redis test here checks that `--reset` keeps it. `--delete` of a whole project is
  the one exception (§8 Q2).
- Not in `projectKinds`, like the active keys: a plan alone never lists a project. It is added
  to the backup/restore key list explicitly (§8 Q3 asks whether backup should carry it).

### 3.3 The commands

All writes are single Lua scripts with Redis `TIME`, as today; each one moves the active plan
into the archive and out of it atomically, so no crash leaves a plan in neither place.

- **`plan load <path>@<commit>`** (unchanged syntax, no PR needed). The script:
  1. archives the active plan, if any: its record and facts written to `{p}:plans[<its id>]`;
  2. if the loaded file's id equals the active plan's, it is a revision: facts of the tasks
     still in the file are kept (today's rule);
     else if the id is in the archive, its archived facts are restored, filtered to the tasks in
     the new file (loading an old plan's new commit resumes it);
     else facts start empty;
  3. sets `{p}:plan`, `{p}:plan:facts`, and appends the revision to `loads`.
- **`plan list [--json] [--all]`**: one row per plan, newest `last_loaded` first:
  `● v0.27  7/12 58 %  loaded 3d ago (9f1c2ab)  active` / `  v0.26  9/9 100 %  finished` /
  `  v0.25  4/7 57 %  unfinished · stale`. By default finished plans older than 30 days are
  folded into a count (`--all` shows them).
- **`plan resume <id>`**: archives the active plan, then makes `<id>`'s archived record and
  facts the active plan. No Git read: the archived file bytes are the plan. To take a newer
  commit of it, `plan load` that commit (step 2 above restores its facts).
- **`plan show [<id>]`**: today's view of the active plan; with an id, the archived plan read
  only (facts as archived, its tasks derived from them; board-derived states shown as `—`).
- **`plan clear`**: no longer deletes: it archives the active plan and leaves none active.
- **`plan close <id>`**: marks an unfinished plan abandoned (`closed: <time>`); it stays in the
  list, sorted with the finished ones, never stale. `plan reopen`… is `plan resume`.
- `plan link|done|note` act on the active plan only, as today.

### 3.4 Progress: one function, recorded facts only

`plan.Progress(file, facts, missions) (done, total int)` — the same Done rule as
`derive.go` (a recorded `done` fact, or the task's mission integrated or aborted), no board, no
PR, no verdict input. It replaces `plan.Count` for the header and is used for archived plans,
so the active plan's percentage and its row in the list never disagree. Percent =
`done*100/total`, rounded down (a plan is 100 % only when every task is recorded done).
A plan is **finished** at 100 %, **closed** when `plan close` marked it, else **unfinished**.

### 3.5 Staleness: two signals, a warning only

A loaded plan is **stale** when either holds:
- **old**: `now − last_loaded > plan.stale_after` (setting in `config/keys.go`, default 7 days,
  replacing the constant `plan.StaleAfter`);
- **not moving**: at least `plan.stale_merges` (default 3) commits reached the delivery base
  (`delivery.base`, e.g. `origin/main`) since the plan's last progress — the newest of
  `last_loaded` and its newest `done` fact — while its done count did not change. The count is
  `git rev-list --count --first-parent --since=<last progress> <base>` on the local repo's
  refs, no fetch: it reads refs, runs no repository code, and is a hint, not a deduction — the
  plan is never changed by it.

Where it shows:
- a health check **`plan-stale`** (warn, never fail), computed with the other checks of the
  project's health pass (the host that has the repo), detail
  `v0.27: loaded 9d ago` or `v0.27: 5 merges on origin/main since the last recorded done (2d)`;
  busmon reads it from `{p}:health` like any check, so it shows on the laptop too;
- `plan show` / `--json` gain `stale_reason`; busmon's plan header and the list row show
  `stale` with the reason;
- sentinel's daily review reads the `plan-stale` check instead of `plan show --json`'s flag, and
  tells master in one `agentbus cmd master` line (unchanged rule: it never loads or edits a
  plan). An unfinished archived plan is never stale: only the active one is judged.

### 3.6 busmon

- **Plan tab (`P`)**: unchanged rows, plus the header `plan v0.27 · 7/12 58 %`. A new key `h`
  opens the **plan list** (history) as an overlay: the rows of `plan list`; `enter` shows the
  selected plan read only (as `plan show <id>`); `r` on an unfinished or closed plan asks
  "make v0.25 the active plan? the active v0.27 is archived with its facts" and runs
  `plan resume`; `esc` returns.
- **Work tab** and the header: the active plan's name and `7/12 58 %`; `stale` when the check
  warns.
- busmon writes nothing else: resume is the only plan write it offers (§8 Q1).

### 3.7 The master skill

The plan section says: write `docs/plans/<name>.json`, **commit it** (a branch is enough; a PR
is needed only to bring the file to main, not to load it), `agentbus plan load
<path>@<commit>`; use `plan resume <id>` to take up an unfinished plan, `plan close <id>` to
abandon one; never `plan clear` to "make room".

## 4. User stories

1. As the person, I want every plan ever loaded kept with its load time, source and task states, so that loading the next batch never erases the last one.
2. As the person, I want a list of plans in busmon with each one's percentage done, so that I see at a glance what was finished and what was left.
3. As the person, I want to re-open an unfinished plan from that list, so that its recorded progress comes back with it.
4. As the person, I want the active plan and its percentage on the Work tab and in the header, so that I always know which plan the team works on.
5. As the person, I want a warning when the active plan is old or has not moved while work merged, so that a forgotten plan does not mislead me.
6. As master, I want `plan list` and `plan resume <id>`, so that I can do from the CLI what the person does in busmon.
7. As master, I want loading a plan to stay one command from a committed file, so that a plan never waits on a PR.
8. As sentinel, I want the staleness as a health check, so that my daily review reads one source.
9. As a maintainer, I want the active-plan keys and their readers unchanged, so that the change is small and reviewable.

## 5. Implementation decisions

- `bus/plan.go`: `PlansKey`, `PlanArchive` type, scripts `planLoad` (extended), `planResume`,
  `planClear` (archive), `planClose`; reads `Plans`, `ArchivedPlan(id)`. Never written from a Go
  struct through `HSET` field by field: each value is one JSON string set by the script (the
  board rule's spirit: no field can be dropped by a partial writer).
- `plan/`: `Progress`, `ValidID`, `Stale(lastLoaded, lastDone, mergesSince, now, settings)`.
- `config/keys.go`: `plan.stale_after` (duration, default 7d), `plan.stale_merges` (int,
  default 3, 0 = signal off).
- `cmd/agentbus`: `plan list|resume|close`, `plan show [<id>]`, `plan clear` archives; health
  check `plan-stale`.
- `cmd/busmon`: header and Work percentage, `h` overlay, `r` resume with confirmation.
- `skills/agent-bus-master`, `skills/agent-bus-sentinel`: §3.5, §3.7.
- Migration: none needed. The first load or clear after the upgrade archives whatever plan is
  active; nothing older exists on the bus to recover.

## 6. Testing decisions

- Redis tests (disposable broker), one per script: load archives the previous plan with its
  facts; a revision keeps the facts of kept tasks; loading an archived id restores its facts
  filtered to the new tasks; resume swaps the two plans atomically; clear archives and leaves none
  active; close marks and never deletes; `loads` capped at 50; `--reset` keeps `{p}:plans`.
- `plan.Progress`: table tests, and a test that the active plan's header and its list row give
  the same numbers for the same facts (one function).
- `plan.Stale`: old, not moving (merges ≥ N with no new done), moving (a done after the merges),
  signal off at 0; the git count against a scripted repository.
- busmon: the `h` overlay rows, `enter` read only, `r` confirm then resume, refused on the active
  plan.
- Mutation on: the archive step of load (dropping it must fail "load archives"), the filter of
  restored facts, `Progress`'s done rule (counting an in-review task must fail), the stale
  thresholds.

## 7. Out of scope

Automatic progress deduction from the board, the PRs or the merge queue (the person: too
complex). Editing a plan from busmon. Deleting a plan from the history. Several active plans at
once.

## 8. Questions for the person

1. **busmon resume.** May busmon re-open a plan itself (after a confirmation), or should it only
   ask master (an `ask` to master: "re-open v0.25")? Recommended: busmon does it, and master is
   told by a report, since it is the person's own decision.
2. **Deleting a project.** `busmon --delete` removes every key of a project. Should it keep
   `{p}:plans` (the history outlives the project), or delete it with the rest? Recommended:
   delete it with the rest — a deleted project is gone, and the plans' files remain in Git.
3. **Backups.** Should `agentbus backup` carry `{p}:plans`? Recommended: yes (it is small and is
   the only record of the facts).
4. **Stale thresholds.** Are 7 days and 3 merges since the last recorded done the right
   defaults?
5. **Abandoning a plan.** Is `plan close <id>` (kept in the list, never stale) wanted, or is an
   unfinished plan simply left unfinished?
6. **Folding.** Should the list fold finished plans older than 30 days by default, or always show
   every plan?
