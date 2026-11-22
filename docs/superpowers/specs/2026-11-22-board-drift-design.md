# The board follows the work (board-drift)

Status: spec r3, for review (subject `board-drift`). Not built. Base: origin/dev `a5e9847b`.
r2 (foureyes' r1 review): §3.2 never judges a head already in the base (`review_base`); four
citation fixes; §4's note names its own refusals.
r3: master's item 7 (cmd `1795348884780-0`): the person's directives are answered, enforced by
the dispatcher (§8, Q5). §3.2 takes foureyes' two r2 notes: a local-forge base, and a failed fetch.
Author: architect, from master's directive (cmd `1795347811956-0`, task `board-drift-spec`),
which carries the person's directive. No new dependency. Questions for the person are last (§10).

## 1. The problem

The board drifts from the work. Agents report and deliver, but nobody closes the request. Then the
Board shows alerts ("no acceptance", "no reply", "body lost") on work that is already done. It is
continuous, not only after a suspend. Master reconciled the board by hand on 2026-11-22 (directive
item 4). This spec makes the board close itself where a fact proves the work is done, and report
where only a person or master can judge.

## 2. Today (code at `a5e9847b`)

- **Closing a request.** Only `request done` closes a request (`requestTransitionSrc`,
  `bus/requests.go:287`). It is run by the target, or by the sender before an accept. It needs an
  accept first, unless the body is gone or expired (`closed_unaccepted`). It appends a `reply` to
  the sender. Nothing else ever closes a request, and nothing does it in someone's name.
- **A request's branch.** `request review --branch` sets `request.branch` (`:325`), and `done
  --branch/--commit` sets it at the end (`:344`). A claim's branch is `BoardEntry.Branch`. No index
  maps a branch to its request: `mergequeue.ConflictOwner` scans the board (`mergequeue/conflict.go:238`).
- **The merge queue already learns merges.** The dispatcher's pass runs every 5 minutes, and sooner
  after a delivery (`internal/dispatchsvc/mergequeuechore.go:33`). It reads branch tips with
  `RemoteTips`/`LocalTips` (`mergequeue/git.go`). It judges a head delivered into the base with
  `Delivery.Delivered` (`mergequeue/delivered.go:76`), which covers ancestor, squash and rebase
  merges, with patch-ids cached by SHA. It runs this only on heads approved in the verdict ledger,
  and it never touches `{p}:board`. Its writes are fenced by the lease (`fenceCheck`,
  `bus/conductorkeys.go:63`). Fenced board writers already exist for the conductor:
  `FencedBoardState` (`:122`) and `FencedRequest` (`:102`).
- **A report knows no request.** `agentbus report` has `--auto`, `--needs` and `--to`
  (`cmd/agentbus/reports.go:82`). Its stream entry has no `task` field.
- **Board alerts** (`boardAlert`, `cmd/busmon/board.go:79`):

  | Alert | Fires when | Its age is measured from |
  |---|---|---|
  | body lost | at once | `created_at` |
  | no acceptance | after `board.no_accept_after` (30 min) | `created_at` |
  | no reply | after `board.no_reply_after` (2 h) | `accepted_at` |

  The "no reply" alert does not check the state, so it also fires on a request in `review` (waiting
  on reviewers) or `blocked` (waiting on someone else). The AGE column is `BoardEntry.Updated`
  (`cmd/busmon/board.go:215`, header `:179`). Every transition rewrites `Updated`. So "no reply 17h" can sit beside "AGE 1h"
  on the same row: the two ages measure different events, and neither says which.
- **Sentinel** is woken on a new health failure and once a day (`bus/health.go:138`, `:203`). Its
  skill forwards `agentbus health warns` to master. It never edits anyone's work.

## 3. Close on merge (item 1: dispatcher, deterministic)

### 3.1 What closes

A request **closes on merge** when all of the following hold in one merge-queue pass:
- its state is **`review`**;
- it names a branch (`request.branch`);
- that branch's head is delivered into the project's base, as judged by `Delivery.Delivered`
  (`forge.ProjectDeliveryBase`; `dev` here).

An **`accepted`** request is never closed on merge. An agent that resumed a request (`accept` from
`review`, as in a multi-step build) is working on it, even when an earlier step's branch merged.
`requested`, `blocked` and `done` requests are not touched either.

### 3.2 The head judged

The head judged is the branch's tip as the pass already reads it: the remote tip, otherwise the
local ref. A branch deleted after its merge (GitHub's auto-delete) has no tip anymore. For that
case, `request review` now also records the head it was given:
- `--commit <sha>` is accepted as on `done`;
- without the flag, the CLI records `git rev-parse --verify <branch>` when that resolves in the
  agent's directory;
- otherwise it records nothing.

The pass judges the tip if it has one, else the recorded commit. With neither, the request is not
judged, and §5 reports it.

**A head already in the base is never judged** (foureyes, r1). `Delivered` counts an ancestor as
delivered. So without a guard, a review on a branch with no commit beyond the base would close at
the next pass with nothing merged. That happens with a branch never committed to, a branch reused
after an earlier merge, or a `--commit` naming a base commit. The guard has two parts:
- `request review` also records **`review_base`**: the base tip the CLI read. On a forge with a
  remote, that is `git rev-parse --verify origin/<base>` after `git fetch origin <base>`. In local
  mode (no remote; foureyes, r2), it is the local `<base>`, as conflict-owner's
  `ConflictPass.Local` reads it (`mergequeue/conflictloop.go:34`). The CLI **refuses** the review when the recorded head is already
  an ancestor of that tip: "nothing to review: `<head>` is already in `<base>`".
- **A failed fetch records no `review_base`** (foureyes, r2). A stale tip could miss a newer base
  that already holds the head, and that head would then be judged and closed. The review itself
  still goes through: only its close on merge is lost, and §5 reports it. A CLI that cannot resolve
  the base also records nothing and refuses nothing.
- The pass judges a request only when it has a `review_base` and the head judged is **not** an
  ancestor of it (`git merge-base --is-ancestor`, as `InBase` runs it). A request with no
  `review_base` is not judged, and §5 reports it ("cannot be judged").

These candidates are added to the heads that `Delivered` already judges, in the same pass and with
the same patch-id cache, under the same 20-second budget. A pass that runs out of budget judges the
rest on its next run. Nothing new is polled.

### 3.3 The write

A new fenced script, `FencedRequestMerged(token, task, branch, head)`, checks in one call:
- the lease: the dispatcher's own key, `DispatchKey`, which the merge queue's other writes
  (`FencedCIStep`, `bus/ci.go:140`) also fence on. `fenceCheck` is the generic check; the
  conductor's `FencedBoardState` fences on the conductor's key instead;
- that the request is still in `review`;
- that its `request.branch` is still the branch the pass read.

When all hold, it sets:
- `state = done`;
- `request.closed_on_merge = {head, base}`;
- `request.responded_at` = Redis `TIME`;
- `updated`.

Like every board writer, it edits the JSON entry in Lua, never from a Go struct
(`TestNoStructWriterOfTheBoard`). If anything changed since the read, the script writes nothing,
and the next pass judges again.

It appends **no `reply`** to the cmd stream, so it wakes nobody (Q3). Instead it writes one entry
to the events stream (`request-merged`: task, branch, head). busmon's Board detail shows "closed on
merge `<head>` into `<base>`" where a reply would be. `plan/derive.go`'s `delivered` already counts
any done request that is not `closed_unaccepted`, so a close on merge counts as delivered with no
change there. A done request with `closed_unaccepted` stays "closed", as it is today.

### 3.4 A step, not the whole request (Q1)

`request review --more` says that more steps follow. When a request with `review_more` set closes
on merge, the script does not close it. It sends it back to `accepted` (a resume, which the WIP
limit never refuses), clears `branch`, `commit` and `review_more`, and appends the head to
`request.merged_steps`. The agent's last step goes to review without `--more`, and its merge
closes the request.

### 3.5 What stays the agents' job

`request done --branch` stays a must in the coder and docs skills: it is still the normal close,
and close-on-merge is the net under it. The skills' one-line addition is: "put a delivered branch
in review (`request review <task> --branch <b>`, `--more` if steps follow); its merge closes it".

## 4. A report that names its request (item 2)

`agentbus report <self> --task <t> [--done] <text…>`:
- `--task <t>` writes `task` on the report entry. A new transition, `note`, sets
  `request.reported_at` and `request.report_id` on that request, with no state change. Its
  refusals are only these: an unknown task or one that is not a tracked request, a request whose
  target is not `<self>`, and a `done` request. It does not inherit `accept`'s other refusals
  (the WIP limit, an expired request, a lost body), because a note changes no state. A refused
  note is checked before anything is published.
- `--done` also completes it, as `request done <t>`, with the report's first line and
  `(report <id>)` as the reply. It is refused where `done` would be (not accepted, blocked), and
  then nothing is published. In the CLI, `--done` is report-then-done: a refused `done` after a
  published report prints the refusal and exits 1, and the report stands.
- A report without `--task` behaves as today (Q2: no guessing from the text).

The agent-bus skill gets one line: "a report on a task names it (`--task`); a final report closes
it (`--done`)".

## 5. Sentinel watches the board (item 3: report only)

A new read-only command, `agentbus board drift [--json]`, lists three kinds of finding, one line
each. It prints "no drift" and exits 0 when there is none.

1. **Open with a merged branch:**
   - an `accepted` or `review` request whose `request.branch` head (or recorded commit) is
     delivered into the base. This catches what §3 must not close: an accepted multi-step, or a
     review whose `--more` was wrong;
   - a review request with no tip and no commit ("cannot be judged").

   It uses `Delivery.Delivered` in the project's directory, as the merge queue does.
2. **Body lost:** an open request whose body is gone from the stream (`availability = missing`).
   The finding names who may close it: the target, or the sender (`request done <t> <why>`).
3. **Agents with no terminal:**
   - a `required` role with no live terminal (`bus.ShownState` = `no terminal`);
   - a pop with no live terminal that owns an open request (§6). This one is named with its
     `agentbus pop <role> --resume <session>` line from `{p}:resume`.

The sentinel skill's daily review runs `agentbus board drift`. When there is drift, it forwards the
output to master **verbatim**, in the same message as the health digest. Sentinel never closes,
reassigns or edits a task. That stays with the owner, the sender or master.

## 6. Why architect and docs stayed down (item 5)

**Cause.** Two by-design exclusions met one silent path.

1. **`resume` never spawns a pop** (`requiredAgents`, `cmd/agentbus/resume.go:319`; spec §12 Q3).
   It only prints the recovery lines. Both resumes told master so, in the directive "Project resumed
   after a suspend" (cmd `1795339055401-0` at 00:09 and `1795347437522-0` at 11:23): "pop architect
   not relaunched: agentbus … pop architect --resume c2bf3f3b…", and the same for docs, coder2 and
   reviewer-codex. Nobody ran those lines.
2. **Self-repair only covers the template's tabs** (`facts`, `internal/dispatchsvc/selfrepairchore.go:203`).
   It skips a retired pop (`selfrepair/decide.go:177`), and the roles.toml loader refuses `required` on a
   pop (`rolecfg/config.go:165`): "a pop comes back through master only". architect and docs are pops, so self-repair was
   right not to act.
3. **The silent path.** The dispatcher saw the problem and said nothing. Its log
   (`~/.local/state/agentbus/agent-bus-monitor/dispatch.log`, 2026-11-22 10:17 and 12:37) shows:
   - `pop-kept: it owns a board task: dispatchsvc-extraction-spec (working), merge-conflicts-r5
     (requested)` for architect;
   - the same for docs' six requested tasks;
   - then `none: no obligation` for both.

   `popRetire` (`dispatch/popretire.go:153`) keeps a pop that owns a board task and logs it, but
   writes nothing to master. That holds even when the pop is closed: `f.Closed`, its terminal gone
   and its process dead (`dispatch/popfacts.go:121`). The wake rules found no obligation, because a
   tracked request's cmd entry older than the 24-hour horizon, or already read, is not one. So for
   13 to 16 hours, nothing escalated.

**Smallest fix.** In `popRetire`, when the pop is **closed** (`f.Closed`) and kept only because it
owns open requests (`f.OwnedTasks`), send master one directive per episode through the existing
`FencedCmdOnce`, under a new mark `popstranded:<role>`:

> architect has no terminal and owns open requests: board-drift-spec (requested),
> merge-conflicts-r5 (requested). Bring it back: agentbus --project agent-bus-monitor pop architect
> --resume c2bf3f3b-…

The session comes from `{p}:resume`. Without one, the line is `pop <role>`.

This wakes master, as a pop-done notice does. The dispatcher still never relaunches a pop (Q4).
The `--force`/`retire --forget` paths are unchanged. A pop retired on purpose with its requests
closed owns nothing, so it is never told about.

## 7. The alert says what it measures (item 6)

- Every alert names its starting point:
  - `no acceptance 3h since request`;
  - `no reply 17h since accept`, or `no reply 2h since report` when `reported_at` (§4) is newer;
  - `body lost (request 27h)`.
- **No reply** is measured from the newer of `accepted_at` and `reported_at`. It fires only on an
  `accepted` request. A request in `review` waits on its reviewers, and a `blocked` one waits on
  someone else: neither raises "no reply".
- The AGE column header becomes **CHANGED**. It still measures `Updated`, the last transition. The
  task detail lists the request's times (created, accepted, reported, in review, responded).
- The alert keeps its place in the sort (oldest alert first). The sort key is the alert's own start
  time, as today.

## 8. The person's directives are answered (item 7)

**The rule.** An agent answers every directive the person sends it on the bus, with `report <self>
--to <person>` or `reply --ref <id> <person>`. Today that is a habit, written in the agent-bus
skill. busmon is where the person reads; text typed only in a pane never reaches them. This
section makes the dispatcher enforce it.

**What is judged.** A new health check, `person-unanswered` (Handler `dispatch`, every minute). It
sits beside `quiet-agent` in the stall guard (`internal/dispatchsvc/stallchecks.go`), whose shape
it copies: `QuietDirectiveFor` and `QuietAgentWakes` in `health/stall.go`.

For each agent A, the check takes the oldest **person's directive** D addressed to A after A's
last answer to the person:
- D is a `directive` with `human = yes` and `typed = yes`, from the project's person (the dispatcher's
  `Config.Person`, `identity.as`, `dispatch/loop.go:42`), with no `wake=no` (Q5);
- an **answer** is any cmd entry from A to the person with an id above D's: a `reply`, or the
  directive that `report --to <person>` writes (`ReportNotice`, `bus/stream.go:495`).

One answer covers every earlier directive. A directive to `@all` is one directive per agent, so
each agent answers for itself, as the skill already says. A directive older than the oldest
retained cmd entry is not judged.

**The two stages.** The setting is `dispatch.answer_after` (default 10m, 0 = off), added to the
registry (`config/keys.go`).
1. **After `answer_after`,** the check warns. It wakes A once per D, with `FencedCmdOnce` under
   the mark `answer-wake:<A>` (episode = D's id), as `QuietAgentWakes` does:

   > Answer guard: the person's directive `<id>` ("<first 80 chars>") has no answer from you after
   > 10m. Answer on the bus: agentbus report `<A>` --to `<person>` "…", or agentbus reply --ref
   > `<id>` `<person>` "…".

   The dispatcher delivers it like any directive, so a working agent is woken once it is idle.
2. **After twice `answer_after`,** still unanswered, the person is told once per D. This uses
   `FencedNotifyOnceForPerson` (`bus/once.go:181`) under the mark `answer-person:<A>`:

   > Answer guard: `<A>` has not answered your directive `<id>` ("<first 80 chars>") for 20m
   > (`<state>`; no terminal, if so). Look at its pane in busmon.

   Its prefix, "Answer guard: ", is added beside `stallNoticePrefix`
   (`internal/dispatchsvc/notifyfacts.go:135`), so the person's notifications ring for it as they
   do for the stall guard.

**What it never does.** It never answers for the agent, types into a pane, or resends the
directive. An agent with no terminal still reaches stage 2: the person is told, and §6's notice
reaches master if it is a pop.

**Tests** (the stall guard's seam, `health` package, no Redis):
- an answered directive is not judged;
- an unanswered one warns at 10 min and its wake is owed once;
- the person's notice is owed at 20 min, once;
- a later answer clears both;
- an untyped (composed) directive is not judged (Q5 A);
- a directive past the retained history is not judged.

The dispatcher writes, fenced and once-only, are tested against Redis.

## 9. Build steps

One PR each, from `origin/dev`, in this order. Steps 1 and 4 are independent.

1. **§6, the stranded-pop notice:** dispatch, `popRetire`, plus the mark. Small; it stops the
   silence first.
2. **§3, close on merge:** bus script, merge-queue pass, `request review --commit/--more`, busmon
   detail, plan's `delivered`, and the skill line.
3. **§4, report `--task`/`--done`:** CLI, the `note` transition, skill line.
4. **§7, alert wording and CHANGED:** busmon only (uses `reported_at` once step 3 is in; before it,
   only `accepted_at`).
5. **§5, `board drift` and the sentinel skill's daily review.**
6. **§8, the answer guard:** the health check, the two once-only writes, the setting, the notice
   prefix. Independent of steps 1-5.

**Tests:**
- **§3, real git on a disposable repository** (the merge queue's own seam): a merge, a squash and a
  rebase. Each must close a `review` request and leave an `accepted` one open. `--more` sends the
  request back to `accepted`. A changed branch between read and write writes nothing. A lost lease
  is `NOTHOLDER`. A review on a branch with no commit beyond the base is refused by the CLI; one
  whose head is an ancestor of its `review_base`, or that has no `review_base`, stays open.
- **§6:** a closed pop that owns a request gets exactly one notice per episode. A live pop gets
  none, and so does a closed pop that owns nothing.
- **§4:** the target-only refusal, and `--done` refused on a request that was not accepted.
- **§7:** `boardAlert` cases on `review`, `blocked` and `reported_at`.
- **Redis cases** use `AGENTBUS_TEST_REDIS_URL` only.

## 10. Questions for the person

**Q1. A multi-step request whose step in review merges.**
- **(A) recommended:** `request review --more` marks a step that is not the last. Its merge sends
  the request back to `accepted`, and the step is recorded. Without `--more`, the merge closes the
  request. A forgotten `--more` closes a build too early; §5 does not catch that, but master sees it
  in the plan.
- **(B)** Any merge of a request in `review` closes it. Multi-step builds use one request per step.
- **(C)** Never close on merge. Only report (§5).

**Q2. How a report names its request.**
- **(A) recommended:** flags only: `--task <t>` and `--done`. Deterministic, nothing guessed.
- **(B)** As (A), plus a report whose first line starts with the name of an open request targeted
  at the reporter counts as `--task` (never as `--done`).
- **(C)** Read "done" or "delivered" in the text and close the request. Free text misfires (for
  example "not done", or "done with step 1").

**Q3. Who hears of a close on merge.**
- **(A) recommended:** nobody is woken. An event goes to the stream, and busmon shows "closed on
  merge".
- **(B)** A reply to the sender (master), as `request done` does. Master is woken on every merge.

**Q4. A closed pop that owns open requests (§6).**
- **(A) recommended:** tell master once per episode, with the `pop --resume` line. Master brings it
  back.
- **(B)** The dispatcher relaunches it itself through self-repair. This reverses the decision that
  "a pop comes back through master only" (2026-11-09) and needs that decision reopened.

**Q5. Which of the person's directives must be answered (§8).**
- **(A) recommended:** only those the person typed (`typed = yes`). A directive that agentbus
  composed in their name, such as a resume summary or a close, needs no answer.
- **(B)** Every directive with `human = yes`, composed ones included.
