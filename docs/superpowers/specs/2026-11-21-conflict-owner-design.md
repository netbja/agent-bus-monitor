# A conflicting approved PR goes back to its owner, git only

Status: spec r2, for review (subject `conflict-owner`). Not built. Base: origin/dev `4f095697`.
r2 fixes the following (reviewer-codex, foureyes):
- the episode is the person's (PR, head, base);
- `FencedCmdOnce` writes each episode's hand-off;
- the CI script's write order is stated correctly;
- custom merge drivers are disabled;
- local mode is named.

r3 adds an `escalated` state that stops every later hand-off, and fixes the cap's timing
(reviewer-codex).

r4 runs `merge-tree` in an isolated empty repository, because `--attr-source` alone still let
`merge.default`, `.git/info/attributes` and `core.attributesFile` pick a custom driver. It also
gates the age bound on a positive duration (reviewer-codex).
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
| `FetchBase` (`:66`), `InBase` (`:78`), `exitTrue` (`:103`), `run` (`mergequeue/forge.go:97`) | `mergequeue/git.go` | The git calls' pattern: they run in the services' directory, with the chore's hardened env (`mergeQueueEnv`, `:126`), and write no ref. |
| `RemoteTips`, `LocalTips` | `mergequeue/git.go:19`, `:29` | The approved branches and their heads, in the git fallback. |
| `ciStepScript` (`:57`), `FencedCIStep` (`:122`) | `bus/ci.go` | **The pattern, not the code.** Per PR, one field holds `digest\|count\|stopped\|done`, with one hand-off per new episode, counted up to a cap, then a stop. The script writes the decision (the count) before any entry. Each part is then `XADD`ed and its done mark set after it (`:102-114`). The script runs atomically, so no crash falls between an entry and its mark. A swap in progress defers the write. Step 1 does not reuse this script (§4). |
| `FencedCIForget` | `bus/ci.go:194` | Forgets a PR that left the queue. |
| `RunCI`'s `held` | `mergequeue/ciloop.go:166` | An owner who is `blocked` or `waiting` (declared states) gets no hand-off. |
| `CIDigest` | `mergequeue/ciloop.go:39` | The episode's dedupe key. |
| `bus.FencedCmdOnce` | `bus/once.go:52` | **Every directive of this spec**: the owner's hand-off once per episode, master's copy, and the escalation once per conflict. The entry and its mark are set in one script, so each is exactly once. |
| `BoardEntry.Branch` (`bus/board.go:29`), `Bus.Board` (`:36`), `RequestInfo.Branch` (`bus/requests.go:31`) | `bus/` | **The owner** (§3). |
| `CIOwner` | `mergequeue/ciloop.go:55` | **Not used.** It reads the verdict ledger, and the person wants the board. |

## 2. Detection: `git merge-tree` against the base

**Candidates.** These are the merge queue's approved heads not yet in the base: `Result.Items`,
plus `Result.Conflicting` (the forge's flag) in the gh path, and the approved tips in the git
fallback. Nothing else is checked: a PR still in review is not in the queue (question 3).

**Objects.**
- **Every mode that pushes** (GitHub, GitLab, Bitbucket, a remote without a forge reader): the
  pass already fetches the base tip (`FetchBase`, `mergequeuechore.go:300`). A candidate head
  missing from the local object store is fetched the same way, with the same flags
  (`--no-write-fetch-head --no-tags --refmap=`): one `git fetch origin <branch>…` for all the
  candidates. That writes no ref.
- **Local mode** (`forge.Local`): nothing is fetched. The base tip and the heads come from
  `LocalTips`, the local branches, which are already in the store.
- **Either way,** a head still absent is skipped this pass, with a reason in the record. It is
  never judged "clean".

**The check** runs in an **isolated repository**: an empty bare repository made for the pass in
the services' state directory, whose `objects/info/alternates` points at the services'
repository's object store. It runs with only `PATH`, plus `HOME=<that directory>`,
`GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_ATTR_NOSYSTEM=1`:

`git --git-dir=<isolated> --attr-source=<empty tree> merge-tree --write-tree --name-only --no-messages <baseTip> <head>`

`<empty tree>` is `git hash-object -t tree /dev/null`. The directory is removed after the pass, so
the merge's objects never land in the real repository.
- Exit status 0: clean.
- Exit status 1: conflict. The conflicting files are the lines after the tree id.
- Any other status: not judged, with a reason in the record.

`--write-tree` needs git 2.38 or later, and `--attr-source` needs 2.42. One probe per pass
checks both: `git --attr-source=<empty tree> merge-tree -h`. It is judged by its output, not its
exit status, which is 0 on a recent git. On a git older than 2.42, an "unknown option" error means
too old. Otherwise `--write-tree` must appear in the usage text. An older git records the reason "git
too old for the conflict check" and sends nothing.

**No repository code runs.** Like `git merge`, `merge-tree` can run an external merge driver,
`merge.<name>.driver`. It is chosen by a `merge=<name>` attribute or by `merge.default`. Checked on
git 2.56 in the real repository, the driver ran each time:
- from the tree's `.gitattributes`;
- from `merge.default`, even with `--attr-source=<empty tree>`;
- from `.git/info/attributes`;
- from `core.attributesFile`.

In the isolated repository, none of those can reach it:
- no local config;
- no global or system config, and no system attributes;
- no `info/attributes`;
- an empty attribute source.

The check there still exited 1 with the file named, and the driver did not run. A control run
in the real repository did run it. `merge-tree` runs no hook and writes no ref. The rule that
the dispatcher never runs repository code holds.

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
(`:362`). Neither is required. Question 4 asks whether naming the branch becomes a must.

## 4. The hand-off, the hold and the escalation

**An episode** is (the PR, or the branch in the git fallback; the head; the base tip), as the
person defined it. Its digest is `CIDigest("conflict", <pr or branch>, head, baseTip)`.

**One directive per episode, through `bus.FencedCmdOnce`**, the person's choice. Each write is an
entry and its mark in one script, so it is sent exactly once:
- **the hand-off:** mark `conflict:<pr or branch digest>`, episode = the digest, to the owner;
- **master's copy:** mark `conflict-copy:<…>`, the same episode, `wake=no`, unless master is the
  owner;
- **the escalation:** mark `conflict-esc:<…>`, episode = the conflict's `first` (below), to
  master. It is waking, unlike the CI loop's stop, which is a `wake=no` directive plus a person
  notice: here master is expected to act.

**The count, the age and the escalation** live in a new hash `{p}:conflicts`, one field per PR
(or branch digest): `last|count|first|escalated`. A small fenced script, `FencedConflictTrack`
(`bus/conflict.go`), updates it under the dispatch lease:
- `first` is set from Redis `TIME` when the field is created;
- with `count`, it raises `count` and sets `last` to the digest, but only when `last` differs
  from the digest;
- with `escalate`, it sets `escalated` to 1;
- a field already `escalated` is never counted again;
- it returns the field.

The pass calls the track script before `FencedCmdOnce`. A crash between the two leaves the
count raised and the mark unset. The next pass then finds `last` equal to the digest, so it
counts nothing again, and `FencedCmdOnce` sends the missing hand-off.

**The pass, per candidate,** in this order:

1. **Clean, or no longer a candidate** (merged, closed, gone): its `{p}:conflicts` field is
   removed in one fenced script, as `FencedCIForget` does for the CI record. So are its three
   `FencedCmdOnce` marks, which are fields of `{p}:quota` (`QuotaKey`), not of
   `{p}:conflicts`. A later conflict on the same PR starts afresh.
2. **Already escalated** (`escalated = 1`): no hand-off to the owner, whatever the episode, until
   step 1 removes the field, whether the escalation came from the age or the cap. The only call is
   the escalation's own `FencedCmdOnce`, which sends nothing when its mark is set (the crash case
   below).
3. **The age bound reached:** only when `conflict.escalate_after` is positive, and
   `now - first ≥ conflict.escalate_after` in Redis time. Then escalate, below. With 0 there is
   no time bound: only the cap escalates.
4. **A new episode** (`last` differs from the digest):
   - **the owner is `blocked` or `waiting`** (`held`, as in `RunCI`): track without counting, so
     `first` is set, and send nothing. The age still runs;
   - **`count < conflict.max_handoffs`:** track with counting, then `FencedCmdOnce` sends the
     hand-off (`Hand-off <count> of <cap>`) and master's copy:

     > `PR #<n> (<branch>) at <head> conflicts with <base> at <base tip>: <files>. Rebase it onto origin/<base>, resolve, push; the reviewers then approve the new head. Hand-off <k> of <cap>.`

   - **`count ≥ conflict.max_handoffs`:** escalate. **The cap's timing,** as in the CI loop: with
     a cap of 3, three hand-offs go out. The fourth conflicting episode escalates instead of a
     fourth hand-off. A cap of 0 escalates the first episode.
5. **The same episode** (`last` equals the digest): nothing new. After a crash, `FencedCmdOnce`
   resends only what is missing (§4, above).

**Escalating** is: track with `escalate`, then `FencedCmdOnce` to master (mark
`conflict-esc:<…>`, episode = `first`), waking: `PR #<n> (<branch>) still conflicts with <base> after <k> hand-off(s) / <age>; owner <owner>. Take it over or reassign it.`
A crash between the two leaves `escalated` set and the mark unset. The next pass reaches step 2,
whose `FencedCmdOnce` then sends it, once.

**Following the person's episode,** a base that moves while the conflict stays is a new episode,
sent and counted. With many merges a day into `dev`, the cap can be reached within hours.
`conflict.max_handoffs` is where the person loosens that.

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
    `--write-tree` or `--attr-source` (a reason, nothing sent);
  - local mode: nothing is fetched, and the local tips are used;
  - the gh path: a forge-flagged PR that `merge-tree` finds clean stays an item;
  - the git fallback finds a conflict the forge never flagged;
  - no ref is written: refs and `FETCH_HEAD` are compared before and after.
- **Owner:**
  - one board entry by claim branch, one by request branch, and two entries with one owner each
    give that owner;
  - two owners, none, or an invalid name give master.
- **No repository code:** a configured driver that touches a file, selected four ways:
  - the tree's `.gitattributes`;
  - `merge.default` in the repository's config;
  - `.git/info/attributes`;
  - `core.attributesFile` in a `HOME` config.

  For each, the check finds the conflict and the file is not created. A control run without the
  isolation creates it, which proves the test can see a driver run. The isolated directory is
  gone after the pass.
- **Track and send** (disposable broker only):
  - one hand-off per episode, with master's copy;
  - a crash after the track and before the send: the next pass sends once, and the count does
    not rise twice;
  - a new base tip or a new head: a new episode, sent and counted;
  - with a cap of 3: three hand-offs, then the fourth episode escalates; a cap of 0 escalates the
    first;
  - `conflict.escalate_after` = 0 with a cap of 3: no escalation by age, only at the cap;
  - an age escalation below the cap, then a new base or head episode: no hand-off to the owner;
  - a crash between `escalate` and its send: the next pass sends the escalation once;
  - a removed field's three `{p}:quota` marks are gone too;
  - a held owner: nothing sent, not counted, but `first` is set and the age escalates;
  - a swap in progress defers a `FencedCmdOnce`;
  - not the lease holder: nothing written;
  - a clean or vanished candidate's field and marks are removed.
- **Settings:** both keys are in the registry, with their env names, groups and invalid texts. A
  value of 0 for both gives master only.
- **Budget:** candidates beyond the budget are left for the next pass, with a reason, and the
  pass still writes its record.

## 7. Questions for the person

1. **The time bound while the owner is blocked or waiting:** does it keep running? Recommended:
   yes. Master can reassign while the owner is stuck.
2. **Defaults:** 3 hand-offs and 2 h? Recommended: yes.
3. **Approved PRs only:** a PR still in review is not checked in step 1. Recommended: yes. That
   is the queue's set, and the person's Q3 named approved PRs.
4. **Must the coder name its branch?** About 70 % of today's board entries name none, and those
   conflicts would go to master. Recommended: the coder and docs skills make
   `request done --branch` a must when a PR is opened.

## 8. Step 2, noted only

Failed checks go back to the owner, on GitHub only. This is the CI loop's hand-off, with a
timeout on the forge read and a pass cadence that slows when reads are slow. It gets its own spec
when the person asks. GitLab, Bitbucket and Gitea readers are not planned.
