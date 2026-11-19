# A failed check goes back to its author, a few times, then to the person (ci-loop)

Status: spec, r1, not built. Idea 1 of
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

A pending check never triggers anything. The loop acts on a failure that has completed, never on a
guess.

Each failing entry keeps two fields, both cleaned with `cleanText`:

- its name: `name` for a check run, `context` for a commit status;
- its URL: `detailsUrl`, or `targetUrl` for a commit status.

The list is sorted by name, and at most 10 are kept, plus "and N more". **The log is never read.**
No CI output reaches the bus, Redis or a pane. The author reads the log itself, from the URL or
with `gh run view --log-failed`.

A draft PR is skipped. If the read fails (timeout, gh missing, a cut list), the pass sends nothing,
the same as when the merge queue cannot be read; that is never taken as "all green".

## 3. The owner

The owner is the author of the head. For the PR's `headRefOid`, the verdict ledger is read for the
verdicts that name that head:

- one distinct `author`: that agent;
- none (a head nobody had reviewed), or several authors: master, meaning the services' escalation
  target, as `escalateTo` reads it today.

In this team every pushed head is a reviewed head: master pushes only after both approvals. So the
author is the coder who wrote the change, and the ledger holds the answer with no new record.

The board is not used. A request records a branch only when it is done (`--branch`), and only
sometimes; the ledger records a head on every approve.

**Master is kept informed, without being woken.** The owner's directive is copied to master with
`wake=no` (Q1). Master dispatches the work, and must know a PR went back to its author.

## 4. The loop

Episode: the PR number, the head sha, and the sorted names of the failing checks.
For example: `pr:42 3f2a…c1 build,test-linux`.

On each pass, for each red PR:

1. **Same episode as last time:** nothing. A re-run of the same head with the same failures sends
   nothing again.
2. **Owner `blocked` or `waiting`:** nothing this pass. The next pass after the state ends sends it.
   A dialog or a person's decision comes first.
3. **Hand-offs already at the cap, `ci.max_handoffs` (default 3, Q2):** this is **the stop**. The
   person gets one notice for the PR (§5), master gets a directive with `wake=no`, and the owner gets
   nothing more.
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

### Writes: one fenced script

The check, the mark, the entry and the count all go in **one Lua script**, fenced by the lease token
like every dispatcher write: `bus.FencedCIHandOff`, in `bus/`. If a pass crashes after the entry,
it can never send it again with no count, or count it with nothing sent. The record is
`{p}:ci`, one hash with one field per PR (`pr:<n>`). Each field holds its last episode, its count
and whether it stopped, in a form the script reads, never a Go struct round-trip. Times are Redis
`TIME`.

The script returns one of:

- `sent`
- `same` (no change)
- `stopped` (the cap, stop announced now)
- `stopped-before` (already stopped: nothing)
- `not-holder`

Master's copy and the person's notice use their existing once-writers with the same episode, so a
retried pass writes each one at most once.

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
- a pending check sends nothing;
- the same episode sends once;
- a new head sends again and counts;
- the cap stops the loop, and stops it once only;
- a `blocked` owner is held, then sent;
- an unknown owner and a two-author head go to master;
- a closed PR deletes its record;
- `ci reset` re-opens the loop;
- the fenced script refuses a stale token;
- no log field and no output of the command ever reaches an entry.

One build step, one PR: the read, the record, the loop, the stop, `ci reset`, an operator-notes
fragment. One condition: measure the time `gh pr list` takes with the rollup, on one of the
person's projects with real checks, against the 10 s per-call budget before it ships. If it does not
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
