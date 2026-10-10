# Event-Driven Dispatch (v0.8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `agentbus dispatch` wakes unarmed agents through herdr when Redis holds work for them, so idle agents cost zero tokens and no LLM routes ordinary messages.

**Architecture:** Protocol primitives (obligation, wake record, dispatcher lease, drain, identity fields) go in `bus/`. A new `dispatch/` package holds the pure `Decide` policy, the herdr `Runtime`, and the reconciliation loop. `cmd/agentbus` gains thin `dispatch`, `subscribe --drain` and `cmd --no-wake` entry points; busmon gains indicators.

**Tech Stack:** Go 1.26, go-redis v9 (already a dependency), herdr CLI (`herdr` on PATH), tview (busmon).

**Spec:** `docs/superpowers/specs/2026-10-10-event-driven-dispatch-design.md` (rev 2 + 5caa04c). Every task argues from it; read §3 and §5 before any task.

## Global Constraints

- No new Go module dependencies. `bus.ProtocolVersion` stays `1`; all wire changes are additive.
- Protocol logic lives in `bus/`; binaries stay thin (CLAUDE.md rule).
- Tests never touch the production broker: Redis tests call `dialTest(t)` (→ `testredis.Configure`) and run only with `AGENTBUS_TEST_REDIS_URL` pointing to an isolated instance.
- All user-visible text in English.
- Durable time comes from Redis (`TIME`, stream-id timestamps), never the dispatcher's local clock, for anything persisted or compared across restarts.
- Pinned values: horizon `24h`; retry window `60s`; max tries `3`; escalate-after T `10m` (`working` never escalates before `4×T`); tick `5s`; dispatcher lease `15s`, renew every `5s`; minimum lease PTTL before a prompt `5s`; herdr prompt timeout `3s`; confirm wait `5s`; screen settle `200ms` (two identical reads); error backoff cap `30s`.
- Wake text, exact: `[agentbus] <project>: <n> message(s) waiting for <agent>. Run: agentbus --project <project> subscribe --drain <agent>`
- Escalation recipient default: role name `master` (`--escalate-to`).
- A client without a probed signature entry is never prompted.

## Review Focus

1. **herdr missing or its socket down** — dispatcher keeps running, defers every agent, escalates each obligation once at T; it never exits or spins. Test in Task 8.
2. **A `cmd` to a name that never registers (typo)** — exactly one escalation, not one per tick, and no prompt anywhere. Test in Task 8.
3. **Human parked on an agent's pane (focused forever)** — no prompt ever; one escalation at T. Test in Task 6.
4. **Project deleted (`busmon --delete`) under a running dispatcher** — the lease vanishes; the dispatcher stops prompting and exits 0 with a message rather than recreating keys. Test in Task 8.
5. **1000-entry backlog for one agent** — one prompt, `<n>` in the wake text equals the obligation size, drain delivers them in id order. Test in Task 8.

---

## File Structure

| File | Responsibility |
|---|---|
| `bus/stream.go` (modify) | `Event` gains `NoWake`, `Stale`, `AgeS`; `ParseEntry` reads `wake`; new cmd types; `CmdNoWake`; `AgentSnapshot`/`AgentIdent` gain identity; `Status` ensures group |
| `bus/obligation.go` (create) | `EnsureGroup`, `Obligation` |
| `bus/delivery.go` (modify) | factor the loop; `DrainCmd` |
| `bus/requests.go` (modify) | block notification in the Lua transition |
| `bus/dispatchkeys.go` (create) | dispatcher lease, `WakeRecord` CAS |
| `bus/projects.go` (modify) | `"dispatch"` in `projectKinds` |
| `dispatch/decide.go` (create) | pure policy |
| `dispatch/ident.go` (create) | `/proc` ancestor + start time |
| `dispatch/herdr.go` (create) | herdr `Runtime`, signature table |
| `dispatch/loop.go` (create) | reconciliation loop, wake procedure, escalation, logs |
| `cmd/agentbus/main.go`, `cmd/agentbus/subscribe.go` (modify) | `dispatch`, `--drain`, `--no-wake`, status identity, shutdown no-wake |
| `cmd/busmon/render.go`, `cmd/busmon/main.go` (modify) | indicators |
| skills, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md`, `scripts/bootstrap`, `docs/releases/v0.8.0.md` | rules and docs |

---

### Task 1: Stream fields — `wake=no`, new cmd types, `stale`

**Files:**
- Modify: `bus/stream.go` (`Event` ~140, `ParseEntry` ~164, `validCmdTypes` ~32, `Cmd` ~278)
- Modify: `cmd/agentbus/main.go` (`cmd` case ~180, `shutdown` case ~503)
- Test: `bus/stream_test.go`, `cmd/agentbus/shutdown_test.go`

**Interfaces:**
- Produces: `const CmdEscalation = "escalation"`, `const CmdBlocked = "blocked"` (both in `validCmdTypes`); `Event.NoWake bool` (field `wake` == `"no"`), `Event.Stale bool`, `Event.AgeS int64` (set by drain only); `func (b *Bus) CmdNoWake(ctx context.Context, from, target, typ, ref, command string) (string, error)` — same validation as `Cmd`, adds field `wake=no`. Implement both through one unexported helper taking an extra-fields map.

- [ ] **Step 1: Write failing tests** — `TestParseEntryReadsWakeNo` (fields `{"wake":"no",...}` → `NoWake==true`; absent → false); `TestCmdNoWakeWritesField` (Redis: XRANGE the entry, `wake=="no"`, `type`/`target` intact); `TestCmdAcceptsEscalationAndBlockedTypes`; `TestShutdownUsesNoWake` (shutdown handler's published entries all carry `wake=no`).
- [ ] **Step 2: Run** `go test ./bus ./cmd/agentbus -run 'WakeNo|NoWake|EscalationAndBlocked|ShutdownUsesNoWake' -count=1` — expect FAIL (undefined).
- [ ] **Step 3: Implement** the fields, types, `CmdNoWake`; `agentbus cmd --no-wake` via `extractBool`; shutdown calls `CmdNoWake`.
- [ ] **Step 4: Run** the same command with `AGENTBUS_TEST_REDIS_URL` set — expect PASS; `go vet ./...` clean.
- [ ] **Step 5: Commit** `feat(bus): wake=no field, escalation/blocked cmd types; shutdown never wakes`

---

### Task 2: Group creation at the horizon floor + obligation

**Files:**
- Create: `bus/obligation.go`; Test: `bus/obligation_test.go`
- Modify: `bus/stream.go` (`Status` ~224)

**Interfaces:**
- Produces:
  ```go
  func (b *Bus) EnsureGroup(ctx context.Context, agent string, horizon time.Duration) error
  // XGROUP CREATE {p}:cmd <agent> <serverNowMs-horizonMs>-0 MKSTREAM; BUSYGROUP is success.
  type Obligation struct {
      GroupExists   bool
      Pending       int64  // XPENDING count
      Unread        int    // entries > last-delivered-id with target==agent, wake!=no, not stale
      Stale         int    // same set, older than horizon
      Trimmed       int64  // group lag not reachable: last-delivered-id < first retained id
      OldestID      string // oldest wake-relevant id (PEL or unread), "" if none
      HighID        string // newest wake-relevant id
      LastDelivered string
  }
  func (b *Bus) Obligation(ctx context.Context, agent string, horizon time.Duration) (Obligation, error)
  func (o Obligation) Wakes() bool // Pending>0 || Unread>0
  func (o Obligation) Size() int64 // Pending + Unread
  ```
  No group → scan the retained stream for `target==agent` (same filters) and report `GroupExists=false`.
- `Status` calls `EnsureGroup(ctx, agent, 24*time.Hour)` best-effort (error ignored, like the snapshot HSET).

- [ ] **Step 1: Failing tests** — `TestEnsureGroupCapturesEarlierCommand` (cmd to `dev`, then `EnsureGroup`, then `Obligation.Unread==1`); `TestEnsureGroupIdempotent`; `TestObligationIgnoresOtherTargetsAndNoWake`; `TestObligationCountsPending` (read one via `WatchCmdDelivery` callback returning error → stays pending → `Pending==1`); `TestObligationNoGroupScansStream` (`GroupExists==false`, `Unread==1`); `TestObligationStaleNotWaking` (insert entry with explicit id older than horizon via `XADD` with id `<now-25h>-0` → `Stale==1`, `Wakes()==false`); `TestStatusCreatesGroup`.
- [ ] **Step 2: Run** `go test ./bus -run 'EnsureGroup|Obligation|StatusCreatesGroup' -count=1` — FAIL.
- [ ] **Step 3: Implement.** Server time via `b.r.Time`. `Trimmed`: from `XINFO GROUPS` lag when last-delivered-id < `XINFO STREAM` first-entry id, else 0. Pending-only obligations set `OldestID` from `XPENDING` summary lower bound.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(bus): create cmd groups at the horizon floor; compute per-agent obligation`

---

### Task 3: `subscribe --drain`

**Files:**
- Modify: `bus/delivery.go`; Test: `bus/drain_test.go`
- Modify: `cmd/agentbus/subscribe.go`, `cmd/agentbus/main.go` (`subscribe` case ~577); Test: `cmd/agentbus/subscribe_test.go`

**Interfaces:**
- Produces: `func (b *Bus) DrainCmd(ctx context.Context, agent, consumer string, horizon time.Duration, deliver func(Event) error) (int, error)` — same lease, PEL-first, ACK-after-output, board-record behaviour as `WatchCmdDelivery`, **no floor**, read `>` without BLOCK; returns delivered count when PEL and unread set are empty. Sets `Event.Stale`/`AgeS` from server time vs id. Refactor: extract the body of `WatchCmdDelivery`'s loop into an unexported function both call; `WatchCmdDelivery` behaviour unchanged (existing tests are the guard).
- CLI: `subscribe --drain <agent>` → one JSON line per entry (`subEvent` gains `Stale bool json:"stale,omitempty"`, `AgeS int64 json:"age_s,omitempty"`, no `rearm`), exit `0` if ≥1, `64` if none, `75` on error after output (same rule as one-shot). `--drain` with `--since` or `--loop` → usage error.

- [ ] **Step 1: Failing tests** — `TestDrainDeliversPendingThenUnreadInOrder` (one pending + two unread → ids in that order, then returns 3); `TestDrainEmptyReturnsImmediately` (≤200 ms, count 0); `TestDrainAcksNonAddressedSilently`; `TestDrainFlagsStale` (`Stale==true`, `AgeS>=90000`); `TestDrainCallbackErrorLeavesPending`; CLI `TestSubscribeDrainJSONLAndExitCodes` (0 with 2 lines, 64 with 0 lines, every line has `"v":1`).
- [ ] **Step 2: Run** `go test ./bus ./cmd/agentbus -run Drain -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the full `go test ./bus ./cmd/agentbus -count=1` — PASS (existing `WatchCmdDelivery` tests included).
- [ ] **Step 5: Commit** `feat: subscribe --drain empties an agent's queue and exits`

---

### Task 4: `request block` notifies the requester (idempotent)

**Files:** Modify `bus/requests.go` (`requestTransition` Lua); Test `bus/requests_test.go`

**Interfaces:**
- Produces: on op `block`, when the previous state ≠ `blocked` **or** `blocked_reason` differs, XADD `{from: agent, target: r.from, type: "blocked", ref: r.thread, command: reason, task, text_complete: yes}`. Return value unchanged (`""` for block).

- [ ] **Step 1: Failing tests** — `TestBlockNotifiesRequesterOnce` (block twice same reason → exactly one `blocked` entry targeted at requester); `TestBlockNewReasonNotifiesAgain`; `TestBlockAfterAcceptNotifies`.
- [ ] **Step 2: Run** `go test ./bus -run Block -count=1` — FAIL.
- [ ] **Step 3: Implement** in the existing script (atomic with the state change).
- [ ] **Step 4: Run** `go test ./bus -count=1` — PASS.
- [ ] **Step 5: Commit** `feat(bus): a blocked request wakes its requester, once per reason`

---

### Task 5: Dispatcher lease and wake record

**Files:** Create `bus/dispatchkeys.go`, test `bus/dispatchkeys_test.go`; modify `bus/projects.go` (`projectKinds`)

**Interfaces:**
- Produces:
  ```go
  func DispatchKey(project string) string            // {p}:dispatch
  func WakeKey(project, agent string) string          // {p}:wake:{agent}
  func (b *Bus) AcquireDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error)
  func (b *Bus) RenewDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error)  // false = lost
  func (b *Bus) ReleaseDispatch(ctx context.Context, token string) error
  func (b *Bus) DispatchAlive(ctx context.Context) (bool, error)
  type WakeRecord struct {
      State        string // prompting | prompted | unconfirmed | deferred
      At           int64  // server ms of last prompt reservation (0 = never prompted)
      Tries        int
      HighID       string
      PID          int
      Escalated    bool
      ProgressMark string // group last-delivered-id + "/" + pending count at reservation
      Reason       string // last defer/escalation reason, for busmon
  }
  func (b *Bus) Wake(ctx context.Context, agent string) (WakeRecord, bool, error)
  func (b *Bus) ReserveWake(ctx context.Context, agent, token string, next WakeRecord, retry, minPTTL time.Duration) (WakeRecord, bool, error)
  func (b *Bus) PutWake(ctx context.Context, agent, token string, rec WakeRecord) error // fenced
  func (b *Bus) DeleteWake(ctx context.Context, agent string) error
  ```
  `ReserveWake` is one Lua script: dispatch lease == token, `PTTL >= minPTTL`, and (`At==0` or `now-At >= retry`); on success writes `next` with `At=now`, returns the stored record. `PutWake` fails if the lease is not held.

- [ ] **Step 1: Failing tests** — `TestDispatchLeaseExclusive`; `TestRenewDetectsLoss`; `TestReserveWakeWindow` (second reservation within 60 s → false, after window → true, using a 50 ms retry in test); `TestReserveWakeRequiresLeaseAndPTTL` (wrong token → false; PTTL below min → false); `TestPutWakeFenced`; `TestProjectsSeesDispatchKey`, `TestWakeKeyDoesNotForgeProject`.
- [ ] **Step 2: Run** `go test ./bus -run 'Dispatch|Wake' -count=1` — FAIL.
- [ ] **Step 3: Implement** (hash fields, JSON-free; server time inside Lua).
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(bus): dispatcher lease and fenced wake reservations`

---

### Task 6: Pure decision policy

**Files:** Create `dispatch/decide.go`, test `dispatch/decide_test.go`; modify `bus/stream.go` (`AgentSnapshot`, `AgentIdent` — fields only)

**Interfaces:**
- Consumes: `bus.Obligation`, `bus.WakeRecord`, `bus.AgentSnapshot`.
- Produces: `bus.AgentSnapshot` and `bus.AgentIdent` gain `PID int json:"pid,omitempty"`, `PIDStart uint64 json:"pid_start,omitempty"`, `Terminal string json:"terminal,omitempty"`; `Status` copies them from `AgentIdent` into the snapshot (Task 7 fills them).
  ```go
  type Inspection struct {
      State   string // idle|working|blocked|done|unknown
      Client  string
      Focused bool
      PIDs    []int
      Starts  map[int]uint64 // pid → /proc start time
      Ready   bool
      Busy    bool
      Gone    bool
      Err     error
  }
  type Action int // ActNone, ActPrompt, ActDefer, ActEscalate
  type Decision struct { Action Action; Rule int; Reason string }
  type Policy struct { Retry, EscalateAfter time.Duration; MaxTries int; Supported map[string]bool }
  func Decide(p Policy, o bus.Obligation, armed bool, snap *bus.AgentSnapshot, in Inspection, w *bus.WakeRecord, progressMark string, oldestAge time.Duration, now time.Time) Decision
  ```
  Rules exactly as spec §3.2 rows 1–9, `Rule` = row number. Ready = `(State idle|done) && !Busy && Ready && !Focused`. Identity = `snap.PID ∈ in.PIDs && in.Starts[snap.PID]==snap.PIDStart`. Progress = `progressMark != w.ProgressMark`. Escalate only if `!w.Escalated`; `working` escalates only when `oldestAge > 4*EscalateAfter`. Unsupported client → rule 7.

- [ ] **Step 1: Failing tests** — one table `TestDecide` with a named case per rule and these extra cases: `burst-within-window-no-prompt`; `tries-cap-escalates`; `progress-resets-tries` (expect prompt); `identity-pid-mismatch-defers`; `restart-same-pid-different-start-defers`; `kimi-done-but-busy-defers`; `focused-forever-escalates-once` (escalated=true → ActNone); `working-escalates-only-after-4T`; `unsupported-client-never-prompts`; `stale-only-none`.
- [ ] **Step 2: Run** `go test ./dispatch -run TestDecide -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(dispatch): pure wake decision policy`

---

### Task 7: Process identity + herdr runtime

**Files:**
- Create `dispatch/ident.go` (+ `dispatch/ident_test.go`), `dispatch/herdr.go` (+ `dispatch/herdr_test.go`, fixtures `dispatch/testdata/screens/<client>-<case>.txt`)
- Modify `cmd/agentbus/main.go` (`status` case ~116)

**Interfaces:**
- Produces:
  ```go
  // ident.go
  func ProcStart(pid int) (uint64, error)                  // field 22 of /proc/<pid>/stat
  func AncestorAmong(candidates []int, self int) (int, bool) // walks ppid from self
  // herdr.go
  type Runtime interface {
      Inspect(ctx context.Context, pane string) Inspection
      Prompt(ctx context.Context, pane, text string) (confirmed bool, err error)
  }
  type Herdr struct{ Bin string } // default "herdr"
  func (h Herdr) Inspect(ctx context.Context, pane string) Inspection
  func (h Herdr) Prompt(ctx context.Context, pane, text string) (bool, error)
  func (h Herdr) ForegroundPIDs(ctx context.Context, pane string) ([]int, string /*terminal_id*/, error)
  type Signature struct{ Ready, Busy []*regexp.Regexp; ANSI bool }
  var Signatures map[string]Signature // keys: codex, kimi, grok, agy, claude (claude only after Step 1)
  ```
- Consumes: identity fields on `bus.AgentSnapshot`/`bus.AgentIdent` (Task 6).
- `agentbus status`: when `HERDR_ENV=1` and `HERDR_PANE_ID` set, `ForegroundPIDs` (1 s timeout) → `AncestorAmong(pids, os.Getpid())` → `ProcStart`; any failure leaves the fields empty (status still succeeds).
- Inspect: `herdr agent get <pane>` (state, client, focused); `ForegroundPIDs`; `herdr pane read <pane> --source recent --lines 40` (with `--ansi` when `Signature.ANSI`) twice 200 ms apart — not identical → `Ready=false`. Gone = herdr reports unknown pane.
- Prompt: `herdr agent prompt <pane> <text>` (3 s timeout; `agent_prompt_stalled` is **not** an error), then `herdr pane wait-output <pane> --match "[agentbus]" --timeout 5000` → `confirmed`.
- Signatures (from spec §10): codex Ready = `›` followed by dim placeholder (`\x1b\[2m`) and footer without `enter/esc latest`; kimi Ready = `^\s*│ >\s*$`, Busy = `^\s*(🌑|🌒|🌓|🌔|🌕|🌖|🌗|🌘)(\s|·|$)`; grok Ready = `^\s*│ ❯\s*$`; agy Ready = a line `^>\s*$` whose previous and next lines are `─` rules.

- [ ] **Step 1: Probe Claude** — in a dedicated herdr pane running `claude`, capture (`herdr pane read --source recent --lines 40 --ansi`) empty input, typed input (`herdr pane send-text`, then `ctrl+u`), and a running turn; save as fixtures; derive the claude Ready/Busy patterns. Record results in spec §10 (Claude column). If no reliable ready signature exists, leave `claude` out of `Signatures` and note it in §10.
- [ ] **Step 2: Capture fixtures** for codex/kimi/grok/agy: empty, typed, busy/modal (codex scroll mode, kimi spinner, agy approval menu).
- [ ] **Step 3: Failing tests** — `TestAncestorAmongFindsParent` (candidates `[os.Getppid()]`, self `os.Getpid()`); `TestProcStartStable`; `TestSignatures` (table over fixtures: Ready/Busy expectations per file, incl. `agy-approval` Ready=false, `codex-scroll` Ready=false, `kimi-spinner` Busy=true); `TestParseAgentGet`/`TestParseProcessInfo` (JSON fixtures copied from real output); `TestStatusRecordsIdentity` (bus level: `AgentIdent{PID:..}` round-trips in `Agents`).
- [ ] **Step 4: Run** `go test ./dispatch ./bus -run 'Ancestor|ProcStart|Signatures|Parse|Identity' -count=1` — FAIL.
- [ ] **Step 5: Implement.**
- [ ] **Step 6: Run** — PASS. Manual check: `agentbus --project scratch status probe idle` from this pane, then `agentbus --project scratch agents --json` shows `pid`, `pid_start`, `terminal`.
- [ ] **Step 7: Commit** `feat(dispatch): herdr runtime, client signatures, process identity`

---

### Task 8: Reconciliation loop and `agentbus dispatch`

**Files:** Create `dispatch/loop.go`, test `dispatch/loop_test.go`; modify `cmd/agentbus/main.go` (new `dispatch` case + usage line)

**Interfaces:**
- Consumes: Tasks 1–7.
- Produces:
  ```go
  type Config struct {
      Horizon, Retry, EscalateAfter, Tick, Lease, Renew, MinPTTL time.Duration
      MaxTries int
      EscalateTo string
      Log io.Writer // JSON lines
  }
  func DefaultConfig() Config // pinned values from Global Constraints
  func Run(ctx context.Context, b *bus.Bus, rt Runtime, cfg Config) error
  ```
  `Run`: acquire lease (not acquired → return `ErrAlreadyRunning`); renew goroutine (lost → cancel, return `ErrLeaseLost`); loop = `XREAD BLOCK Tick` on `{p}:cmd` from `$` (signal only) + reconcile all agents = union of `Agents()` keys, existing cmd group names, and targets of retained entries. Per agent: `Obligation` → armed? (`EXISTS ReceiverKey`) → `rt.Inspect` → `Decide` → act:
  - prompt: `ReserveWake` (state `prompting`, `Tries+1` or `1` after progress, `HighID`, `PID`, `ProgressMark`) → `rt.Prompt(wake text)` → `PutWake` state `prompted`/`unconfirmed`;
  - defer: `PutWake` with `State: deferred`, `Reason`;
  - escalate: `b.Cmd(ctx, "dispatch", cfg.EscalateTo, CmdEscalation, "", body)` (a waking cmd, never `CmdNoWake`) + `b.Notify`; if agent == `EscalateTo`, notify only; then `PutWake Escalated=true`;
  - none with no obligation: `DeleteWake`.
  - `Trimmed>0` first seen → one `Notify`.
  Every decision logs one JSON line `{"agent","action","rule","reason","high_id","latency_ms"}`. Errors: log + backoff doubling from 1 s, cap 30 s; never return on transient errors. Exit codes in CLI: `ErrAlreadyRunning` → print `dispatch already running for <p>` exit 0; `ErrLeaseLost` → message, exit 0.
  - Body of escalation: `"<agent>: <reason>; <n> waiting, oldest <id>"`.
- CLI: `agentbus [--project P] dispatch [--horizon D] [--escalate-after D] [--escalate-to NAME]`; `agentbus dispatch --status` prints `dispatch up|down` and exits 0 / 1 (used by the sentinel), via `DispatchAlive`.

- [ ] **Step 1: Failing integration tests** (fake `Runtime` recording prompts; `Config` with ms-scale durations) — `TestRunWakesUnarmedIdleAgent`; `TestRunSkipsArmedAgent`; `TestRunBurstOnePrompt`; `TestRunCrashAfterReserveNoBurst` (pre-seed a `prompting` record → no prompt within window); `TestRunSecondDispatcherExits`; `TestRunLeaseLapseWakes`; `TestRunRequestDoneWakesRequester`; `TestRunRequestBlockWakesRequester`; `TestRunStaleOnlyNoWake`; `TestRunNoWakeEntryNoPrompt`; Review Focus: `TestRunRuntimeErrorDefersAndEscalatesOnce` (#1), `TestRunUnregisteredTargetEscalatesOnce` (#2), `TestRunProjectDeletedStops` (#4: `DeleteProject` mid-run → `Run` returns `ErrLeaseLost`, no keys recreated), `TestRunLargeBacklogOnePromptCountMatches` (#5: 1000 entries → 1 prompt, text contains `1000 message(s)`); `TestRunEscalationTargetsRoleNotPilot`; `TestRunSelfEscalationNotifiesOnly`; CLI `TestDispatchStatusExitCodes`.
- [ ] **Step 2: Run** `go test ./dispatch -run TestRun -count=1` — FAIL.
- [ ] **Step 3: Implement** `loop.go` and the CLI case (wires `dispatch.Herdr{}`).
- [ ] **Step 4: Run** `go test ./... -count=1 -race` with `AGENTBUS_TEST_REDIS_URL` — PASS.
- [ ] **Step 5: Commit** `feat: agentbus dispatch wakes unarmed agents through herdr`

---

### Task 9: busmon indicators

**Files:** Modify `cmd/busmon/render.go` (`statusBar` ~185, `agentLabel` ~532), `cmd/busmon/main.go` (1 s ticker); Test `cmd/busmon/render_test.go`

**Interfaces:**
- Consumes: `Bus.DispatchAlive`, `Bus.Obligation`, `Bus.Wake`.
- Produces: `statusData.dispatch` (`"" | "up" | "down"`) → `dispatch ●` / `dispatch ○` in the STATUS bar (omit when `""` = unknown/error); `agentState` gains `waiting int64, stale int, trimmed int64, wake *bus.WakeRecord` → chip suffixes `✉N` (waiting>0 and no wake record), `⏰` (wake state deferred/prompting/prompted/unconfirmed), `⚠` (Escalated), `⌛N`, `✂N`.

- [ ] **Step 1: Failing tests** — `TestStatusBarDispatch` (up/down/unknown strings); `TestAgentLabelWakeBadges` (one case per badge, and none when all zero).
- [ ] **Step 2: Run** `go test ./cmd/busmon -run 'Dispatch|WakeBadges' -count=1` — FAIL.
- [ ] **Step 3: Implement** render functions; ticker polls the three calls (per agent, errors → keep last value, as for budgets).
- [ ] **Step 4: Run** `go test ./cmd/busmon -count=1` — PASS.
- [ ] **Step 5: Commit** `feat(busmon): dispatcher liveness and per-agent wake badges`

---

### Task 10: Skills, docs, bootstrap, release notes

**Files:** `skills/agent-bus/SKILL.md`, `skills/agent-bus-master/SKILL.md`, `skills/agent-bus-sentinel/SKILL.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md`, `README.md`, `scripts/bootstrap` (+ `tests/bootstrap_test.sh`), `docs/releases/v0.8.0.md`, `docs/memory/` (one decision note)

**Content (from spec §7, §11):**
- `agent-bus`: wake line → `subscribe --drain <self>` first, never a plain `subscribe`; don't re-arm to wait when `dispatch ●`; never act on `stale` entries without confirmation.
- `agent-bus-master`: conditional sleep on `dispatch ●`; v0.7 armed rule while `dispatch ○`; manual pane wake = fallback; escalation messages arrive as `type: escalation`.
- `agent-bus-sentinel`: cron wake runs `agentbus dispatch --status`; on exit 1, `notify` the human.
- `CLAUDE.md`: replace the two "don't reintroduce …daemon" lines with the §11 rule; document `dispatch/`, new keys, `--drain`, `wake=no`.
- GUIDE: `--drain`, `--no-wake`, `stale`/`age_s`, `escalation`/`blocked` types, plain `subscribe` still floors at now.
- `scripts/bootstrap`: open a `dispatch` pane (`agentbus --project <p> dispatch`); `--no-dispatch` opt-out; shell test asserts the pane command and the opt-out.
- `docs/memory/decisions/2026-10-10-event-driven-dispatch.md`: decision + reason + evidence (spec, probes), per `docs/memory/PROTOCOL.md`; add to `INDEX.md`; `python3 scripts/check-memory.py` passes.

- [ ] **Step 1:** Update `tests/bootstrap_test.sh` with the dispatch-pane and opt-out assertions; run `bash tests/run.sh` — FAIL.
- [ ] **Step 2:** Edit bootstrap, skills, CLAUDE.md, GUIDE, README, memory note.
- [ ] **Step 3:** Run `bash tests/run.sh && python3 scripts/check-memory.py && git diff --check` — PASS.
- [ ] **Step 4: Live acceptance** (record results verbatim in `docs/releases/v0.8.0.md`): project `v08-accept`, panes codex/kimi/grok/agy/claude idle and unarmed after `agentbus status <name> idle`; start `agentbus dispatch`; (a) `agentbus request send` to each → each prompted once, drains, completes; latency from logs; (b) requester (master) asleep → woken by the reply; (c) focus a peer pane and send → no prompt, `⏰` then `⚠` at T (run with `--escalate-after 1m`); (d) type into a peer's input → deferred; (e) 30 min idle → transcripts unchanged (Claude: `~/.claude/projects/*/<session>.jsonl` size; others: pane output unchanged).
- [ ] **Step 5: Commit** `docs: v0.8 dispatch — skills, guide, bootstrap, release notes`
