# Plan history: every plan kept, one active, progress and staleness at a glance

- Status: draft, for review (coder, foureyes, reviewer-codex) and for the person's answers (§8)
- Revision 3: reviewer-codex on bb1ff8ef — §2 and §3.4 misstated today's Done rule: it also
  derives Done for a person task whose board row is done and for a delivered, approved non-merge
  task, and for integrated/aborted missions. No behaviour change: progress stays
  `Count(Derive(…))` with all four cases; the archive freezes that count; tests pin each case
  (§2, §3.3, §3.4, §5, §6, §7)
- Revision 2: foureyes on bb1ff8ef — archive moves are Go `WATCH`/`MULTI` transactions, not one
  Lua script (the record gains an `id`, with an in-transaction migration; done/total computed in
  Go, frozen for an archived plan; record and facts kept as opaque strings, no `cjson`); backup
  and `--delete` already cover `{p}:plans` (two questions dropped); `plan-stale` uses the bounded
  git exec of `trackedPaths` (§3.1–3.5, §5, §6, §8)
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
- `plan/derive.go` reads a task as **Done** in four cases (read at this base,
  `derive.go:111` and `:135-141`, `deriveMission`):
  1. master recorded it (`plan done`, fact `done`);
  2. a mission task whose mission is integrated, or aborted;
  3. a **person** task whose board row is `done` (no `plan done` needed);
  4. a non-merge, non-person task that was **delivered and whose review is approved**.
  Cases 2–4 are derived, not recorded. `plan.Count` is done/total over the derived rows, and is
  what the header's `3/9` shows today.
- Stale: `plan.StaleAfter = 7 days` since `LoadedAt`, shown by `plan show` (`"stale": true`),
  busmon's plan header and drawer; sentinel's daily review tells master when it is true.

## 3. Design

### 3.1 Identity: a plan is its name

A plan file already has a name (`"plan": "v0.27"`). The name is the plan's **id** in the
history (checked with `ValidName`-like rules extended to dots: `^[a-z0-9][a-z0-9._-]{0,47}$`;
a file whose name fails is refused at load). The id is taken in Go from `plan.Parse` and
stored in the record: `PlanRecord` gains `ID string json:"id"` (today the name lives only inside
the opaque `File` bytes, `bus/plan.go`). Loading a new commit of the same name is a new
**revision** of the same plan, not a new plan — exactly what the master skill already does when
a task is added ("a new commit and a new `plan load`; facts of the tasks kept stay").

### 3.2 Storage: one archive hash, the active keys unchanged

- `{p}:plan` and `{p}:plan:facts` stay as they are: every reader of the active plan (busmon,
  `plan show`, the derivation, the merge queue, sentinel) is untouched.
- New hash `{p}:plans`: field = plan id, value = one JSON object built **in Go**:
  `{id, goal, first_loaded, last_loaded, loads: [{commit, path, at}], record, facts, done,
  total, archived_at, closed}`. `record` is the last `{p}:plan` value **as an opaque string**
  (exactly the bytes stored there), and `facts` maps each task id to its `{p}:plan:facts` value,
  again an **opaque string**: nothing is decoded and re-encoded by Lua's `cjson`, which would
  rewrite an empty array as `{}` (the reason `File` is already opaque today). `done`/`total` are
  computed in Go at archive time (§3.4). `loads` keeps the last 50 revisions. A plan file is a
  few KB; a hundred plans stay under 1 MB.
- **Never erased by the plan commands.** No command deletes an entry. `busmon --reset` trims
  streams only, so the hash survives it. When the herdr-exit spec's explicit `survivesReset` list
  is built (§6.5 there; it does not exist in the code yet), `plans` joins it beside `resume`, so
  its guard test covers it; until then a Redis test here checks that `--reset` keeps it.
- **Backup and `--delete` need nothing added**: both enumerate a project's keys by `SCAN
  <p>:*` (`bus/backupdump.go:38`, `bus/deletequiet.go:126`), so `agentbus backup` carries
  `{p}:plans` and `busmon --delete` removes it with the rest of the project, as it does the
  active plan today. A Redis test pins both.
- Not in `projectKinds`, like the active keys: a plan alone never lists a project.

### 3.3 The commands

**Every write that moves a plan is one optimistic transaction in Go**, on the `putSnapshot`
pattern (`bus/ident.go`: `b.r.Watch` with a retry on `redis.TxFailedErr`, with jitter): `WATCH {p}:plan {p}:plan:facts {p}:plans`; read the active record,
its facts and the archive entry; read Redis `TIME`; compute in Go the new archive value
(including `done`/`total` from `plan.Count(plan.Derive(…))`, which needs the board, verdicts and mission states that only Go
reads) and the new active keys; then `MULTI` … `EXEC` writes them together. A conflict (another
writer touched a watched key) retries with jitter, as `putSnapshot` does, at most 20 times,
then fails with a clear error. So the
move is atomic — no crash or race leaves a plan in neither place — without any decoding in Lua.
`plan link|done|note` keep their existing Lua script (it only reads `rec.tasks` from the record
and never re-encodes it); the new `id` field passes through untouched.

**Migration.** A record written before the upgrade has no `id`. The first transaction that
archives it (a `load`, `resume` or `clear`) runs `plan.Parse` on its `File` in Go to take the id,
and writes the archive under it; a record whose file no longer parses is archived under
`legacy-<loaded_at>`. No offline step.

- **`plan load <path>@<commit>`** (unchanged syntax, no PR needed). The transaction:
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

### 3.4 Progress: today's rule, one function, no new deduction

**No behaviour change.** The percentage counts exactly what today's header counts: the rows
`plan.Derive` reads as Done, all four cases of §2 included. The person's "no automatic progress
deduction" (§1) rules out **new** inference from the board, PRs or merge queue (for instance
"PR merged, so the task is done"); it does not remove the derivations the plan view has used
since v0.22, and removing them would make today's `3/9` drop for no reason the person asked
for. So there is no new `Progress` with fewer inputs: progress is `plan.Count(plan.Derive(file,
inputs))`, one code path, with the same `Inputs` (facts, board, verdicts, asks, missions, merge
queue) that the header already reads.
- **Active plan**: computed at read time, live, as the header does today.
- **Archived plan**: computed **once, when it is archived**, and stored as `done`/`total`. The
  transaction of §3.3 reads the derivation inputs that are not plan keys (board, verdicts, asks,
  missions, merge queue) just before its `WATCH`; they are not watched, so a board change in the
  same instant can shift the frozen count by that one change — the count is a snapshot labelled
  with its time, not a ledger. The list shows that frozen count, labelled with the archive
  time. Reason: a mission's state, a board row or a verdict can be gone later (expired, trimmed,
  cleaned), and an archived plan must not lose progress it had. When an archived plan is resumed, its percentage
  is live again and may differ from the frozen one: a task done only by a derived case (2–4)
  whose source is gone reads as today's derivation reads it then (a mission no longer loaded
  reads `upcoming`, `mission not loaded`); a task master recorded done (`plan done`, case 1)
  stays done, since its fact travels with the plan. busmon's list shows the frozen count with
  its time, and the active plan's row the live one.
So the active plan's header and its own list row use the same function on the same inputs and
never disagree. Percent = `done*100/total`, rounded down: 100 % only when every task reads Done
by one of the four cases.
A plan is **finished** at 100 %, **closed** when `plan close` marked it, else **unfinished**.

### 3.5 Staleness: two signals, a warning only

A loaded plan is **stale** when either holds:
- **old**: `now − last_loaded > plan.stale_after` (setting in `config/keys.go`, default 7 days,
  replacing the constant `plan.StaleAfter`);
- **not moving**: at least `plan.stale_merges` (default 3) commits reached the delivery base
  (`delivery.base`, e.g. `origin/main`) since the plan's last progress — the newest of
  `last_loaded` and its newest `done` fact — while its done count did not change. The count is
  `git rev-list --count --first-parent --since=<last progress> <base>` on the local repo's
  refs, no fetch, run the way the existing `git ls-files` health read is
  (`cmd/agentbus/healthchecks.go` `trackedPaths`: `exec.CommandContext` with the check's
  deadline, its own process group, killed as a group on cancel): it reads refs, runs no repository code, and is a hint, not a deduction — the
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

- `bus/plan.go`: `PlanRecord.ID`; `PlansKey`; the transactions `LoadPlan` (replacing the
  `planLoad` script), `ResumePlan`, `ClearPlan` (archives), `ClosePlan`, each `WATCH`/`MULTI`
  with retry; reads `Plans`, `ArchivedPlan(id)`. Each archive entry is one JSON string written
  whole (`HSET` of one field), its record and facts kept as opaque strings: no partial writer
  can drop a field (the board rule's spirit). The count is passed in as a function so `bus` does
  not import `plan`.
- `plan/`: no new progress function (`Count` over `Derive`, §3.4); `ValidID`, `Stale(lastLoaded, lastDone, mergesSince, now, settings)`.
- `config/keys.go`: `plan.stale_after` (duration, default 7d), `plan.stale_merges` (int,
  default 3, 0 = signal off).
- `cmd/agentbus`: `plan list|resume|close`, `plan show [<id>]`, `plan clear` archives; health
  check `plan-stale`.
- `cmd/busmon`: header and Work percentage, `h` overlay, `r` resume with confirmation.
- `skills/agent-bus-master`, `skills/agent-bus-sentinel`: §3.5, §3.7.
- Migration: in Go, inside the first archiving transaction (§3.3): the id is parsed from the old
  record's `File`; `legacy-<loaded_at>` when it no longer parses.

## 6. Testing decisions

- Redis tests (disposable broker), one per transaction: load archives the previous plan with its
  facts; a revision keeps the facts of kept tasks; loading an archived id restores its facts
  filtered to the new tasks; resume swaps the two plans atomically; clear archives and leaves none
  active; close marks and never deletes; `loads` capped at 50; `--reset` keeps `{p}:plans`;
  `backup` carries it and `--delete` removes it; a concurrent `plan done` during a `load` makes
  the load retry and the done survive (WATCH conflict); a record with an empty task list or
  empty fact arrays round-trips byte-for-byte through archive and resume (no `{}` rewrite); a
  pre-upgrade record without `id` is archived under the id parsed from its file, and under
  `legacy-<loaded_at>` when the file does not parse.
- Progress: tests pinning today's four Done cases so this change cannot alter them — a person
  task whose board row is `done` with no `plan done` fact counts; a non-merge task delivered with
  an approved review counts; a merge task delivered and approved but not recorded does **not**;
  an integrated and an aborted mission task count; and the active plan's header and its list row
  give the same numbers for the same inputs (one code path). Archive: the frozen count equals
  the live count at archive time; after the board row of a case-3 task is trimmed, the archived
  count is unchanged and a resumed plan's live count reflects the loss.
- `plan.Stale`: old, not moving (merges ≥ N with no new done), moving (a done after the merges),
  signal off at 0; the git count against a scripted repository.
- busmon: the `h` overlay rows, `enter` read only, `r` confirm then resume, refused on the active
  plan.
- Mutation on: the WATCH of the facts key (dropping it must fail the concurrent-done test), the archive step of load (dropping it must fail "load archives"), the filter of
  restored facts, the count's done rule (counting an in-review task must fail; dropping case 3 or 4 must fail the pinning tests), the stale
  thresholds.

## 7. Out of scope

New automatic progress deduction from the board, the PRs or the merge queue (the person: too
complex); the plan view's existing Done derivations (§2) stay as they are. Editing a plan from busmon. Deleting a plan from the history. Several active plans at
once.

## 8. Questions for the person

(Revision 2: two former questions are answered by the code — `busmon --delete` removes
`{p}:plans` with the rest of the project and `agentbus backup` carries it, both by `SCAN <p>:*`;
§3.2.)

1. **busmon resume.** May busmon re-open a plan itself (after a confirmation), or should it only
   ask master (an `ask` to master: "re-open v0.25")? Recommended: busmon does it, and master is
   told by a report, since it is the person's own decision.
2. **Stale thresholds.** Are 7 days and 3 merges since the last recorded done the right
   defaults?
3. **Abandoning a plan.** Is `plan close <id>` (kept in the list, never stale) wanted, or is an
   unfinished plan simply left unfinished?
4. **Folding.** Should the list fold finished plans older than 30 days by default, or always show
   every plan?
