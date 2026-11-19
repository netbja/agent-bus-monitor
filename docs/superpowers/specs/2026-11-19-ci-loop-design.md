# A failed check goes back to its author, a few times, then to the person (ci-loop)

Status: spec, r4, not built. r2: every write of one action in one script (reviewer-codex), and the owner rule's source and fields spelled out. r3 (reviewer-codex): a Redis error inside the script does not undo earlier writes. The types are preflighted, each part's progress is recorded, and the retry writes only what is missing. The guarantee is stated separately for our process stopping and for Redis refusing a command. r4 (foureyes): a hand-off only once the head's CI has finished; self-addressed verdicts ignored; master's swap key checked too; a held owner does not hold the stop; the declared state is named; the latest of two same-named checks wins. Idea 1 of
[the agent-orchestrator study](../../memory/open-questions/2026-11-19-ci-loop-and-status-ideas.md).
The person said yes to a CI loop. It must stay simple and robust: when a PR's checks fail, a
bounded loop hands the failure to the author, with a retry cap and a stop to the person. §8 lists
the questions for the person; nothing in §8 is decided here.

## 1. What exists, and what is missing

| Already built | Where |
|---|---|
| A pass every 5 minutes (20 s budget) that runs `gh pr list` once and records the open PRs, the approved ones as the merge queue | `cmd/agentbus/mergequeuechore.go`, `mergequeue/forge.go` (`ReadGitHub`) |
| The forge's free text, cleaned: one line, no control characters, redacted | `mergequeue.cleanText` |
| A dispatcher directive sent once per episode, fenced, waking its target the usual way | `bus.FencedCmdOnce` (`bus/once.go`) |
| A notice to the person, once per episode, fenced | `bus.FencedNotifyOnceForPerson` |
| Who wrote a head: the verdict ledger's `author` on every verdict that names that `head` | `bus.Verdict` |
| An agent's state (`blocked`, `waiting`, `working`, `idle`) | `{p}:agents` |

Missing: the check results are never read; nobody is told when a PR's checks fail.

**Scope.** GitHub with `gh` only, the forge the pass already reads. GitLab, Bitbucket and the
local forge have no PR reader yet (`mergequeuechore.go`, the git fallback). They get no CI loop and
no error; §8 Q7 asks whether they should get one. This repository has no GitHub Actions, so here
the loop stays silent: it is for the person's other projects.

## 2. Reading the checks

The same `gh pr list` call gets one more field, `statusCheckRollup`. There is no new process, no
new cadence and no new credential: `gh` uses its own credentials, as today.

A PR's checks are **red** when at least one entry has failed:

- a check run (`__typename` `CheckRun`) whose `status` is `COMPLETED` and whose `conclusion` is
  `FAILURE`, `TIMED_OUT` or `STARTUP_FAILURE`;
- a commit status (`StatusContext`) whose `state` is `FAILURE` or `ERROR`.

Anything else does not count as a failure:

- `PENDING`, `QUEUED` or `IN_PROGRESS`;
- `SUCCESS`, `NEUTRAL` or `SKIPPED`;
- `CANCELLED` and `ACTION_REQUIRED`, which §8 Q4 asks about.

**The head's CI must be finished.** The loop acts on a PR only when its rollup has **no entry still
running**: no check run that is not `COMPLETED`, and no commit status that is `PENDING` or
`EXPECTED` (foureyes, r2). Then one push gives one hand-off, which lists every failure at once.
Checks that fail one by one would otherwise make a new episode each, and use the cap on a single
head. A PR whose CI is red but still running waits for the next pass.

If a check never finishes, the PR is never handed off. That is the forge's problem to show, not this
loop's.

**Duplicate names.** A re-run can leave two rollup entries with the same name. The latest wins
(`completedAt`, else `startedAt`, for a check run; `createdAt` for a status), so a re-run that
passed is not counted red.

Each failing entry keeps two fields, both cleaned with `cleanText`:

- its name: `name` for a check run, `context` for a commit status;
- its URL: `detailsUrl`, or `targetUrl` for a commit status.

The list is sorted by name, and at most 10 are kept, plus "and N more". **The log is never read.**
No CI output reaches the bus, Redis or a pane. The author reads the log itself, from the URL or
with `gh run view --log-failed`.

A draft PR is skipped. If the read fails (timeout, gh missing, a cut list), the pass sends nothing,
the same as when the merge queue cannot be read; that is never taken as "all green".

## 3. The owner

**Where the data comes from.** The owner is read from the verdict ledger, `{p}:verdicts`. That is a
Redis stream, read with `bus.Verdicts(ctx, "")`, which the pass already calls each time for the
merge queue (`vs` in `deriveRun`). There is no further read, no forge call and no subprocess.

**Which field.** Each ledger entry has two names:

- `author`: the agent that opened the challenge, which is the one that wrote the change;
- `reviewer`: the agent that gave the verdict.

`head` is the full sha the reviewer read (`verdict --head`). The owner rule reads `author` and
ignores `reviewer`. If two reviewers approve one head, both entries still carry the same author:
the coder who challenged them. That counts as one author, not several.

**A self-addressed verdict is ignored** (`reviewer == author`, which some reviewers have written).
`author` is the name the reviewer types; it is not copied from the challenge. Ignoring these follows
`mergequeue/derive.go` (`candidates`) and `bus/verdicts.go` (foureyes, r2).

**The rule.** For the PR's `headRefOid`, take the set of distinct `author` values over the ledger
entries whose `head` equals it and whose `reviewer` is not the `author`:

- one author: that agent is the owner;
- none, meaning no entry names that head (a head pushed without a review, or one whose entries the
  ledger has trimmed): master, meaning the services' escalation target, as `escalateTo` reads it
  today;
- two or more: master. Two agents each challenged the same commit, for example a coder and docs
  agent on one shared branch. Neither is "the" author, and master knows who did what. This is
  rare, and sending it to master is safe.

In this team every pushed head is a reviewed head: master pushes only after both approvals. So in
the usual case there is exactly one author, the coder, and the ledger holds the answer with no new
record.

**The board is not used.** A request records a branch only when it is done (`--branch`), and only
sometimes; the ledger records a head on every approve.

**Master is kept informed, without being woken.** The owner's directive is copied to master with
`wake=no` (Q1). Master dispatches the work, so it must know a PR went back to its author. When the
owner is master, there is no copy.

## 4. The loop

Episode: the PR number, the head sha, and the sorted names of the failing checks.
For example: `pr:42 3f2a…c1 build,test-linux`.

On each pass, for each red PR:

1. **Same episode as last time:** nothing. A re-run of the same head with the same failures sends
   nothing again.
2. **Hand-offs already at the cap, `ci.max_handoffs` (default 3, Q2):** this is **the stop**. The
   person gets one notice for the PR (§5), master gets a directive with `wake=no`, and the owner gets
   nothing more. A held owner does not hold the stop, because the stop writes nothing to the owner
   (foureyes, r2). Otherwise, if master were the owner and `waiting` on an ask, the person's notice
   could wait without end.
3. **A hand-off, owner `blocked` or `waiting`:** nothing this pass. The next pass after the state
   ends sends it. This is the state the agent **declared**, in `{p}:agents`: `waiting` for a
   person's decision, `blocked` for a 4-eyes gate. It is not busmon's screen reading. Whether the
   owner's pane may be woken stays the dispatcher's usual rule, and the entry waits in the stream
   until then.
4. **Otherwise:** one directive to the owner, which wakes it, and the hand-off count goes up by one.

The count is kept per PR, across heads. A fix is pushed as a new head; if that head fails again, it
counts toward the same cap. A green pass does not reset the count (Q3).

The directive reads:

> CI failed on PR #42 (branch `fix/x`, head `3f2a…c1`), hand-off 1 of 3: `build`
> (https://…), `test-linux` (https://…). Read the log from the link or with
> `gh run view --log-failed`. Fix it on the branch, have the fix reviewed as usual, and report to
> master. The loop stops after hand-off 3 and gives the PR to the person.

A pass whose PR is no longer open (merged, closed) deletes that PR's record, so a reopened PR, a
rare case, starts at zero.

### Writes: one fenced script, with the progress of each part recorded

One action (a hand-off or a stop) is written by **one Lua script**, fenced by the lease token like
every dispatcher write. The script is `bus.FencedCIStep`, in `bus/`. It does not call the existing
once-writers one after another.

The action has parts:

- a hand-off: the owner's entry, and master's copy (`wake=no`) unless the owner is master;
- a stop: master's directive (`wake=no`), and the person's notice (`for=person`).

**Two kinds of failure, two guarantees.**

- **Our process stops** (a crash, a lost lease, a timeout of the pass). Redis runs a script with no
  other command in between, and our process cannot stop it halfway. The stop falls before the script
  or after it, and the action is written whole or not at all.
- **Redis refuses a command inside the script.** Redis does **not** undo the commands a script
  already ran when a later one errors. So the script can stop with some parts written and others
  not. It is built so that this case loses no part and duplicates at most one entry. It is also
  reported, never hidden.

**The record** is `{p}:ci`, a hash with one field per PR (`pr:<n>`). Its value is
`episode|count|stopped|done`:

- `done` is the set of parts already written for that episode (`o` owner, `m` master, `n` notice);
- the script reads and writes it, never a Go struct round-trip;
- times are Redis `TIME`.

**Keys:**

- `{p}:dispatch`, the lease;
- `{p}:ci`;
- the `cmd` stream;
- the `notify` stream;
- the owner's swap key, `{p}:swap:<owner>`, and master's, `{p}:swap:<master>`.

**Arguments:**

- the token;
- the field;
- the episode;
- the cap;
- the owner, master, and whether the owner is master;
- the three texts, validated in Go first (text, names, types, as `onceCmdFields` does).

**In order:**

1. If the lease is not the token, return `not-holder`.
2. **Preflight the types.** If `{p}:ci` is not a hash and not absent, or a stream key is not a
   stream and not absent, return `bad-type`. That covers a key a hand or another tool wrote with
   another type, the case where a later XADD or HSET would fail.
3. Read the field.
   - If its episode equals the argument and every part of its action is in `done`, return `same`.
   - If it is stopped and its stop is complete, return `stopped-before`.
4. If any recipient of a part not yet written is being swapped, return `swap`: the owner for the
   owner's part, master for master's part (on a hand-off and on a stop) (foureyes, r2). The script
   takes both swap keys, `{p}:swap:<owner>` and `{p}:swap:<master>`.
   The next pass tries again, as the once-writers do (`ErrSwapInProgress`).
5. **Decide, once per episode.** If the field's episode is not the argument, this is a new action:
   - a stop if the count is at or above the cap, else a hand-off;
   - HSET the field to `episode|count'|stopped'|` with nothing done, before any entry, where count'
     is count+1 for a hand-off and stays count for a stop.

   If the field's episode equals the argument, the action is the one already recorded and
   unfinished. Its count is not decided again.
6. **Each missing part:** XADD it, then at once HSET the field with that part added to `done`.
7. Return `sent` or `stopped`.

Steps 1 to 4 write nothing. The held case (§4 step 3, a hand-off to an owner that is `blocked` or `waiting`) is decided
in Go before the call, so it writes nothing.

**Retry and recovery.** A script that errors at step 5 or 6 leaves a record of exactly what it did:
the decision, and the parts listed in `done`. The pass returns the error, and the merge-queue
record and health name it ("ci: write failed: …"). The next pass, 5 minutes later, sees the same
red episode. Step 5 finds the action already decided, and step 6 writes only the parts not in
`done`.

- **No part is lost** while the PR stays red with the same episode.
- **Each part is written once**, except one case: Redis takes the XADD, then refuses the HSET
  right after it. Then that one part is written again on the retry. With the types checked at step 2,
  what remains is the server refusing a write under `maxmemory` with no eviction. A duplicate
  hand-off is harmless: the owner reads the same failure twice.
- If the episode changes while an action is unfinished (a new head, or other failing checks), the
  new episode's action replaces it. Its entries describe the failure as it now stands, and the
  missing parts of the old action are not sent. The count already includes the old action's
  hand-off.

**Stated guarantee.**

- If our process is interrupted, an action is written whole or not at all.
- If Redis refuses a command, no part is lost while the episode lasts, at most one entry is
  duplicated, and the error is reported.

The person's `ci reset` is the only other writer: a script that deletes the field.

## 5. The stop, to the person

The notice is sent once per PR, at the stop:

> myproject: PR #42 still fails CI after 3 hand-offs (`build`, `test-linux`): the loop stopped; the
> PR is yours to decide (fix it yourself, close it, or `agentbus ci reset 42` to give the author 3
> more tries).

It appears in For you, as the other dispatcher notices for the person do.

After the stop, the loop sends nothing more for that PR, whatever its checks do. It resumes only
when:

- the PR closes and its record is deleted;
- the person runs `agentbus ci reset <pr>` (Q5). That command deletes the PR's field with a small
  script and needs no lease, the same way `agentbus run reset` works (`bus.ResetRun`): only the
  person may run it, and an agent caller is refused.

## 6. What stays as it is

- **The dispatcher never runs repository code.** `gh` reads the forge only, as the pass does today.
- **The dispatcher types only the wake line.** The failure text goes on the bus, never into a pane.
- **No conflict loop in this step.** A conflicting PR is already in the merge queue for master,
  `Conflicting`, to be rebased. Q6 asks whether a conflict should follow the same loop.
- **No review-comment loop.** Our reviews already reach the author on the bus. Comments a person
  leaves on the forge are deferred, as the study says.
- **No new process, cadence or token.**

## 7. Settings, tests, step

Settings, in `config/keys.go`, read by `dispatch`:

- `ci.loop`, on or off, default on (Q2).
- `ci.max_handoffs`, an int, default 3 (Q2); 0 means never hand off, so every red PR goes straight
  to the stop.

Tests, with a fake `gh` and the disposable broker:

- the rollup parsing for each status and conclusion above;
- a pending check sends nothing, even beside a failed one; once all are complete, one hand-off lists every failure;
- same head, the failing set grows while checks finish: one hand-off, and the count moves once;
- duplicate check names: the latest entry wins;
- a self-addressed verdict is not an author;
- a held owner holds a hand-off but not a stop;
- master being swapped holds the stop;
- the same episode sends once;
- a new head sends again and counts;
- the cap stops the loop, and stops it once only;
- a `blocked` owner is held, then sent;
- an unknown owner and a two-author head go to master;
- a closed PR deletes its record;
- `ci reset` re-opens the loop;
- the fenced script refuses a stale token and writes nothing;
- a swap in progress writes nothing, and the next pass sends;
- a hand-off writes the owner's entry, master's copy and the record. A pass stopped before the script writes nothing, and the next pass writes all of it. A pass stopped after it writes nothing more (`same`);
- a wrong-typed `{p}:ci`, cmd or notify key gives `bad-type` and writes nothing;
- a script failure injected after the owner's XADD and its `done` mark: the retry writes master's copy only, the decision is not taken again, and the count does not move;
- an episode that changes while its action is unfinished: the new action is decided, and the old one's missing part is not sent;
- the owner rule: one author over two reviewers is that author; no entry, or two authors, is master;
- no log field and no output of the command ever reaches an entry.

One build step, one PR: the read, the record, the loop, the stop, `ci reset`, an operator-notes
fragment. One condition: measure the time `gh pr list` takes with the rollup, on one of the
person's projects with real checks and with many open PRs (`ghLimit` is 1000), against the 10 s per-call budget before it ships. If it does not
fit, the rollup becomes its own `gh` call in the same pass. That is still bounded, and it is
decided at build time, not guessed now.

## 8. Questions for the person

1. **Who is told?** The author, woken, with a copy to master that does not wake it (proposed).
   Or master only, who re-dispatches.
2. **Cap and default.** 3 hand-offs per PR, loop on by default (proposed). Or another number, or
   off until you turn it on per project.
3. **Does green reset the count?** No (proposed): a PR that goes red, green, then red again has used
   2 of its 3. Or a green pass gives back the full count.
4. **`CANCELLED` and `ACTION_REQUIRED`.** Not failures (proposed): a cancel is usually a newer run
   replacing the old one, and an action-required check waits for a person. Or treat either one as a
   failure.
5. **After the stop.** `agentbus ci reset <pr>` re-opens the loop (proposed). Or the stop is final
   for that PR until it closes.
6. **Conflicts.** Left to master, through the merge queue, as today (proposed). Or a conflict with
   the base also goes to the author with the same cap.
7. **GitLab and Bitbucket.** No loop until they have a PR reader (proposed). Or build their readers
   now, as a separate step.
8. **The merge queue.** Should an approved PR whose checks are red be marked in the queue, so you do
   not merge it by hand? Proposed: yes, a "CI red" mark on its row, a small second step after this one.
