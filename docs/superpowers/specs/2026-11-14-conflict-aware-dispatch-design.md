# Conflict-aware dispatch for parallel coders

Status: spec, for review (subject `conflict-aware-dispatch`). Not built. Base: origin/main
`b7a23edc`. Author: architect, task `conflict-aware-dispatch-spec`, from the person's feature
request of 2026-11-14 (thread `1794690081577-0`), relayed by master. One PR for §2 to §4; the hot
file refactor (§1) is separate PRs.

## 1. Problem, measured

With two coders, most time goes to the merge queue: rebases cascade on a few shared files. In the
30 days to `b7a23edc`, **423** PRs merged into `main`. The files they touched most often (merge
commits, first parent):

| PRs | File | Treatment (separate PRs, not this spec) |
|---|---|---|
| 154 | `CLAUDE.md` | rules change rarely; most edits are drift: one rule per short line, details moved to `docs/operator-notes.d/` |
| 69 | `README.md` | user reference split by topic under `docs/` |
| 57 | `docs/operator-notes.md` | **one fragment per change**, `docs/operator-notes.d/<task>.md`, assembled at release (the person's §1) |
| 57 | `cmd/busmon/keys.go` | key bindings per page file |
| 54, 43 | `docs/OPERATIONAL-GUIDE.md`, `.fr.md` | as operator notes: fragments |
| 52 | `cmd/busmon/main.go` | wiring only; features register themselves |
| 48 | `CHANGELOG.md` | fragments, assembled at release |
| 47 | `cmd/busmon/testdata/overlay/help.txt` | generated from the per-page bindings |
| 46, 38 | `cmd/agentbus/main.go`, `commands.go` | one file per command, each registering itself |
| 31 | `config/keys.go` | **one file per group**, each registering its entries (the person's §1); registry tests stay |

These are the default hot paths (§2.4) until each is refactored.

## 2. Task footprint and the overlap check

### 2.1 The footprint

A task declares the paths it expects to touch: its **footprint**, a list of **units**.

- **Unit (package granularity):** a Go file's unit is its directory, written with a trailing `/`
  (`cmd/busmon/`). A path under a `testdata/` directory belongs to the package above it. Any
  other file is its own unit (`CLAUDE.md`, `docs/operator-notes.md`). For the overlap, a
  footprint entry is normalised by the same rule. An entry may still name a Go file
  (`cmd/busmon/keys.go`): it then counts as its package for the overlap, and as that file for
  the hot paths (§2.4).
- **Overlap:** two footprints overlap when they share a unit.
- **Where it lives:**
  - in the plan task, a new optional field `footprint` (`plan/plan.go:40`, `Task`);
  - on the request, `RequestInfo.Footprint` (`bus/requests.go`), set by `agentbus request send
    <task> <target> --footprint <units>`. It is written only through the board scripts' round trip,
    never from a Go struct (`TestNoStructWriterOfTheBoard`).
- **Who declares it:** master or architect, at planning. The coder **amends** it at start, when
  it accepts: `agentbus request footprint <task> <units…>` adds units (it never removes one). An
  amendment is reported to master and re-runs the overlap check (§2.3).
- **A task with no footprint** overlaps everything. So an undeclared task is never run in
  parallel: the cost of forgetting falls on speed, not on conflicts.

### 2.2 In flight

A task is **in flight** from its request until its branch is in the base: its request is not
done, or it is done with a branch (`request done --branch`) not yet merged into
`origin/<delivery.base>`. The check uses the merge queue's existing `InBase` (`mergequeue/git.go:78`).

### 2.3 The check, before a second coder starts

`agentbus request send <task> <coder> --footprint …` runs the check before it publishes. It compares
the new footprint with that of every in-flight task **of another target**.

- **No overlap:** the request is sent. The two coders work in parallel.
- **Overlap:** it refuses, naming each overlapping task, its owner and the shared units. Master
  then does one of three things:
  1. **Sequence it:** keep the task in the plan with `after: [<task>]` and send it once that
     task is merged.
  2. **Give it to the owner:** send it to the coder that holds the overlapping task. One coder's
     tasks run one after the other, so they never conflict with each other. This is the only use
     of ownership: a tiebreak (§5).
  3. **Override,** for a reason the person accepts: `--overlap-ok "<reason>"`, stored on the
     request and shown in busmon.
- **`agentbus footprint check <units…>`** runs the same check without sending, for planning.

### 2.4 Hot paths

`delivery.hot_paths` (one registry entry in `config/keys.go`; a list of **file paths**, not units;
default empty): **two tasks that both touch a hot path never run in parallel**, even on
different hot paths. Hot files conflict in cascades, and the registries and notes are usually
edited together. For this project its value is §1's list, until each file is refactored.

- **Matched by file, never normalised to a package:** `cmd/busmon/keys.go` being hot does not
  make every `cmd/busmon/` task hot. A task touches a hot path only when its footprint names that
  file (§2.1).
- **A hot file touched without being named** is caught as outside the footprint (§2.5):
  `footprint verify` and `deliver` compare the diff with the named hot files as well as with the
  units.

### 2.5 A diff that leaves its footprint

- **Before asking for review,** the coder runs `agentbus footprint verify <task>`. It maps
  `git diff --name-only origin/<base>...HEAD` to units and lists those outside the footprint,
  and every hot path in the diff that the footprint does not name. The
  coder amends the footprint (§2.1), which tells master and re-runs the check. The agent-bus coder
  skill says so.
- **`agentbus deliver` enforces it:** for the request whose branch it pushes, a unit outside the
  footprint refuses the push, with the list.

## 3. The merge queue rebases, one item at a time

Today each coder rebases on its own whenever the base moves. Every rebase changes the head, so
the head-bound approvals (decision 2026-11-02 forge-delivery) no longer match and the reviewers
review again. This is the cascade.

### 3.1 An approval survives a clean rebase

**An approve of head `H` also covers head `H'` when `H'` is `H` replayed on a newer base with the
same patch.** Concretely, `git patch-id --verbatim` of `git diff <merge-base(base, H)> H` equals
that of `git diff <merge-base(base, H')> H'`.

- **`--verbatim`, never `--stable`:** `--stable` strips whitespace, so two different indentation
  changes give one id (reviewer-codex; checked with git 2.56: Python's `return` indented by 8
  spaces and by 2 both gave `f29971edb6c5` under `--stable`, and `5668c6048cde` and
  `701a0f0dc182` under `--verbatim`). Whitespace matters in Python, YAML, Makefiles and Go's
  raw strings.

- `deliver` and the merge queue's derivation compute it themselves, at the moment they need it,
  from the two SHAs. Nothing is stored or trusted, and no verdict is written for a reviewer.
- **`H` must stay readable** after the branch is force-pushed to `H'`. Before that push, the queue
  pushes `H` to a hidden ref, `refs/agentbus/approved/<H>`, removed once the item is merged. A
  reader that lacks `H` (the chore's working directory, a fresh clone) fetches that ref first. If
  `H` still cannot be read, nothing carries: the item needs review again (fail closed).
- A rebase with a conflict, or any edit, changes the patch-id. Review is then needed again, as
  today.
- This amends the forge-delivery decision. It is the person's call (§5, question 4).

### 3.2 Who rebases

Coders no longer rebase on their own (the coder skill says so). The queue rebases at the item's
turn: `agentbus merge-queue rebase --next`, run by **master**, an agent. It is not the dispatcher,
which never runs repository code.

1. **The item:** the oldest approved item (`ApprovedAt`) whose branch does not contain the base
   tip, and that is not held. None: nothing to do.
   - **One ready item at a time.** While an item rebased by the queue waits for its merge,
     `--next` rebases nothing else: a second rebase onto the same base would be checked against a
     base the first merge then changes. The queue's throughput is therefore one merge per check
     run, and that is intended. The coders keep working on their next tasks meanwhile.
   - **Held:** an item the person or master does not want merged now is held with `agentbus
     merge-queue hold <branch>` (and `release`), a set on the bus. `--next` skips it, and a held
     ready item no longer blocks the next one.
2. **Rebase** in a temporary worktree beside master's: `git rebase origin/<base>`.
   - **Conflict:** abort. A request goes to the item's coder: "rebase `<branch>`: conflict in
     `<files>`". The conflict is recorded (§4), and the item waits for the coder's new head.
   - **Clean:** go on.
3. **Patch-id** (§3.1): equal, and the approvals carry. Different (without a conflict, a rare
   case): treated as a conflict.
4. **Checks on the merged result:** `delivery.check`, a new setting holding the project's check
   command, run in that worktree. This project's value is `go build ./... && go vet ./... && go
   test ./... -count=1`. With it empty, no check runs and the queue says so. Red: no push; a
   request to the coder with the output's tail; recorded.
5. **Push** `H` to `refs/agentbus/approved/<H>` (§3.1), then `H':refs/heads/<branch>` with
   `--force-with-lease=<branch>:<H>`, so a push by someone else in between is not overwritten.
   Record the rebase (§4). The worktree is removed.
   - **The coder's worktree is now stale.** The queue tells the item's coder, without waking it:
     "`<branch>` rebased to `<H'>` by the queue". From approval to merge, the branch belongs to
     the queue: the coder makes no commit on it (coder skill). If the item is rejected later, the
     coder first takes the queue's head, `git fetch` then `git reset --keep origin/<branch>`
     (`--keep` refuses to drop uncommitted work), and then fixes it.
6. **The item is ready**, checked on base tip `B`, which is recorded with the rebase (§4).

### 3.3 The merge: onto the checked base, or not at all

`--match-head-commit` pins the PR's head, not its base: another merge between the check and this
one would put the item onto an unchecked `B'` (reviewer-codex). So a queue item is merged by
**`agentbus merge-queue merge <branch>`**, which binds both:

1. It reads the item's record: head `H'` and checked base `B`. The PR's head must still be `H'`.
2. It builds the merge commit `M` locally: `git merge --no-ff H'` on `B`. `H'` already sits on `B`,
   so `M`'s tree is `H'`'s tree: exactly what the checks ran on.
3. It pushes `M:refs/heads/<base>` **without force**. Git accepts it only as a fast-forward of the
   base, that is, only if the base is still `B`. If anything else merged meanwhile, the push is
   refused, nothing changed, and the item goes back to step 1 of §3.2 (rebase and check on the new
   base).
4. The forge marks the PR merged, as it does for any push that brings its head into the base. The
   hidden ref of §3.1 is removed.

**Who runs it:** the person, or master under `delivery.automerge` when that spec is built, which
would then merge through this command. The guard of the auto-merge spec applies: master holding
the pilot lease, or the person outside the agents' panes.

**The guarantee, stated exactly:** every merge made by `merge-queue merge` lands a tree that passed
`delivery.check`. A merge made another way (the forge's button) moves the base under the ready
item. The queue then sees that the item's branch no longer contains the base tip and redoes its
rebase and check, but the merge made elsewhere was not checked on its merged result. busmon's
`merges` line counts such merges ("merged outside the queue").

## 4. Measures

- **Records:** each `merge-queue rebase` appends one entry to `{p}:rebases`, a capped stream:
  `{subject, branch, from, to, base, result: clean|conflict|checks-red|patch-changed, files}`,
  with Redis `TIME`. `base` is the checked tip `B` (§3.3).
- **Per day:** the merge queue's chore (it already reads git in the services' directory every 5
  minutes) adds a `stats` field to `{p}:mergequeue` (additive):
  - **PRs merged per hour:** first-parent merges into the base over the day, and per active hour;
  - **rebases per merged PR:** from `{p}:rebases`;
  - **conflicts per file:** the top files in the day's conflict records.
- **Where:**
  - `agentbus merge-queue stats [--days N]` prints them;
  - busmon's Health page shows one `merges` line (today's numbers, top conflicting file);
  - master quotes the command's output in its daily report to the person.

## 5. The person's three questions: recommended answers

1. **Who declares the footprint?** Master or architect, at planning, where the task is cut. The
   coder amends it at start, once it has read the code (§2.1); an amendment is reported and
   re-checked. A declaration only at start would come too late to plan the parallel work.
2. **Granularity?** **Package**, with non-Go files as their own unit (§2.1). Files are too fine:
   two edits of one package conflict in tests and helpers more often than the file names say.
   Directories above the package are too coarse.
3. **Ownership or free assignment?** **Free assignment with the overlap check.** Ownership
   (coder1 = `cmd/busmon`) idles one coder whenever the plan leans on one area. Ownership is kept
   only as the tiebreak of §2.3: an overlapping task goes to the coder already holding the
   overlap.

A fourth, which only the person can answer: **does a clean rebase keep the approvals** (§3.1)?
Recommended: yes. Without it the queue's rebase still saves the coders' time, but every rebase
costs a review.

## 6. Tests

- **Units and overlap:** a `.go` path gives its directory; `testdata/` belongs to its package; a
  root file is itself. Overlap is symmetric. An empty footprint overlaps everything.
- **The check:** no overlap sends; an overlap refuses with the tasks, owners and units; the same
  target is allowed; `--overlap-ok` is recorded. A task done but not merged is still in flight; a
  merged one is not. Two tasks on different hot paths are refused.
- **Amend:** it only adds; it reports to master and re-checks; `footprint verify` lists the units
  outside; `deliver` refuses a push that leaves the footprint.
- **Patch-id carry:** on a disposable git repository, a clean rebase onto a moved base keeps the
  approval for `deliver` and the queue. A rebase that resolves a conflict, or adds a line, does
  not, and neither does a **whitespace-only** change (indentation 4 to 8 versus 4 to 2), which
  `--stable` would have carried.
- **Merge onto the checked base:** `merge-queue merge` pushes `M` when the base is still `B`; with
  another merge pushed meanwhile, the push is refused, nothing changes, and the item is queued for
  rebase again. A forge-button merge is counted as outside the queue.
- **Hot paths by file:** `cmd/busmon/keys.go` hot; a `cmd/busmon/` task that does not name it
  is not hot, and its diff touching `keys.go` is refused by `verify` and `deliver` until named.
- **Old head readable:** after the queue's force push, a fresh clone without `H` fetches
  `refs/agentbus/approved/<H>` and the carry holds. With the ref deleted, nothing carries.
- **Queue rebase:** on a fake forge and a disposable repository:
  - a conflict aborts, sends the request and records the files;
  - red checks never push;
  - a clean rebase pushes with the lease, and a branch moved meanwhile is not overwritten;
  - `--next` takes the oldest approved item; while one ready item waits, it rebases nothing; a
    held item is skipped;
  - the coder is told of the rebase.
- **Stats:** from scripted records and a scripted git log, the three numbers; busmon's line in a
  golden.

## 7. Out of scope

- The hot file refactor itself (§1): separate PRs, one per file group, each its own task.
- Automatic sequencing: the plan's `after` and master's next send do it; no scheduler is added.
- Rebasing by the dispatcher or the conductor.
- Footprints guessed from the code by a tool: declared and amended by people and agents only.
