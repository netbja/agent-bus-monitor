# Missions: a deterministic conductor for large changes (v0.10)

- **Status:** rev 3 (Codex re-check: operator commands go through the conductor, no forced reload over an approval or a merge in progress, an exact supersession protocol). Rev 2 (Codex spec review folded in: request names, gate closing, conductor primitives, Git locking, integration resume, superseded attempts, recovery by hash). Rev 1: Approach A was chosen by the user on 2026-10-11 ("ok pour A"), and design
  sections 1 to 4 were approved one by one.
- **Base:** `main` @ d0edbff (v0.9.0). It ships with v0.10, alongside the memory tooling (PR #47).
  The CLI/ACP drivers move to v0.11.
- **Inputs:**
  - the user's answers: the target is a generic method; the four pains are a saturated
    coordinator, multi-day resumption, parallel work without breakage, and guaranteed scope;
    automation is a deterministic bus; validation is mechanical checks plus a 4-eyes review;
  - Codex's independent opinion (`.superpowers/brainstorm/bigprojects/codex.md`);
  - a comparison with Claude Code agent teams (§2.2).

## 1. Problem

Large changes run today the way v0.8 to v0.10 ran: brainstorm, then spec, then plan. After
that, one coordinator session (Claude) cuts briefs, sends them to Kimi or Codex, reads the
reports, launches the reviews and decides what comes next. This has four costs:

1. **The coordinator saturates.** Every hand-off passes through one LLM context, which
   re-explains each task. When that session ends, the work stops.
2. **Resumption across days is fragile.** "Who does what, on which commit, and what is
   validated" lives in that context and in a git-ignored ledger.
3. **Parallel work is unsafe by default.** Nothing stops two tasks that share a file or an
   interface from running together.
4. **Scope is a promise, not a check.** Nothing verifies that a delivered commit touches only
   what the task allowed.

Codex's experience report adds one more cost: the costliest failures were context drifting
between artefacts, and reviews made against a revision that was already stale.

## 2. Decision

### 2.1 What we build

The plan stays in Git, and the bus runs it. A change's tasks become **missions** described in a
versioned `missions.json`. A deterministic **conductor**, `agentbus missions`, uses no LLM. It
does the following:

- dispatches each mission once its dependencies are integrated;
- checks every delivery mechanically (scope and tests);
- opens a 4-eyes review on another agent;
- integrates approved commits into the change branch.

Master only arbitrates: blocked missions, repeated rejections, scope changes.

The bus never holds its own copy of the plan. It points at a commit and stores a hash. This
answers Codex's main risk, double authority (spec, board and contract drifting apart while
each looks coherent on its own).

### 2.2 What we deliberately do not reuse, and when to use it instead

- **Claude Code agent teams** (experimental) already provide a shared task list with
  dependencies, automatic unblocking, locked claiming and a `TaskCompleted` quality-gate hook.
  We reuse their vocabulary and their semantics: pending, then in progress, then completed;
  locked claiming; a blocking completion check. They do not fit here, for four reasons:
  - teammates can only be Claude Code sessions;
  - a team is bound to one session: in-process teammates are not restored by `/resume`, there
    is one team per session and the lead is fixed;
  - the lead is an LLM coordinating by conversation, which is the bottleneck we are removing;
  - they keep no review ledger and have no integration branch.

  **Use agent teams, not missions, for a short, Claude-only change.**
- **GitHub (PR per mission, CI, required reviews, merge queue)** covers checks and
  integration. Two things rule it out here:
  - all agents act under one GitHub account, so an independent approval cannot be required;
  - each mission would cost a push and remote CI minutes.

  The final merge to `main` keeps going through a normal PR.
- **OpenSpec** is not added. The existing brainstorm, spec and plan flow already covers it, and
  a second scope source is exactly the double-authority risk (Codex).
- **Not built:** a generic workflow or DAG engine, plan extraction by an LLM, an auto-merge to
  `main`, any push, and nested orchestration. The dependency graph is read from one file, and
  nothing else is decided by the conductor.

## 3. The change and its missions

### 3.1 Files in Git

A change lives in `docs/changes/<change>/`. `<change>` must match `bus.ValidName`. The folder
holds three files:

- `spec.md` and `plan.md` (or links to them): the human-readable scope, as today.
- `missions.json`: the machine-readable cut of the plan. The plan's author writes it in the same
  commit as the plan. JSON was chosen because the standard library reads it, so no dependency is
  needed.

```json
{
  "change": "busmon-state",
  "base": "main",
  "branch": "change/busmon-state",
  "reviewer": "codex",
  "test": "go test ./... -count=1",
  "max_attempts": 3,
  "missions": [
    { "id": "t1", "title": "Characterisation tests for the feed",
      "agent": "kimi",
      "allowed_paths": ["cmd/busmon/*_test.go"],
      "test": "go test ./cmd/busmon/ -count=1",
      "brief": "plan.md#task-1" },
    { "id": "t3", "title": "Extract the state model out of rendering",
      "agent": "coder", "reviewer": "kimi", "depends_on": ["t1"],
      "allowed_paths": ["cmd/busmon/state*.go", "cmd/busmon/*_test.go"],
      "test": "go test ./cmd/busmon/ -count=1", "timeout": "4h",
      "brief": "plan.md#task-3" }
  ]
}
```

**Change-level fields:**

| Field | Required | Default |
|---|---|---|
| `change` | yes | — (`ValidName`, at most 16 characters: it prefixes request names, §4) |
| `base` | no | `main` |
| `branch` | no | `change/<change>` |
| `reviewer` | yes | — (default reviewer for every mission) |
| `test` | no | the suite run after every integration; none if absent |
| `max_attempts` | no | 3 |

**Mission fields:**

| Field | Required | Default / meaning |
|---|---|---|
| `id` | yes | `^[a-z0-9][a-z0-9-]{0,7}$` (lowercase: it is part of a request name, §4) |
| `title` | yes | — |
| `agent` | yes | — |
| `reviewer` | no | the change's reviewer |
| `depends_on` | no | none |
| `allowed_paths` | yes | at least one |
| `test` | yes | — |
| `timeout` | no | `4h`, counted from acceptance |
| `brief` | yes | a path relative to the change folder, optionally with an anchor |

### 3.2 Validation (`mission load` refuses on any failure, writes nothing)

`mission load` refuses the file, and writes nothing, when any of these holds:

- the JSON is malformed or has unknown fields;
- a mission id appears twice;
- a dependency is unknown, or the dependencies form a cycle;
- an agent or reviewer fails `ValidName`;
- **a mission's reviewer is its own agent;**
- `brief` points at a file that is absent at the loaded commit;
- **two missions that are not ordered by dependencies have overlapping `allowed_paths`.**

**Pattern syntax.** `allowed_paths` uses `path.Match` syntax per path segment, plus a final
`/**` that means "anything below".

**When two patterns overlap.** They overlap when either of these holds:

- a file of the repository at the loaded commit matches both;
- or, for files that do not exist yet, one pattern's literal prefix (up to its first wildcard)
  is a prefix of the other's.

This test is deliberately conservative. A shared interface is not independent: when in doubt,
order the two missions with a dependency.

### 3.3 Loading and revisions

`agentbus mission load docs/changes/<change>/missions.json@<commit>` does the following:

- reads the file with `git show <commit>:<path>`, never from the working tree;
- stores the change, the revision (1, 2, …), the file's hash and a hash per mission;
- creates the local branch `branch` from `base` if it does not exist yet.

Reloading at a new commit creates the next revision (§6.4). The conductor only ever acts on the
current revision.

## 4. Mission lifecycle

| State | Meaning | Next |
|---|---|---|
| `pending` | waiting for dependencies | `ready` when every dependency is `integrated` |
| `ready` | can be requested | `requested` |
| `requested` | a bus request was sent to the agent | `accepted`, or `blocked` |
| `accepted` | the agent is working | `delivered`, `blocked`, or a timeout escalation |
| `delivered` | the agent ran `request done --commit` | `checking` |
| `checking` | the conductor checks scope and tests | `in_review`, or `rejected` |
| `in_review` | a challenge is open on the reviewer | `approved`, or `rejected` |
| `approved` | an independent approve was recorded for this commit | `integrating` |
| `integrating` | merging into the change branch | `integrated`, or `blocked` |
| `integrated` | in the change branch; dependents unlock | terminal |
| `rejected` | checks failed or the review rejected | a new attempt → `requested`, or `blocked` after `max_attempts` |
| `blocked` | needs master | `ready` (`retry`), `integrated` (`skip`), or `aborted` |
| `aborted` | the change was aborted | terminal |

**`mission.Next`.** It is a pure function, `Next(state, event) → (state, actions)`, in the
spirit of `dispatch.Decide`. Every transition goes through it, and it has a full
state × event test table.

**One attempt, one request.** Each attempt is one bus request. This is needed because the
existing request protocol refuses to reuse a task name. The request's task name, which is also
the challenge ref, is `<change>-<id>-<attempt>`, for example `busmon-state-t3-2`.

- It must pass `bus.ValidName` (`^[a-z][a-z0-9_-]{0,31}$`), which `Bus.Request` and every
  transition enforce.
- So §3.1 bounds `change` to 16 characters, `id` to `^[a-z0-9][a-z0-9-]{0,7}$`, and attempts
  to 99.
- `mission load` checks that the longest possible name fits.
- The conductor never parses the name: the mission record stores it.

**Attempts are closed explicitly.** A new attempt (after a rejection, `retry`, `reassign` or a
reload) closes the previous one. The protocol is precise:

1. The mission record marks the old task `superseded`. The record's `current_task` is the
   only task whose `accept` or `done` the conductor acts on.
2. The board entry of the old task is set to state `superseded` through a fenced write. The
   request metadata is left as it was. busmon counts every tracked request whose state is
   not `done` as waiting (`request.waiting()` in `cmd/busmon/tracking.go`). Slice 1 therefore
   also excludes `superseded` there, so a superseded request stops being shown as waiting.
3. The conductor sends one `FencedCmd` to the old agent:
   - type `reply`;
   - `ref` set to the old request's thread (`RequestInfo.Thread`);
   - text: `superseded by <new task>: stop, do not deliver`;
   - `wake=no`.

   The agent is not woken for it. If it is working, the reply reaches it at its next drain, and
   if the request is still queued, the agent sees both entries in the same drain. The
   dispatcher's behaviour is unchanged: it wakes on unread entries, not on board states.
4. A later `accept` or `done` on the superseded task moves no mission state. The conductor sends
   the same reply once more, at most once per such transition.

Nothing from a superseded attempt is ever checked or integrated.

**What the request body carries.** It holds references only:

- the objective (the title);
- `base=<sha>`, the head of the change branch when the mission became ready;
- `rev=<n>`;
- the brief as `<path>@<commit>#<anchor>`;
- on a retry, the previous attempt's rejection (check output or verdict id).

The agent reads the brief at that commit. Nothing from the plan is copied into Redis.

## 5. Components

### 5.1 Package `mission/`

- `Parse` and `Validate` (§3.2), and `Next` (§4).
- Redis access through the existing `bus` client.
  - **Keys:**
    - `{p}:change:{c}`: a hash holding path, commit, file hash, revision, base, branch and
      state;
    - `{p}:missions:{c}`: a hash mapping mission id → a JSON record. The record holds state,
      attempt, the request task, the base commit, the delivered commit and branch, check
      results and log path, the verdict subject, and timestamps from Redis `TIME`;
    - `{p}:conductor`: the conductor lease.
  - **Project scanning.** The first two keys have two colons, so they cannot forge a project,
    as with `{p}:wake:{agent}`. `conductor` joins `projectKinds`.
  - **New `bus` primitives, in `bus/conductorkeys.go`.** The dispatcher's are bound to
    `DispatchKey`, and `OpenChallenge` is a plain `HSET`, so the conductor needs its own:
    - `AcquireConductor`, `RenewConductor`, `ReleaseConductor` and `ConductorAlive`;
    - `FencedMissionPut` (a mission record), `FencedChangePut`, `FencedRequest` (the
      request's `XADD` plus its board entry), `FencedChallenge` (the gate `HSET` plus its
      `cmd`), `FencedResolveChallenge` and `FencedCmd` (replies and escalations).

    Each one is a Lua script that first checks `GET {p}:conductor == token`. No conductor write
    goes around them.
- `RequestInfo` gains two optional fields, `commit` and `branch`, filled by `request done
  --commit <sha> [--branch <b>]`. Both are additive. A done without `--commit` on a mission
  task is rejected by the conductor (§6.2).

### 5.2 The conductor: `agentbus missions [--repo DIR]`

- **One per project.** It runs under the lease `{p}:conductor` (`PX 15000`, renewed every 5 s,
  the dispatcher's mechanism). It runs in its own herdr tab: the bootstrap template gains a
  `missions` tab, and `--no-missions` omits it. `missions --status` prints `missions up` or
  `missions down`.
- **It reconciles; it does not remember.** Every 2 s, and on any new `{p}:cmd` entry, it
  recomputes from Redis and Git what each mission needs. It holds no durable local state, so a
  restart loses nothing (§6.3).
- **The Redis lease does not stop Git, so a local lock does.** For its whole life, the
  conductor holds an exclusive `flock` on `~/.local/state/agentbus/<p>/<c>/conductor.lock`.
  Two conductors on one machine can therefore never run `git` or tests on the same change, even
  in the window where a lease has lapsed. Every `git` and test command runs in its own process
  group, under a context cancelled when a renewal fails. Losing the lease kills the commands in
  flight, and the conductor exits: it does not try to finish an integration it no longer owns.
- **It never runs an LLM, and never pushes or touches `main`.** The dispatcher keeps its rule
  that it never runs code. The conductor is the one process that runs `git` and the test
  commands, and it is a separate process on purpose.
- **Checks** (`delivered` → `checking`), in a disposable `git worktree` of the delivered commit
  under `~/.local/state/agentbus/<p>/<c>/check-<mission>.<attempt>/`:
  1. the commit exists and descends from the attempt's `base`;
  2. every path in `git diff --name-only base..commit` matches one of the mission's
     `allowed_paths`; offenders are listed;
  3. the mission's `test` runs through `sh -c` in that worktree, with a 20-minute limit, and
     the output goes to a log file. Only the exit code and the last 40 lines (≤ 4 KiB) reach
     the bus.

  The worktree is removed afterwards. The test command always comes from `missions.json` at the
  loaded commit, never from the agent.
- **Review** (`checking` → `in_review`). The conductor opens a challenge on the reviewer with
  `FencedChallenge`. Its ref is the attempt's task name. Its body holds the mission reference,
  the commit, the log path and the exact command to answer:

  ```
  agentbus verdict --subject mission:<c>/<id>@<sha12> --ref <c>-<id>-<attempt> <agent> <approve|reject> <message>
  ```

  `--ref` closes the reviewer's gate, the existing behaviour of `verdict`. The mission moves on
  only on the roll-up of **that subject**. The existing rule applies: APPROVED when the latest
  independent approve is newer than any reject. A verdict on another commit counts for nothing,
  so a review of a stale revision cannot validate a new one. Whenever a mission leaves
  `in_review` for any reason, the conductor also calls `FencedResolveChallenge` on that ref, so
  a forgotten `--ref` never leaves the reviewer gated.
- **Integration** (`approved` → `integrated`). Integrations are serialised, one worktree of the
  change branch under `~/.local/state/agentbus/<p>/<c>/integration/`. The conductor runs:

  ```
  git merge --no-ff <commit> -m "mission <c>/<id> (rev <n>): <title>" \
    --trailer "Mission: <c>/<id>" --trailer "Mission-Commit: <sha>" \
    --trailer "Mission-Rev: <n>" --trailer "Mission-Hash: <mission hash>"
  ```

  Before merging, it records `pre_head` (the branch head) in the mission record. Then it runs
  the change's `test`, if one is set, and records `tested_head` (the merge commit) and the
  result. Only a green result, recorded for the current branch head, makes the mission
  `integrated`.

  On a conflict or a red suite, it does three things:
  - it runs `git merge --abort`, or `git reset --hard <pre_head>`, in its own integration
    worktree;
  - it puts the mission in `blocked`;
  - it escalates.
- **Escalation** uses the dispatcher's channel: a `cmd` of type `escalation` to `master` (or to
  `--escalate-to`). The text names the mission, the reason and the next command to run.

### 5.3 `agentbus mission …` (master or the user)

```
mission load <path>@<commit> [--force <reason>]   # first load, or reload as revision n+1 (§6.4)
mission load --recover <path>@<commit>            # rebuild after losing Redis (§6.5); no --force
mission status [<change>]
mission show <change>/<id>                         # the mission, its brief reference, its attempts
mission retry <change>/<id>                        # blocked → a new attempt
mission reassign <change>/<id> <agent>             # blocked or accepted → a new attempt for <agent>
mission skip <change>/<id> --reason <text>
mission abort <change> --reason <text>
```

`retry` and `reassign` always open a new attempt and supersede the current one (§4).

**Who writes: the conductor, always.** Only the lease holder may change a change or a
mission, so the CLI never writes mission state itself.

- **Reads.** `status` and `show` read Redis directly.
- **Operator commands.** `load`, `retry`, `reassign`, `skip` and `abort` are sent to the
  conductor as entries in a dedicated stream, `{p}:missionops`, which joins `projectKinds`. Each
  entry holds `op`, its arguments, `from` (`AGENT_BUS_AGENT`) and a request id. The cmd stream
  is not used, so the dispatcher never treats an operation as an agent's obligation.
- **How the conductor handles them.** It reads the stream through its own consumer group and
  applies the operations one at a time, between reconciliation passes, with its fenced writes.
  For `load`, it reads `git show` itself. It then appends a `result` entry (`ok`, or the error)
  that carries the request id.
- **What the CLI does.** It waits for that result (30 s, `--wait` to change it), prints it and
  exits 0 or 1. With no live conductor (`ConductorAlive` false), it refuses at once:
  `missions down: start agentbus missions`. With a timeout, it says the operation is queued and
  not yet applied. It never claims success it did not see.
- **Audit.** The operations stream is also the audit trail of every operator action.

`skip` integrates nothing: it marks the mission `integrated` without a commit, so dependents
unlock. It records a self-verdict with the reason, so the act is in the ledger. `abort` stops
the conductor's work on the change and leaves the branch and the worktrees in place.

### 5.4 The agent side

Agents keep the existing verbs:

```
agentbus request accept <task>
agentbus request block <task> <reason>
agentbus request done <task> --commit <sha> [--branch <b>] <summary>
```

The peer and master skills gain a short "missions" section. An agent:

1. reads the brief at the given commit;
2. works in its own worktree, on a branch created from `base`;
3. commits;
4. delivers with `--commit`.

A reviewer answers its challenge with a verdict on the exact subject.

### 5.5 busmon

Missions already show in Board and Requests, because they are requests. The header gains
`missions ●/○` next to `dispatch ●/○`.

## 6. Errors and resumption

### 6.1 Silent agents and reviewers

| Situation | Behaviour |
|---|---|
| A request is not accepted | the v0.8 dispatcher wakes the agent and escalates after 10 minutes (unchanged) |
| A mission is accepted but not delivered within `timeout` | it becomes `blocked` (reason `timeout`) and is escalated; master runs `retry` or `reassign`, which supersede the attempt (§4); no automatic reassignment |
| `request block` | the mission becomes `blocked`, and master gets the reason |
| The reviewer is silent | the open challenge is an obligation: the dispatcher wakes the reviewer and escalates |

### 6.2 Bad deliveries (rejected without spending a review)

The conductor rejects a delivery when:

- `done` has no `--commit`;
- the commit is unknown, or does not descend from `base`;
- the diff touches files outside the allowed paths (they are listed);
- the tests are red (the log tail is attached);
- it comes from a superseded attempt (§4): ignored rather than rejected, so it costs no
  attempt.

Each rejection is one attempt. After `max_attempts`, the mission is `blocked` and escalated.

### 6.3 Conductor restarts

The delivered commit is the idempotence key.

- **At startup, the conductor cleans its integration worktree.**
  - If a merge is in progress (`MERGE_HEAD` exists), it runs `git merge --abort`.
  - If the worktree holds changes it did not record, or untracked files, it does not delete
    them: it blocks the change and escalates.
- **A mission found in `checking`** is checked again from scratch, in a new disposable
  worktree.
- **A mission found in `integrating`** is resumed from the record. Being an ancestor of the
  branch is not enough:
  - if the commit is not in the branch, the merge is redone from `pre_head`;
  - if it is in the branch, the change's `test` must be green and recorded for the current head
    (`tested_head`), otherwise the suite is run again;
  - only then does the mission become `integrated`. A red suite blocks it.
- A conductor that lost its lease writes nothing, and its commands were killed (§5.2).
- A second conductor for the same project exits 0, as a second dispatcher does.

### 6.4 Reloading the plan

`mission load` at a new commit creates revision n + 1.

- A mission whose hash did not change keeps its state.
- A changed mission that has not started (`pending`, `ready`) is simply replaced.
- A changed mission whose attempt is in flight (`requested` through `in_review`) makes the
  reload fail, unless `--force <reason>` is given. With it, the attempt is superseded (§4): any
  later accept or done on it is ignored, and its agent is told to stop. The mission then goes
  back to `pending`. A queued request can still be read, but it can no longer produce anything.
- A changed or removed mission in `approved` or `integrating` makes the reload fail, **even with
  `--force`**. An approval or a merge in progress is never cancelled by a reload: wait until the
  mission is `integrated` or `blocked`, or `abort` the change.
- A changed or removed mission that is `integrated` makes the reload fail, unless
  `--force <reason>` is given. Its merge is history: it stays `integrated`. The record keeps
  `integrated_under` (the revision and hash it was merged with) and the reason. New work needs a
  new mission. Integrated work is never rewritten or re-run silently.
- A new mission starts as `pending`.

### 6.5 Losing Redis

`mission load --recover <path>@<commit>` reads the trailers of the change branch's merge commits:

- a mission whose `Mission-Hash` equals its current definition's hash becomes `integrated`;
- a mission merged under a different hash (its definition changed since) becomes `blocked`,
  waiting for master: an old merge never validates a new definition;
- every other mission becomes `pending`.

Git is the durable proof, and Redis the working state.

### 6.6 Disk and logs

Check worktrees are removed after use. Logs live under `~/.local/state/agentbus/<p>/<c>/logs/`,
outside the repository. The oldest logs are pruned past 200 MiB per change.

### 6.7 Limits accepted in v1

- **Same machine.** The conductor and the agents share one local repository: a commit must be
  in it. Remote agents (VDR) are out of scope.
- **Trust.** Running a mission's tests means executing, as the conductor's user, whatever code
  the agent committed: `sh -c` in a worktree of that commit. This is arbitrary code execution on
  the host, bounded only by what that user can do. The rules that follow from it:
  - only load a `missions.json` from a commit you have reviewed;
  - run the conductor as an ordinary user, with no secrets in its environment beyond what the
    tests need (no deploy keys, no production Redis URL);
  - agents and the conductor already share one machine and one account, so this adds no new
    privilege, but it does not sandbox anything either.
- **Who reviews.** The reviewer is a role, not a guarantee: the 4-eyes rule ensures another
  agent approved, not that it read well.

## 7. Testing

- **Unit:** `Parse`/`Validate` table (every refusal of §3.2, including overlap at the loaded
  tree and the prefix rule); `Next` full state × event table; `allowed_paths` matching.
- **Redis** (isolated `AGENTBUS_TEST_REDIS_URL` only): atomic transitions, the fenced writes
  after a lost lease, two conductors.
- **End-to-end:** a temporary Git repository, agents simulated by `agentbus request
  accept/done` calls, and test commands `true` and `false`, with no LLM and no Python. The
  scenarios are:
  - three missions: two in parallel and one depending on both;
  - an out-of-scope rejection;
  - a red-test rejection;
  - `max_attempts` reached, which escalates;
  - an integration conflict, which escalates;
  - a restart during checking, and another during integration;
  - a reload with a changed mission;
  - `--recover` after the Redis keys are deleted, including a mission merged under an older
    hash, which must come back `blocked`;
  - a verdict on the wrong commit being ignored, and the reviewer's gate closed after every
    exit from `in_review`;
  - an `accept` and a `done` on a superseded attempt being ignored;
  - a lost lease killing a running test, and two conductors on one machine (the `flock`);
  - a start with a merge in progress, and one with unknown files, in the integration worktree;
  - a crash after the merge but before the change suite, which must rerun the suite;
  - a mission id or change name too long for a request name, refused at load.
  - an operator command with no conductor running (refused), one that times out (reported as
    queued), and a reload refused over `approved`/`integrating` even with `--force`;
  - a superseded request no longer counted as waiting, and its agent not woken by the reply.
- **Live acceptance:** port the launcher (`scripts/lib/role_config.py`) to Go as a real
  change. Kimi and Codex run it as missions, master only arbitrates, and the result is
  recorded in the release notes.

## 8. Delivery slices (one spec, two plans)

1. **Slice 1, the full loop:**
   - the `mission/` package, `mission load|status|show`, and `request done --commit`;
   - the conductor with checks, review, integration and the escalations;
   - restart idempotence;
   - `retry` and `abort`.
2. **Slice 2, operations:**
   - revisions on reload and `--recover`;
   - `skip` and `reassign`;
   - mission timeouts;
   - the busmon `missions ●/○` indicator and the bootstrap `missions` tab;
   - the skill sections;
   - then the live acceptance.

v0.10.0 is tagged after slice 2 and the memory tooling are both merged.

## 9. Risks and open points

- **Overlap rule false positives.** The rule may force more dependencies than needed. That is
  accepted: it costs parallelism, never correctness.
- **Plan authors must write `missions.json`.** The writing-plans step gains this output. An
  incoherent file is refused at load time, never half-run.
- **Two long-running processes per project** (dispatcher and conductor). They stay separate, so
  the dispatcher keeps its "never runs code" rule. Merging them would be a later decision with
  its own spec.
- **Open: a bridge to agent teams.** A `TaskCompleted` hook could call `agentbus mission`, so
  that Claude teammates can take part. This is not in v0.10.

## 10. Known minors carried into slice 2 (from the slice-1 reviews)

Codex reviewed two checkpoints and Kimi did the final review. All their critical and important
findings are fixed. Six minor findings remain open:

- **`abort` ordering.** `abort` marks the change aborted before it transitions its missions. A crash
  between the two leaves requests waiting until `mission abort` is run again. Fix: transition the
  missions first, or have startup finish the job.
- **Transient git errors.** A failed `ResolveCommit` always rejects the delivery. A transient git
  error should be retried as an infrastructure error, as the checks already are.
- **Verdict subject length.** The subject uses a 12-hex prefix of the sha, a 48-bit collision space.
  Consider 16 hex characters or the full sha.
- **Late replies on an aborted change.** An agent that accepts or delivers an attempt of an
  aborted change gets no reply, because `Pass` skips aborted changes.
- **Effects that write to a stream are at-least-once.** This covers the supersede reply and the
  escalation. A crash between the `XADD` and the flag write repeats the effect once. Either
  document this, or make the write and the marker one Lua script.
- **Dirty worktree at startup.** A dirty integration worktree with no mission integrating is not
  blocked at startup. It is caught, and now repaired live, when the next integration fails.
