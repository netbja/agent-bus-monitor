# A conflicting approved PR goes back to its owner, git only

Status: spec r1, for review (subject `conflict-owner`). Not built. Base: origin/dev `4f095697`.
Author: architect, from the person's directive of 2026-11-21 (thread `1795222170445-0`), relayed by
master (cmd `1795222242792-0`).

The person simplified the CI loop:
- the `ci-loop-timing` task is dropped;
- **step 1** (this spec): conflicts go back to the branch's owner, using git only;
- **step 2, later:** failed checks go back to the owner, on GitHub only. The forge read gets a
  timeout, and the cadence slows when reads are slow. It is not specified here;
- other forges (GitLab, Bitbucket, Gitea) wait until a real need appears.

This replaces the person's earlier answer to merge-conflicts Q3, "a conflict stays with master".
That spec's §5 is amended in the same PR.

## 1. What exists, and what is reused

These are the helpers searched for and reused:

| Existing | Where | Use |
|---|---|---|
| The merge queue's pass | `internal/dispatchsvc/mergequeuechore.go:155` (`pass`), `:240` (`deriveRun`) | The conflict step runs in the same pass, under the same dispatch lease, after `deriveRun` and before `FencedPutMergeQueue`. |
| `Derive` and `Result.Conflicting` | `mergequeue/derive.go:95`, `:117` | Today `Conflicting` comes only from the forge's own flag (`PR.Conflicting`, gh path). The git fallback never sets it. Step 1 confirms or finds conflicts with git, in every mode. |
| `FetchBase`, `InBase`, `run`, `exitTrue` | `mergequeue/git.go:66`, `:78`, `:103` | The git calls' pattern: they run in the services' directory, with the chore's hardened env (`mergeQueueEnv`, `:126`), and write no ref. |
| `RemoteTips`, `LocalTips` | `mergequeue/git.go:19`, `:29` | The approved branches and their heads, in the git fallback. |
| `FencedCIStep` and its script | `bus/ci.go:57`, `:122` | **The model.** Per PR, one field holds `digest\|count\|stopped\|done`. There is one hand-off per new episode, counted up to a cap, then a stop to master. Each part is written after a done mark, so a crashed pass resends only what is missing. A swap in progress defers the write. |
| `FencedCIForget` | `bus/ci.go:194` | Forgets a PR that left the queue. |
| `RunCI`'s `held` | `mergequeue/ciloop.go:166` | An owner who is `blocked` or `waiting` (declared states) gets no hand-off. |
| `CIDigest` | `mergequeue/ciloop.go:39` | The episode's dedupe key. |
| `bus.FencedCmdOnce` | `bus/once.go:52` | The escalation to master, once per conflict. |
| `Bus.Board`, `BoardEntry.Branch`, `RequestInfo.Branch` | `bus/board.go:29`, `:36`; `bus/requests.go:31` | **The owner** (§3). |
| `CIOwner` | `mergequeue/ciloop.go:55` | **Not used.** It reads the verdict ledger, and the person wants the board. |

## 2. Detection: `git merge-tree` against the base

**Candidates.** These are the merge queue's approved heads not yet in the base: `Result.Items`,
plus `Result.Conflicting` (the forge's flag) in the gh path, and the approved tips in the git
fallback. Nothing else is checked: a PR still in review is not in the queue (question 4).

**Objects.** The base tip is fetched already (`FetchBase`). A candidate head that is not in the
local object store is fetched the same way: the same flags (`--no-write-fetch-head --no-tags
--refmap=`), one `git fetch origin <branch>…` for all the candidates. That writes no ref. A head
still absent afterwards is skipped this pass, with a reason in the record, and is never judged
"clean".

**The check:** `git merge-tree --write-tree --name-only --no-messages <baseTip> <head>`.
- Exit status 0: clean.
- Exit status 1: conflict. The conflicting files are the lines after the tree id.
- Any other status: not judged, with a reason in the record.

`--write-tree` needs git 2.38 or later. One `git merge-tree -h` probe per pass tells the old
form from the new. An older git records the reason "git too old for the conflict check" and
sends nothing.

`merge-tree` runs no hook and writes no ref. It adds loose objects, which `git gc` collects. It
runs git's own merge only, never repository code. That keeps the rule that the dispatcher never
runs repository code.

**The record.** `MergeQueueRecord.Conflicting` gains additive fields: `branch`, `head`,
`base_tip` and `files` (the first 10). From step 1 on, it holds what git confirmed, in every mode.
- A PR the forge flags but `merge-tree` finds clean is an item, not a conflict: forges' flags lag.
- A clean approved head stays an item.

**Budget.** The pass's 20 s (`mergeQueueBudget`) has to fit the new git calls. Each `merge-tree`
call gets the chore's 10 s call timeout. The candidates are checked oldest-approved first, and
those not reached are left for the next pass (5 minutes later) with a reason. The queue rarely
holds more than a few items.

## 3. The owner: from the board

A board entry names a branch in one of two ways:
- `agentbus board claim <task> --branch <b>` sets `BoardEntry.Branch`;
- `agentbus request done <task> --branch <b>` sets `BoardEntry.Request.Branch`.

On this project's board today, 305 entries hold 10 claim branches and 92 request branches.

**The rule:** the owner of a conflicting branch is the `Owner` of every board entry whose
`Branch` or `Request.Branch` equals the branch's name exactly.
- **One distinct owner:** that agent.
- **None, several, or an owner that is not a valid agent name:** master, the services'
  escalation target, as the CI loop uses it.

A done entry still names its owner: that is the usual case, since a PR is opened once the request
is done.

The agent-bus skill shows both forms: `board claim <task> --branch <role>/<task>`
(`skills/agent-bus/SKILL.md:276`) and, for a mission, `request done … --branch`
(`:362`). Neither is required. Question 5 asks whether naming the branch becomes a must.

## 4. The hand-off, the hold and the escalation

**An episode** is (the PR, or the branch in the git fallback; the head; the base tip), as the
person defined it. Its digest is `CIDigest("conflict", <pr or branch>, head, baseTip)`. The
record is one field per PR (or branch digest) in a new hash `{p}:conflicts`, holding
`digest|head|count|first|stopped|done`:
- `first` is the time the conflict was first seen, from Redis `TIME` inside the script;
- `count` is how many hand-offs went out;
- `done` holds the parts written: `o` the owner's directive, `m` master's copy, `e` the
  escalation.

**The pass, per candidate:**

1. **Clean, or no longer a candidate** (merged, closed, gone): its field is forgotten, as
   `FencedCIForget` does.
2. **Conflict, and a new head** (or no field yet): a new hand-off. Its directive goes to the owner,
   counted:

   > `PR #<n> (<branch>) at <head> conflicts with <base> at <base tip>: <files>. Rebase it onto origin/<base>, resolve, push; the reviewers then approve the new head. Hand-off <k> of <cap>.`

   Master gets a copy with `wake=no`, as in the CI loop, unless master is the owner.
3. **Conflict, same head, newer base tip:** nothing more. The owner already holds a directive to
   rebase onto the current base. Only a new head that still conflicts counts as a new attempt
   (question 1).
4. **The owner is `blocked` or `waiting`:** no hand-off is written and nothing is counted. The
   time bound still runs (question 2).
5. **Escalation** happens once per conflict, through the same script's `e` part. The owner did
   not resolve it when either:
   - `count` reached `conflict.max_handoffs` and a new head still conflicts; or
   - `now - first ≥ conflict.escalate_after`.

   Master then gets one waking directive: `PR #<n> (<branch>) still conflicts with <base> after <k> hand-off(s) / <age>; owner <owner>. Take it over or reassign it.` After that, nothing more is sent for that conflict until it is resolved, which forgets the field.

**The script** is `FencedConflictStep` in `bus/conflict.go`. It is modelled on `ciStepScript`:
- the same lease check, type checks, done marks and swap deferral;
- plus `first` from `TIME`, the `e` part, and the "same head: nothing" rule.

The CI loop's script is not changed: the CI loop keeps its own record (`{p}:ci`).

**Settings** (registry, `ReadBy: "dispatch"`):

| Name | Kind | Default | Meaning |
|---|---|---|---|
| `conflict.max_handoffs` | Int | 3 | Hand-offs to the owner before master; 0 sends every conflict straight to master. |
| `conflict.escalate_after` | Duration | 2h | The age at which an unresolved conflict goes to master; 0 means no time bound. |

There is no on/off setting: with `max_handoffs` at 0 and the time bound at 0, the behaviour is
today's, master only.

## 5. What changes elsewhere

- `skills/agent-bus-master/SKILL.md`: a conflict reaches the owner first. Master gets copies,
  and acts on the escalation.
- `skills/agent-bus/SKILL.md`, for the coder: a conflict directive means rebase onto
  `origin/<base>`, resolve (rerere replays known hunks), test, push, and ask for review of the new
  head.
- `docs/operator-notes.d/conflict-owner.md`.
- **The merge-conflicts spec, §5, Q3:** its answer is amended in this PR.

## 6. Tests

- **Detection:**
  - with a real git in `t.TempDir()`: a clean approved head, a conflicting one (files named), a
    head absent and still absent after the fetch (skipped, with a reason), and a git without
    `--write-tree` (a reason, nothing sent);
  - the gh path: a forge-flagged PR that `merge-tree` finds clean stays an item;
  - the git fallback finds a conflict the forge never flagged;
  - no ref is written: refs and `FETCH_HEAD` are compared before and after.
- **Owner:**
  - one board entry by claim branch, one by request branch, and two entries with one owner each
    give that owner;
  - two owners, none, or an invalid name give master.
- **Script** (disposable broker only):
  - one directive per episode, with master's copy;
  - a crash between parts resends only the missing part;
  - same head and a new base tip: nothing;
  - a new conflicting head: the count rises;
  - escalation at the cap and at the age, once each;
  - a held owner: nothing, not counted, but the age still escalates;
  - a swap defers the write;
  - not the lease holder: nothing written;
  - a clean or vanished candidate's field is forgotten.
- **Settings:** both keys are in the registry, with their env names, groups and invalid texts. A
  value of 0 for both gives master only.
- **Budget:** candidates beyond the budget are left for the next pass, with a reason, and the
  pass still writes its record.

## 7. Questions for the person

1. **A base that moves under the same head:** no new directive, so only a new head that still
   conflicts counts as an attempt? Recommended: yes. Otherwise every merge into `dev` re-sends,
   and the cap is spent in an hour.
2. **The time bound while the owner is blocked or waiting:** does it keep running? Recommended:
   yes. Master can reassign while the owner is stuck.
3. **Defaults:** 3 hand-offs and 2 h? Recommended: yes.
4. **Approved PRs only:** a PR still in review is not checked in step 1. Recommended: yes. That
   is the queue's set, and the person's Q3 named approved PRs.
5. **Must the coder name its branch?** About 70 % of today's board entries name none, and those
   conflicts would go to master. Recommended: the coder and docs skills make
   `request done --branch` a must when a PR is opened.

## 8. Step 2, noted only

Failed checks go back to the owner, on GitHub only. This is the CI loop's hand-off, with a
timeout on the forge read and a pass cadence that slows when reads are slow. It gets its own spec
when the person asks. GitLab, Bitbucket and Gitea readers are not planned.
