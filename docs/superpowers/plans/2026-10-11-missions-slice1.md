# Missions slice 1 — the full loop (load → request → check → review → integrate) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build slice 1 of the missions spec. A deterministic conductor (`agentbus missions`) runs a loaded `missions.json` end to end. It sends requests, checks scope and tests, opens the 4-eyes review and integrates approved commits into the change branch. It survives restarts. Master can `load`, `retry` and `abort`.

**Architecture:**
- **`mission/`** holds the pure parts: the model, validation, path matching and the `Next` state machine. It also holds three I/O parts:
  - the store (typed records over new fenced `bus` primitives);
  - `Git` (every `git` and test command, each in its own process group);
  - the conductor's reconciliation pass and its `Run` loop.
- **`bus/conductorkeys.go`** adds the conductor lease and its fenced writes, built like the dispatcher's.
- **`cmd/agentbus`** only parses arguments.
- **Operator commands** go through the `{p}:missionops` stream. Only the conductor writes mission state.

**Tech Stack:** Go 1.26, `github.com/redis/go-redis/v9` (already a dependency), the system `git` (2.55 here), `golang.org/x/sys` is NOT added (use `syscall.Flock`, Linux only, as the rest of the repo).

**Spec:** `docs/superpowers/specs/2026-10-11-missions-design.md` (rev 3). Slice 1 = §8 item 1.

## Global Constraints

- No LLM, no network, no `git push`, never touch `main` or any branch except `change/<c>` and the agent branches it only reads.
- Every conductor write to Redis checks `GET {p}:conductor == token` in Lua (§5.1). Persisted times come from Redis `TIME`, never the local clock.
- Names:
  - task/ref names are `<change>-<id>-<attempt>` and must pass `bus.ValidName` (`^[a-z][a-z0-9_-]{0,31}$`);
  - `change` passes `ValidName` and is at most 16 characters;
  - `id` matches `^[a-z0-9][a-z0-9-]{0,7}$`;
  - attempts are 1 to 99.
- Verdict subject: `mission:<change>/<id>@<sha12>`. The conductor's bus identity: `conductor`. The escalation target defaults to `master`.
- Lease: `{p}:conductor`, `PX 15000`, renewed every 5 s. Tick: 2 s. Check test limit: 20 min. Log tail on the bus: the last 40 lines, at most 4 KiB.
- Local state root: `~/.local/state/agentbus/<p>/<c>/` holds `conductor.lock`, `integration/`, `check-<task>/` and `logs/`. `XDG_STATE_HOME` overrides `~/.local/state`.
- Defaults: `base` `main`, `branch` `change/<change>`, `max_attempts` 3.
- Tests use only `AGENTBUS_TEST_REDIS_URL` (the isolated broker); Redis cases skip without it. Git tests use `t.TempDir()` repositories.
- No Python. User-facing text in English. Commits end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Not in slice 1:**
  - `reassign`, `skip` and mission timeouts;
  - reload revisions (a second `load` of a known change is accepted only when its file hash is identical, and is otherwise refused with "reload is slice 2");
  - `--recover`;
  - the busmon header indicator, the bootstrap tab and the skill sections.

## Review Focus

1. **A conductor's own inbox entries must never look like an obligation that wakes it.** Agents' `request done` replies and `blocked` notices are addressed to `conductor`, so the conductor must hold its receiver lease and drain them (Task 9).
2. **A delivery whose commit is not in the shared repository** must be a rejection with a reason, not a crash and not an endless re-check: this covers a typo'd sha and a branch that only exists in a worktree that was never committed (Task 7).
3. **A test command that prints megabytes, hangs, or forks a background child** must be bounded: the log goes to a file, the 20-minute limit applies, and the whole process group is killed (Task 6).
4. **A verdict whose subject has the wrong sha, or whose reviewer is the author,** must leave the mission `in_review` (Task 7).
5. **Two conductors on one machine,** or one whose lease lapsed mid-merge, must not both touch the integration worktree (Task 9).

---

## File Structure

| File | Responsibility |
|---|---|
| `bus/requests.go` | `request done` records `commit`/`branch` (additive) |
| `bus/verdicts.go` (new) | `RollUp` moved from `cmd/agentbus/verdicts.go` |
| `bus/conductorkeys.go` (new) | conductor lease and fenced writes, missionops stream |
| `bus/projects.go` | `conductor`, `missionops` join `projectKinds` |
| `cmd/busmon/tracking.go` | `superseded` is not waiting |
| `mission/model.go`, `validate.go`, `paths.go` | `File`, `Mission`, `Parse`, `Validate`, `MatchPath`, `Overlap` |
| `mission/state.go` | `State`, `Event`, `Action`, `Next` |
| `mission/store.go` | `ChangeRecord`, `MissionRecord`, `Store` over the fenced primitives; op queue client |
| `mission/git.go` | `Git`: every `git` call and `RunTest`, process groups, context-cancelled |
| `mission/conductor.go`, `integrate.go`, `ops.go` | one reconciliation pass; integration and startup cleanup; op handling |
| `mission/run.go` | `Run`: flock, lease, renewal, inbox watcher, tick |
| `cmd/agentbus/missions.go` | `agentbus missions [--repo DIR] [--escalate-to A] [--status]` and `agentbus mission load|status|show|retry|abort` |

---

### Task 1: Bus additions the conductor needs

**Files:**
- Modify: `bus/requests.go` (`CompleteRequest`, `transitionRequest`, `RequestInfo`), `cmd/agentbus/requests.go` (`done` flags), `bus/projects.go`, `cmd/busmon/tracking.go:85`, `cmd/agentbus/verdicts.go` (use `bus.RollUp`)
- Create: `bus/verdicts.go`
- Test: `bus/requests_test.go`, `bus/verdicts_test.go`, `cmd/busmon/tracking_test.go`, `bus/projects_test.go`

**Interfaces — Produces:**
- `RequestInfo.Commit string \`json:"commit,omitempty"\``, `RequestInfo.Branch string \`json:"branch,omitempty"\``
- `func (b *Bus) CompleteRequestAt(ctx context.Context, task, agent, body, commit, branch string) (string, error)`. `CompleteRequest` calls it with empty commit and branch. The Lua script receives two more `ARGV` and sets `r.commit` / `r.branch` only when non-empty. On a repeated `done`, the result is the existing `response_id` and commit/branch are not overwritten.
- CLI: `request done <task> [--commit <sha>] [--branch <b>] <reply>`. `--commit` must match `^[0-9a-f]{7,40}$` (otherwise usage, exit 2). `--branch` passes `git check-ref-format --branch` rules: no spaces, no `..`, no leading `-`.
- `func RollUp(vs []Verdict) (state string, deciderIdx int)` in `bus`. It moves from `cmd/agentbus`, with its body and comment unchanged.
- `projectKinds` gains `"conductor": true, "missionops": true`.
- busmon: `func (r request) waiting() bool { return r.State != "done" && r.State != "superseded" }`.

- [ ] **Step 1: Failing tests**:
  - `TestCompleteRequestRecordsCommit` (Redis): `Request` → `AcceptRequest` → `CompleteRequestAt(.., "abc1234", "mission/x")`. The board entry has `request.commit == "abc1234"` and `request.branch == "mission/x"`. A second `CompleteRequestAt` with another commit returns the same response id and keeps `abc1234`.
  - `TestRollUpMoved`: the table from `cmd/agentbus/verdicts_test.go`, now against `bus.RollUp`.
  - `TestProjectFromKeyConductorKinds`: `p:conductor` and `p:missionops` give project `p`, and `p:missions:c` and `p:change:c` give none.
  - `TestSupersededIsNotWaiting`: `request{State:"superseded", Tracked:true}.waiting() == false`, and `waitingCount` ignores it.
  - `TestRequestDoneFlags` (cmd/agentbus): bad `--commit` exits 2, and good flags reach `CompleteRequestAt`. The latter is a Redis case.
- [ ] **Step 2: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./bus/ ./cmd/agentbus/ ./cmd/busmon/ -run 'CompleteRequestRecords|RollUp|ConductorKinds|Superseded|RequestDoneFlags' -count=1` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** the same and `go test ./... -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(bus): request done records commit/branch; RollUp in bus; conductor key kinds`.

---

### Task 2: `mission` model, validation and path matching

**Files:**
- Create: `mission/model.go`, `mission/validate.go`, `mission/paths.go`
- Test: `mission/validate_test.go`, `mission/paths_test.go`, `mission/testdata/*.json`

**Interfaces — Produces:**
```go
type File struct {
    Change      string    `json:"change"`
    Base        string    `json:"base"`
    Branch      string    `json:"branch"`
    Reviewer    string    `json:"reviewer"`
    Test        string    `json:"test"`
    MaxAttempts int       `json:"max_attempts"`
    Missions    []Mission `json:"missions"`
}
type Mission struct {
    ID, Title, Agent, Reviewer, Test, Timeout, Brief string
    DependsOn    []string `json:"depends_on"`
    AllowedPaths []string `json:"allowed_paths"`
}   // JSON tags: id, title, agent, reviewer, test, timeout, brief
func Parse(data []byte) (File, error)              // DisallowUnknownFields; fills defaults (§3.1)
func Validate(f File, tree []string, briefExists func(path string) bool) error // every §3.2 rule; one error listing all problems
func (f File) ReviewerOf(m Mission) string
func (m Mission) Hash() string                    // sha256 hex of the mission's canonical JSON (sorted keys via json.Marshal of the struct)
func FileHash(data []byte) string                 // sha256 hex of the raw bytes
func TaskName(change, id string, attempt int) string // "<change>-<id>-<attempt>"
func MatchPath(pattern, path string) bool         // path.Match per segment; a final "/**" matches anything below
func Overlap(a, b []string, tree []string) bool   // §3.2: a tree file matches both, or one literal prefix is a prefix of the other
func DependsTransitively(f File, a, b string) bool
```
`Validate` also checks that `TaskName(change, id, 99)` passes `bus.ValidName` for every mission. `tree` is the repository's file list at the loaded commit, passed in so `Validate` stays pure.

- [ ] **Step 1: Failing tests**:
  - `TestParseDefaults`: `base` becomes `main`, `branch` becomes `change/<c>`, and `max_attempts` becomes 3.
  - `TestValidateRefusals`: a table with one fixture per rule. The rules are: unknown field, duplicate id, unknown dependency, cycle `a→b→a`, a `ValidName` failure on an agent, reviewer == agent, a missing brief, an upper-case id, a 17-character change, overlapping `allowed_paths` without a dependency, and an empty `allowed_paths`. Each asserts that the error names the offending mission.
  - `TestOverlapOrderedIsAllowed`: the same paths with `depends_on` are accepted.
  - `TestMatchPath`, as a table:

    | pattern | path | match |
    |---|---|---|
    | `cmd/busmon/state*.go` | `cmd/busmon/state_model.go` | yes |
    | `cmd/busmon/state*.go` | `cmd/busmon/sub/state.go` | no |
    | `docs/**` | `docs/a/b.md` | yes |
    | `docs/**` | `docsx/a` | no |
  - `TestOverlapPrefixRule`: `cmd/busmon/new*.go` and `cmd/busmon/**` overlap with an empty tree, while `cmd/a/*` and `cmd/b/*` do not.
  - `TestTaskName`: `TaskName("busmon-state","t3",2) == "busmon-state-t3-2"`.
- [ ] **Step 2: Run** `go test ./mission/ -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement.** Cycle detection is a DFS. `Overlap` is checked only for pairs where `!DependsTransitively` holds in either direction.
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): missions.json model, validation and path rules`.

---

### Task 3: The state machine `Next`

**Files:** Create `mission/state.go`. Test: `mission/state_test.go`.

**Interfaces — Produces:**
```go
type State string // pending ready requested accepted delivered checking in_review approved integrating integrated rejected blocked aborted
type Event string // deps_integrated request_sent accepted agent_blocked delivered check_started check_failed check_passed approved review_rejected integrate_started integrated integrate_failed retry abort
type Action string // send_request supersede_attempt run_check open_review resolve_review integrate escalate
type Ctx struct{ RejectionsLeft int } // max_attempts − rejections so far in this round
func Next(s State, ev Event, c Ctx) (State, []Action, error) // error = event not allowed in s
```
The transition table is exact. Any pair not listed returns an error and leaves the state unchanged.

| From | Event | To | Actions |
|---|---|---|---|
| pending | deps_integrated | ready | — |
| ready | request_sent | requested | — |
| requested | accepted | accepted | — |
| requested, accepted | agent_blocked | blocked | escalate |
| accepted | delivered | delivered | — |
| delivered | check_started | checking | — |
| checking | check_passed | in_review | open_review |
| checking | check_failed | requested if RejectionsLeft > 1, else blocked | supersede_attempt + send_request, or escalate |
| in_review | approved | approved | resolve_review |
| in_review | review_rejected | requested if RejectionsLeft > 1, else blocked | resolve_review + supersede_attempt + send_request, or resolve_review + escalate |
| approved | integrate_started | integrating | — |
| integrating | integrated | integrated | — |
| integrating | integrate_failed | blocked | escalate |
| blocked | retry | requested | supersede_attempt + send_request |
| any non-terminal | abort | aborted | supersede_attempt, resolve_review if in_review |

`rejected` is a transient label recorded in the attempt history; `Next` never returns it as the mission state.

- [ ] **Step 1: Failing test**: `TestNextTable` iterates over every listed row, asserting state and actions. It also iterates over every (state × event) pair *not* listed and asserts an error with the state unchanged. `TestRejectionsExhausted` uses `RejectionsLeft: 1` on `check_failed`, which gives `blocked` with `escalate`.
- [ ] **Step 2: Run** `go test ./mission/ -run Next -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `Next` as a table.
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): mission state machine`.

---

### Task 4: Conductor lease and fenced writes (`bus/conductorkeys.go`)

**Files:**
- Create: `bus/conductorkeys.go`
- Modify: `bus/dispatchkeys.go` — generalise the private `fencedAdd` to take the lease key; the dispatcher keeps passing `DispatchKey`
- Test: `bus/conductorkeys_test.go`

**Interfaces — Produces** (every write returns `ErrNotHolder` when the token does not hold `{p}:conductor`):
```go
func ConductorKey(p string) string        // p+":conductor"
func ChangeKey(p, c string) string        // p+":change:"+c
func MissionsKey(p, c string) string      // p+":missions:"+c
func MissionOpsKey(p string) string       // p+":missionops"
func (b *Bus) AcquireConductor(ctx, token string, ttl time.Duration) (bool, error)
func (b *Bus) RenewConductor(ctx, token string, ttl time.Duration) (bool, error)
func (b *Bus) ReleaseConductor(ctx, token string) error
func (b *Bus) ConductorAlive(ctx) (bool, error)
func (b *Bus) FencedHashSet(ctx, token, key, field, value string) error      // ChangeKey / MissionsKey only (refuse other keys)
func (b *Bus) FencedRequest(ctx, token, task, from, target, ref, body string) (string, error) // publishRequest semantics, TTL 0, fenced
func (b *Bus) FencedBoardState(ctx, token, task, state string) error          // sets e.state only; request metadata untouched
func (b *Bus) FencedChallenge(ctx, token, reviewer, ref, from, msg string) error // gate HSET (meta "<from>|<msg>") + cmd type challenge, one script
func (b *Bus) FencedResolveChallenge(ctx, token, reviewer, ref string) error  // HDEL; a missing ref is not an error
func (b *Bus) FencedCmdRef(ctx, token, from, target, typ, ref, command string, noWake bool) (string, error) // noWake adds wake=no
func (b *Bus) ServerNowMS(ctx) (int64, error)                                // Redis TIME in ms
// Operator queue (written by the CLI, not fenced; read and answered by the conductor):
func (b *Bus) SubmitMissionOp(ctx, from, op string, args []string) (string, error) // XADD kind=op
func (b *Bus) MissionOpResult(ctx, token, reqID string, ok bool, msg string) error // fenced XADD kind=result req=<id>
func (b *Bus) ReadMissionOps(ctx, group, consumer string, block time.Duration) ([]MissionOp, error) // XREADGROUP; creates group at "0" if missing
func (b *Bus) AckMissionOp(ctx, group, id string) error
func (b *Bus) WaitMissionOpResult(ctx, reqID string, timeout time.Duration) (ok bool, msg string, found bool, err error) // XREAD after reqID, filters kind=result req=reqID
type MissionOp struct{ ID, From, Op string; Args []string }
```

- [ ] **Step 1: Failing tests** (Redis):
  - `TestConductorLease`: acquire, then a second acquire fails; renew with the wrong token fails; release.
  - `TestConductorFencedWritesRefuseStaleToken`: every `Fenced*` function with a lost token returns `ErrNotHolder` and changes nothing (check the keys and stream lengths).
  - `TestFencedRequestMatchesRequest`: the board entry shape equals `Bus.Request`'s for the same input, and a duplicate task is refused.
  - `TestFencedChallengeOpensGateAndCmd`, then `TestFencedResolveChallengeIdempotent`.
  - `TestFencedCmdRefNoWake`: the entry carries `wake=no` and `ref`.
  - `TestMissionOpsRoundTrip`: submit → read → result → `WaitMissionOpResult` finds it; a timeout returns `found=false`.
  - `TestFencedHashSetRefusesForeignKey`: the key `p:agents` is refused.
- [ ] **Step 2: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./bus/ -run 'Conductor|Fenced|MissionOps' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement**, reusing the dispatcher's Lua patterns. `FencedRequest` is `publishRequest` with a leading `GET` check.
- [ ] **Step 4: Run** the same and `go test ./bus/ -count=1` (the dispatcher tests stay green) — Expected: PASS.
- [ ] **Step 5: Commit** `feat(bus): conductor lease, fenced writes and the missionops queue`.

---

### Task 5: Mission store

**Files:** Create `mission/store.go`. Test: `mission/store_test.go`.

**Interfaces — Consumes:** Task 4. **Produces:**
```go
type ChangeRecord struct {
    Change, Path, Commit, FileHash, Base, Branch string
    Rev   int
    State string // active | aborted
    LoadedAt int64
}
type Attempt struct {
    N int; Task, Agent, Base, Commit, Branch, Outcome, Reason string
} // Outcome: "", superseded, check_failed, rejected, approved
type MissionRecord struct {
    ID, Hash string
    State                     State
    Attempt, Rejections       int
    Current                   Attempt
    History                   []Attempt
    Reviewer, Subject, ReviewRef string
    LogPath, CheckTail        string
    PreHead, TestedHead       string
    TestedOK                  bool
    Reason                    string
    Updated                   int64
}
type Store struct{ B *bus.Bus; Token string }
func (s Store) Change(ctx, c string) (ChangeRecord, bool, error)
func (s Store) PutChange(ctx, r ChangeRecord) error
func (s Store) Missions(ctx, c string) (map[string]MissionRecord, error)
func (s Store) PutMission(ctx, c string, r MissionRecord) error // stamps Updated from ServerNowMS
func (s Store) Changes(ctx) ([]string, error)                   // SCAN p:change:* (ValidName on the suffix)
```

- [ ] **Step 1: Failing tests** (Redis): `TestStoreRoundTrip` (put then get, field for field); `TestStoreStaleToken` (put with a lost lease gives `ErrNotHolder`); `TestStoreChangesScan` (two changes are listed, and a foreign key `p:change:Bad Name` is skipped).
- [ ] **Step 2: Run** `go test ./mission/ -run Store -count=1` (with the test broker) — Expected: FAIL.
- [ ] **Step 3: Implement** — JSON values in the hashes.
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): typed store over fenced writes`.

---

### Task 6: `Git` — every command, bounded

**Files:** Create `mission/git.go`. Test: `mission/git_test.go`.

**Interfaces — Produces:**
```go
type Git struct{ Repo string } // the shared repository (main worktree or any worktree of it)
func (g Git) Show(ctx, commit, path string) ([]byte, error)
func (g Git) Tree(ctx, commit string) ([]string, error)                   // ls-tree -r --name-only
func (g Git) ResolveCommit(ctx, rev string) (string, error)               // full sha; error if unknown
func (g Git) IsAncestor(ctx, a, b string) (bool, error)
func (g Git) DiffNames(ctx, base, commit string) ([]string, error)
func (g Git) EnsureBranch(ctx, branch, from string) error                 // create if missing; never moves an existing branch
func (g Git) AddWorktree(ctx, dir, rev string, detach bool) error
func (g Git) RemoveWorktree(ctx, dir string) error                         // --force on our own dirs only
func (g Git) Head(ctx, dir string) (string, error)
func (g Git) MergeNoFF(ctx, dir, commit, message string) error            // conflict → ErrConflict (merge left in progress)
func (g Git) MergeInProgress(dir string) (bool, error)                    // MERGE_HEAD present
func (g Git) MergeAbort(ctx, dir string) error
func (g Git) ResetHard(ctx, dir, rev string) error
func (g Git) Dirty(ctx, dir string) (bool, error)                          // status --porcelain non-empty (untracked included)
func (g Git) BranchHasTrailer(ctx, branch, key, value string) (bool, error) // used by restart checks
func RunTest(ctx context.Context, dir, command, logPath string, limit time.Duration) (ok bool, tail string, err error)
var ErrConflict = errors.New("merge conflict")
```
Rules:
- Every command runs via `exec.CommandContext` with `SysProcAttr{Setpgid: true}` and `Cancel` set to kill the **process group** (`syscall.Kill(-pid, SIGKILL)`), and `WaitDelay` 5 s.
- `RunTest` runs `sh -c command` with stdout and stderr appended to `logPath`, and enforces `limit`. On timeout it returns `ok=false` with `timeout after 20m0s` as the tail's last line. `tail` is the last 40 lines, at most 4096 bytes.
- The merge message carries the trailers in its body, because `git merge` has no `--trailer` (§5.2):
  `mission <c>/<id> (rev <n>): <title>\n\nMission: <c>/<id>\nMission-Commit: <sha>\nMission-Rev: <n>\nMission-Hash: <hash>\n`

- [ ] **Step 1: Failing tests** on a `t.TempDir()` repository built with `git init -b main` and commits made by the test:
  - `TestDiffNamesAndAncestor`;
  - `TestEnsureBranchNeverMoves`;
  - `TestMergeNoFFTrailers`: `git log -1 --format=%(trailers:key=Mission-Hash,valueonly)` returns the hash;
  - `TestMergeConflictLeavesMergeInProgress`, then `MergeAbort` → clean;
  - `TestRunTestPassFail`: `true`/`false`;
  - `TestRunTestTimeoutKillsGroup`: the command is `sleep 30 & sleep 30` with a 1 s limit, which returns within 7 s. Afterwards `pgrep -f "sleep 30"` finds no survivor started by this test (match on a unique marker in the command);
  - `TestRunTestTailBounded`: the command is `yes | head -c 2000000`, the tail is at most 4096 bytes, and the log file holds the full output;
  - `TestResolveCommitUnknown`: an error for `deadbeef`.
- [ ] **Step 2: Run** `go test ./mission/ -run 'Diff|Ensure|Merge|RunTest|Resolve' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): bounded git and test runner`.

---

### Task 7: The reconciliation pass — request, check, review

**Files:** Create `mission/conductor.go`. Test: `mission/conductor_test.go`.

**Interfaces — Consumes:** Tasks 1–6. **Produces:**
```go
type Conductor struct {
    Bus        *bus.Bus
    Store      Store
    Git        Git
    StateRoot  string    // ~/.local/state/agentbus/<p>
    EscalateTo string    // default "master"
    Self       string    // "conductor"
    TestLimit  time.Duration // 20m
}
func (c *Conductor) Pass(ctx context.Context) error // one reconciliation over every active change
```
`Pass` reads each active change's `missions.json` with `Git.Show(change.Commit, change.Path)`, verifies `FileHash`, and then, for each mission in id order:

1. **pending → ready** when every dependency is `integrated`. On `ready`, it sends the request:
   - `Attempt++`, `Current = {N, Task: TaskName(..), Agent, Base: Git.ResolveCommit(change.Branch)} (the branch head; the integration worktree may not exist yet)`;
   - `FencedRequest(task, "conductor", agent, "", body)`. The body is `mission <c>/<id> rev <n>: <title> | base=<sha> | brief=<change dir>/<brief path>@<commit>#<anchor> | deliver: agentbus request done <task> --commit <sha> --branch <b> "<summary>"`, and on a retry it adds `| previous: <reason>`;
   - then `Next(ready, request_sent)`.
2. **requested/accepted:** read `Bus.Board()[Current.Task]`. A board state of `accepted` → `accepted`. `blocked` → `blocked`, with the reason and an escalation. `done` → `delivered`, with `Current.Commit/Branch` taken from `request.commit/branch`.
3. **delivered → checking → in_review or a new attempt.** The checks run in order:
   - no commit, or `ResolveCommit` fails → `check_failed` with "commit <x> not found in <repo>";
   - `!IsAncestor(base, commit)` → `check_failed` with "does not descend from base";
   - any `DiffNames` path outside `allowed_paths` → `check_failed` listing them;
   - otherwise, in a worktree `StateRoot/<c>/check-<task>` (detached at the commit), `RunTest`. Red → `check_failed` with the tail; green → `check_passed`. The worktree is always removed afterwards.

   On `check_passed`, it sets `Subject = "mission:<c>/<id>@" + commit[:12]` and `ReviewRef = Current.Task`, then calls `FencedChallenge(reviewer, ReviewRef, "conductor", msg)`. `msg` holds the subject, the log path and the exact `agentbus verdict --subject <Subject> --ref <ReviewRef> <agent> <approve|reject> <message>` line.
4. **in_review:** `RollUp(Verdicts(Subject))`, keeping only verdicts with `Author == Current.Agent` (the agent). `APPROVED` → `approved` + `FencedResolveChallenge`; `REJECTED` → `review_rejected`; `PENDING` → nothing.
5. **Applying actions:**
   - `supersede_attempt`: mark the old attempt `superseded` in `History`, set `FencedBoardState(oldTask, "superseded")`, and send `FencedCmdRef(.., oldAgent, "reply", oldThread, "superseded by <new task>: stop, do not deliver", noWake=true)`;
   - `escalate`: `FencedCmdRef(.., EscalateTo, "escalation", "", "<c>/<id> blocked: <reason>. Next: agentbus mission retry <c>/<id> | agentbus mission abort <c>")`.

   Rejections increment `Rejections`. `Ctx.RejectionsLeft = MaxAttempts - Rejections` (counted after the increment).
6. **A late `accept` or `done` on a superseded task** found on the board moves nothing. The conductor resends the superseded reply once per such transition (remember it in `History[i].Outcome = "superseded_notified_<state>"`).

Integration is Task 8. Here `approved` stays `approved`.

- [ ] **Step 1: Failing tests** (Redis plus a temp repository; agents are simulated by calling `bus` request transitions as those agents; `Pass` is called directly, one step at a time). Set up a two-mission change: `t1` (agent `kimi`, reviewer `codex`), and `t2` depending on `t1`.
  - `TestPassRequestsReadyMission`: after one `Pass`, the board has `chg-t1-1` targeting `kimi` and `t2` is still `pending`.
  - `TestPassChecksAndOpensReview`: kimi accepts and commits an allowed file, then `CompleteRequestAt` with the sha. After `Pass`: `in_review`, a gate on `codex` with ref `chg-t1-1`, and the subject set.
  - `TestPassRejectsOutOfScope`: the commit touches `README.md`, giving attempt 2, the old task `superseded`, and a `wake=no` reply on the old thread.
  - `TestPassRejectsRedTests`: the test is `false`.
  - `TestPassUnknownCommit`: `--commit deadbeef` gives `check_failed` with "not found" and attempt 2, with no panic.
  - `TestPassAttemptsExhausted`: three red deliveries give `blocked` and one escalation cmd to `master`.
  - `TestPassIgnoresWrongSubjectAndSelfApproval`: a verdict on `@<other sha>` and a self-approve by kimi leave it `in_review`, while codex's approve on the right subject gives `approved` with the gate closed.
  - `TestPassAgentBlocked`: `BlockRequest` gives `blocked` with the reason, plus an escalation.
  - `TestPassSupersededLateDone`: after a supersede, a `CompleteRequestAt` on the old task changes nothing for the mission, and exactly one more reply is sent.
- [ ] **Step 2: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -run Pass -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement** `Pass` and the action helpers.
- [ ] **Step 4: Run** — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): conductor pass — requests, checks, review`.

---

### Task 8: Integration, startup cleanup, and the operator commands

**Files:** Create `mission/integrate.go`, `mission/ops.go`. Modify `mission/conductor.go` (call them from `Pass`). Test: `mission/integrate_test.go`, `mission/ops_test.go`.

**Interfaces — Produces:**
```go
func (c *Conductor) Startup(ctx context.Context) error           // per active change: integration worktree cleanup (below)
func (c *Conductor) integrate(ctx context.Context, ch ChangeRecord, f File, m Mission, r *MissionRecord) error
func (c *Conductor) HandleOp(ctx context.Context, op bus.MissionOp) (ok bool, msg string)
```
**Integration**, one mission per `Pass`, approved missions in id order, in `StateRoot/<c>/integration`. That directory is created with `AddWorktree(dir, branch, detach=false)` when it is missing.
1. Record `PreHead = Head(dir)`, then call `Next(approved, integrate_started)` and persist.
2. `MergeNoFF(commit, message)`. `ErrConflict` → `MergeAbort`, then `integrate_failed`.
3. If the change has `test`, run `RunTest(dir, test, log, TestLimit)`, then record `TestedHead = Head(dir)` and `TestedOK`. Red → `ResetHard(PreHead)` and `integrate_failed`.
4. Green → `integrated`. Dependents unlock in the same or the next `Pass`.

**Startup**, for each active change:
- if `MergeInProgress(integration)` → `MergeAbort`;
- if `Dirty` → set every mission of the change that is in `integrating` to `blocked` with the reason "integration worktree has unrecorded changes", escalate, and stop handling that change, without deleting anything;
- for each mission in `integrating`, follow §6.3:
  - commit not an ancestor of the branch head → redo the merge from `PreHead`, after a `ResetHard(PreHead)` only if `Head != PreHead` and `PreHead` is an ancestor of `Head`;
  - commit is an ancestor, and `TestedOK && TestedHead == Head` → `integrated`;
  - otherwise rerun the change test on the current head;
- missions in `checking` go back to `delivered`, so `Pass` re-checks them.

**Ops**, each answered with `MissionOpResult`:
- `load <path>@<commit>`:
  1. `Show`, `Parse`, then `Validate` with `Tree(commit)` and `briefExists` checked through `Show`;
  2. a known change with the same `FileHash` → ok "already loaded rev N";
  3. a known change with a different hash → error "reload is not available in slice 1";
  4. `EnsureBranch(branch, base)`;
  5. create the records (rev 1, every mission `pending`), then ok "loaded <c> rev 1: N missions".
- `retry <c>/<id>`: only when the mission is `blocked`. It resets `Rejections` to 0, then `Next(blocked, retry)` and the actions.
- `abort <c> --reason <text>`: `Change.State = aborted`. Every non-terminal mission → `abort`, with its actions. Gates are resolved and in-flight attempts are superseded. Branch and worktrees are left in place.

- [ ] **Step 1: Failing tests** (Redis plus a temp repository):
  - `TestIntegrateUnlocksDependent`: t1 approved → `integrated`, the change branch has the trailers, and the next `Pass` requests t2 with `base` = the new head.
  - `TestIntegrateConflictBlocks`: the change branch moved with a conflicting edit → `blocked`, an escalation, the worktree clean and `Head == PreHead`.
  - `TestIntegrateRedSuiteResets`: the change test is `false` → `blocked`, with `Head == PreHead`.
  - `TestStartupMergeInProgress`: a worktree left mid-merge is aborted and the pass continues.
  - `TestStartupDirtyBlocks`: an untracked file in the integration worktree → `blocked` + escalation, and the file still exists.
  - `TestStartupResumeAfterMergeBeforeSuite`: a record in `integrating` with the commit already merged and `TestedOK=false` → the suite reruns, then `integrated`.
  - `TestOpLoadValidates`: an invalid file gives an error result and no records; a valid one creates rev 1; loading the same file twice gives an ok "already loaded"; a different file for the same change gives the slice-1 refusal.
  - `TestOpRetryOnlyBlocked`: retry on `in_review` is an error, and on `blocked` it opens a new attempt.
  - `TestOpAbort`: open gates are resolved, attempts superseded and the change `aborted`; `Pass` does nothing more for it.
- [ ] **Step 2: Run** `go test ./mission/ -run 'Integrate|Startup|Op' -count=1` (with the test broker) — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./mission/ -count=1` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mission): integration, restart cleanup and operator ops`.

---

### Task 9: `Run` — lock, lease, inbox, tick; the CLI

**Files:** Create `mission/run.go`, `cmd/agentbus/missions.go`. Modify `cmd/agentbus/main.go` (dispatch `missions` and `mission` after `bus.Open`; usage list). Test: `mission/run_test.go`, `cmd/agentbus/missions_test.go`.

**Interfaces — Produces:**
```go
type RunConfig struct{ Repo, StateRoot, EscalateTo string; Tick, Lease, Renew, TestLimit time.Duration }
func DefaultRunConfig() RunConfig  // Tick 2s, Lease 15s, Renew 5s, TestLimit 20m, EscalateTo "master", StateRoot from XDG
var ErrAlreadyRunning = errors.New("another conductor holds the lease")
func Run(ctx context.Context, b *bus.Bus, cfg RunConfig) error
```
`Run` does the following, in order:
1. take an exclusive non-blocking `flock` on `StateRoot/conductor.lock`. If it is busy, return `ErrAlreadyRunning`;
2. `AcquireConductor`. If that fails, also return `ErrAlreadyRunning`;
3. start the renewal goroutine. A renewal failure cancels the run context, which kills the commands in flight through `Git`/`RunTest`, and `Run` then returns `bus.ErrNotHolder` wrapped as "lease lost";
4. start the inbox goroutine, `b.WatchCmdDelivery(ctx, "conductor", consumer, "", deliver)`. It holds the receiver lease, and each entry kicks a pass (a non-blocking send on a channel) and returns `false` so the watch keeps going;
5. call `Startup`;
6. loop: on every `Tick` or kick, read the pending ops (`ReadMissionOps` with group `conductor`, block 0), `HandleOp` each one and ack it, then `Pass`.

On exit it releases the lease and the lock.

CLI (in `cmd/agentbus/missions.go`):
- `missions [--repo DIR] [--escalate-to A]` runs `mission.Run`. `ErrAlreadyRunning` → exit 0 with "conductor already running". Lease lost → exit 1. `--status` prints `missions up` (exit 0) or `missions down` (exit 1).
- `mission load <path>@<commit> [--wait 30s]`, `mission retry <c>/<id>` and `mission abort <c> --reason <text>`:
  - with `ConductorAlive` false → `missions down: start agentbus missions`, exit 1;
  - otherwise `SubmitMissionOp`, then `WaitMissionOpResult`. ok → print the message, exit 0. Error → print it, exit 1. Timeout → `queued as <id>, not applied yet`, exit 1.
- `mission status [<c>]` (a table: id, state, attempt, agent, reviewer, reason) and `mission show <c>/<id>` read Redis directly.
- Usage errors exit 2.

- [ ] **Step 1: Failing tests**:
  - `TestRunSecondConductorExits` (Redis plus a temp state dir): two `Run` calls, the second returns `ErrAlreadyRunning`.
  - `TestRunFlockBlocksEvenWithoutLease`: with the lease key deleted externally but the lock held, a second `Run` still returns `ErrAlreadyRunning`.
  - `TestRunLeaseLostKillsTest`: a change whose mission test is `sleep 60`; delete `{p}:conductor` during the check → `Run` returns within 20 s, and no `sleep 60` with the test's marker survives.
  - `TestRunInboxKeepsConductorArmed`: while `Run` runs, `b.Armed(ctx, "conductor")` is true.
  - `TestMissionCLIDown`: with no conductor, `mission retry x/y` exits 1 with `missions down`.
  - `TestMissionCLIRoundTrip`: with `Run` in a goroutine, `mission load` exits 0 and `mission status` lists the missions.
  - `TestMissionCLIUsage`: bad arguments exit 2.
- [ ] **Step 2: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ ./cmd/agentbus/ -run 'Run|MissionCLI' -count=1` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the same, then `go build ./... && go vet ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(agentbus): missions conductor and mission CLI`.

---

### Task 10: End-to-end scenario and docs

**Files:** Create `mission/e2e_test.go`. Modify `CLAUDE.md` (architecture paragraph for `mission/` and `bus/conductorkeys.go`; a Things-that-bite line: *the conductor is the only process that runs repository code and the only writer of mission state; never add an unfenced mission write*), `README.md` (a "Missions (v0.10, slice 1)" section: what it is, when to use agent teams instead, the four commands, the limits from spec §6.7).

- [ ] **Step 1: Failing test** `TestEndToEndThreeMissions`. It uses Redis and a temp repository, and runs `Run` in a goroutine. There are three missions: `a` and `b` independent (disjoint paths), and `c` depending on both. Simulated agents (goroutines) poll the board, accept their task, commit in their own worktree from the given base, and deliver. A simulated reviewer approves each subject. The test asserts:
  - `a` and `b` are requested in the same pass;
  - `c` is requested only after both are `integrated`, with `base` = the head that contains both merges;
  - all three end `integrated`;
  - the change branch has exactly three merge commits with `Mission-Hash` trailers;
  - `main` is untouched;
  - no gate stays open.

  `TestEndToEndRestartMidCheck` kills `Run`'s context during `b`'s check (the test is `sleep 2; true`), restarts it, and asserts that `b` ends `integrated` exactly once.
- [ ] **Step 2: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./mission/ -run EndToEnd -count=1 -v` — Expected: FAIL until all the pieces are wired, then PASS (this task only adds the test and the docs; a failure here is a bug in Tasks 1–9 to fix where it lives).
- [ ] **Step 3: Docs** as listed; `go run ./cmd/agentbus mem check` → PASS.
- [ ] **Step 4: Run** `AGENTBUS_TEST_REDIS_URL=redis://127.0.0.1:6392 go test ./... -race -count=1` — Expected: every package `ok`.
- [ ] **Step 5: Commit** `test(mission): end-to-end conductor run; docs`.
