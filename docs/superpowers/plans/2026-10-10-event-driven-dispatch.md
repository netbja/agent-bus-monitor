# Event-Driven Dispatch (v0.8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `agentbus dispatch` wakes unarmed agents through herdr when Redis holds work for them, so idle agents cost zero tokens and no LLM routes ordinary messages.

**Architecture:** Protocol primitives (obligation, wake record, dispatcher lease, fenced writes, drain, identity fields) go in `bus/`. A new `dispatch/` package holds the pure `Decide` policy, the herdr `Runtime`, and the reconciliation loop. `cmd/agentbus` gains thin `dispatch`, `subscribe --drain` and `cmd --no-wake` entry points; busmon gains indicators; `bootstrap` adds a `dispatch` tab.

**Tech Stack:** Go 1.26, go-redis v9 (existing), herdr CLI, tview (busmon), bash/python (bootstrap).

**Spec:** `docs/superpowers/specs/2026-10-10-event-driven-dispatch-design.md` (rev 3). Read §3 and §5 before any task.

## Global Constraints

- No new Go module dependencies. `bus.ProtocolVersion` stays `1`; all wire changes are additive.
- Protocol logic lives in `bus/`; binaries stay thin (CLAUDE.md rule). `dispatch/` uses only exported `bus` API.
- Tests never touch the production broker. `bus` tests use `dialTest(t)`; `dispatch` and `cmd` tests use a local helper that calls `testredis.Configure(t)`, `bus.Connect("")`, `bus.Open` with a unique project, and cleans up with `bus.DeleteProject`. Manual checks use an isolated Redis too (`REDIS_URL=$AGENTBUS_TEST_REDIS_URL`), never the operational broker.
- All user-visible text in English.
- Anything persisted or compared across restarts uses Redis time (`TIME`, stream-id timestamps), never the dispatcher's local clock.
- **Every dispatcher write is fenced** by its lease token inside Lua (wake records, escalation XADD, notify XADD, deletes), so a dispatcher that lost its lease — or whose project was deleted — writes nothing and recreates no key.
- Pinned values: horizon `24h`; retry window `60s`; max tries `3`; escalate-after T `10m` (`working` never escalates before `4×T`); tick `5s`; lease `15s`, renew `5s`; min PTTL to reserve `5s`; herdr prompt timeout `3s`; observe wait `5s`; settle `200ms`; drain receiver-lease wait `5s`; error backoff `1s` doubling to `30s`.
- Wake text, exact: `[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus --project <project> subscribe --drain <agent>` — `<nonce>` = 6 random lowercase hex chars per prompt.
- Escalation recipient default: role name `master` (`--escalate-to`).
- A client without a probed signature entry is never prompted.

## Review Focus

1. **herdr missing or its socket down** — dispatcher keeps running, defers every agent, escalates each obligation once at T; never exits or spins. Test: Task 8.
2. **A `cmd` to a name that never registers (typo)** — exactly one escalation, no prompt anywhere. Test: Task 8.
3. **Human parked on an agent's pane (focused forever)** — no prompt; one escalation at T. Test: Task 6.
4. **Project deleted (`busmon --delete`) under a running dispatcher** — lease vanishes; no key is recreated; `Run` returns `ErrLeaseLost`, CLI exits 0 with a message. Test: Task 8.
5. **1000-entry backlog for one agent** — one prompt, `<n>`=1000 in the text, and a following drain returns all 1000 in id order. Test: Task 8.

---

## File Structure

| File | Responsibility |
|---|---|
| `bus/stream.go` (modify) | `Event` fields; `ParseEntry`; cmd types; `CmdNoWake`; identity fields; `Status` ensures group |
| `bus/obligation.go` (create) | `EnsureGroup`, `Obligation`, `Armed`, `CmdTargets`, `GroupNames`, `ServerTime` |
| `bus/delivery.go` (modify) | factored delivery step; `DrainCmd` |
| `bus/requests.go` (modify) | block notification |
| `bus/dispatchkeys.go` (create) | dispatcher lease; fenced wake records; fenced escalation/notify |
| `bus/projects.go` (modify) | `"dispatch"` in `projectKinds` |
| `dispatch/decide.go` | pure policy |
| `dispatch/ident.go` | `/proc` ancestry + start time |
| `dispatch/herdr.go` | herdr `Runtime`, signature table |
| `dispatch/loop.go` | reconciliation loop |
| `dispatch/testhelp_test.go` | isolated-Redis helper, fake `Runtime` |
| `cmd/agentbus/*` | `dispatch`, `--drain`, `--no-wake`, status identity, shutdown |
| `cmd/busmon/*` | indicators |
| skills, `CLAUDE.md`, GUIDE, README, `scripts/bootstrap`, release notes, memory note | rules and docs |

---

### Task 1: Stream fields — `wake=no`, new cmd types

**Files:** Modify `bus/stream.go` (`Event` ~140, `ParseEntry` ~164, `validCmdTypes` ~32, `Cmd` ~278), `cmd/agentbus/main.go` (`cmd` ~180, `shutdown` ~503). Tests: `bus/stream_test.go`, `cmd/agentbus/shutdown_test.go`.

**Interfaces — Produces:**
- `const CmdEscalation = "escalation"`, `const CmdBlocked = "blocked"`, both in `validCmdTypes`.
- `Event.NoWake bool` (field `wake=="no"`), `Event.Stale bool`, `Event.AgeS int64` (set by drain only).
- `func (b *Bus) CmdNoWake(ctx context.Context, from, target, typ, ref, command string) (string, error)` — `Cmd` validation + field `wake=no`; both through one unexported helper taking extra fields.

- [ ] **Step 1: Failing tests** — `TestParseEntryReadsWakeNo`; `TestCmdNoWakeWritesField` (XRANGE: `wake=="no"`, other fields intact); `TestCmdAcceptsEscalationAndBlockedTypes`; `TestShutdownUsesNoWake`; `TestCmdNoWakeFlag` (CLI `cmd --no-wake dev hi` → entry has `wake=no`).
- [ ] **Step 2: Run** `go test ./bus ./cmd/agentbus -run 'WakeNo|NoWake|EscalationAndBlocked' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** with `AGENTBUS_TEST_REDIS_URL` — PASS; `go vet ./...`.
- [ ] **Step 5: Commit** `feat(bus): wake=no field, escalation/blocked cmd types; shutdown never wakes`

---

### Task 2: Groups at the horizon floor, obligation, read primitives

**Files:** Create `bus/obligation.go`, `bus/obligation_test.go`; modify `bus/stream.go` (`Status`).

**Interfaces — Produces:**
```go
func (b *Bus) ServerTime(ctx context.Context) (time.Time, error)
func (b *Bus) EnsureGroup(ctx context.Context, agent string, horizon time.Duration) error // XGROUP CREATE {p}:cmd agent <nowMs-horizonMs>-0 MKSTREAM; BUSYGROUP = ok
func (b *Bus) Armed(ctx context.Context, agent string) (bool, error)        // EXISTS ReceiverKey
func (b *Bus) GroupNames(ctx context.Context) ([]string, error)             // XINFO GROUPS {p}:cmd; no stream → nil
func (b *Bus) CmdTargets(ctx context.Context) ([]string, error)             // distinct valid targets in the retained stream
type Obligation struct {
    GroupExists   bool
    Pending       int64  // PEL entries that are wake-relevant (see below)
    PendingTotal  int64  // raw XPENDING count, for progress
    Unread        int
    Stale         int
    GapPossible   bool
    OldestID      string // oldest wake-relevant id
    HighID        string
    LastDelivered string
}
func (b *Bus) Obligation(ctx context.Context, agent string, horizon time.Duration) (Obligation, error)
func (o Obligation) Wakes() bool  // Pending+Unread > 0
func (o Obligation) Size() int64  // Pending + Unread
```
Wake-relevant = addressed to `agent`, not `wake=no`, id not older than horizon. PEL: list ids (`XPENDING - + 1000`), fetch bodies in one pipeline (`XRANGE id id`); a missing body counts as wake-relevant unless stale. Unread: `XRANGE (LastDelivered +`. No group: same filters over the retained stream, `GroupExists=false`. `GapPossible` = group exists, `LastDelivered` < first retained id, and `XINFO STREAM` entries-added > retained length. `Status` calls `EnsureGroup(ctx, agent, 24*time.Hour)` best-effort.

- [ ] **Step 1: Failing tests** — `TestEnsureGroupCapturesEarlierCommand`; `TestEnsureGroupIdempotent`; `TestObligationIgnoresOtherTargetsAndNoWake`; `TestObligationPendingFiltered` (pending entry for another target / `wake=no` / stale → `Pending==0`, `PendingTotal==1`); `TestObligationPendingMissingBodyCounts` (pending, then `XTRIM MAXLEN 0` → `Pending==1`); `TestObligationNoGroupScansStream`; `TestObligationStaleNotWaking` (XADD explicit id `<now-25h>-0`); `TestGapPossible` (group created at 0, XADD 5, `XTRIM MAXLEN 2` → true; untrimmed → false); `TestStatusCreatesGroup`; `TestArmedAndGroupNamesAndTargets`.
- [ ] **Step 2: Run** `go test ./bus -run 'EnsureGroup|Obligation|Gap|StatusCreatesGroup|ArmedAnd' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(bus): horizon-floor groups, filtered obligation, dispatcher read primitives`

---

### Task 3: `subscribe --drain`

**Files:** Modify `bus/delivery.go`; create `bus/drain_test.go`; modify `cmd/agentbus/subscribe.go`, `cmd/agentbus/main.go` (`subscribe` ~577); test `cmd/agentbus/subscribe_test.go`.

**Interfaces — Produces:** `func (b *Bus) DrainCmd(ctx context.Context, agent, consumer string, horizon time.Duration, deliver func(Event) error) (int, error)`.
- Calls `EnsureGroup` first.
- Receiver lease: same SETNX loop as `WatchCmdDelivery` but bounded by `5s`; still contended → `ErrReceiverBusy`.
- Extract from `WatchCmdDelivery` an unexported `deliverOne(ctx, agent, consumer, token, floor string, block time.Duration, deliver) (handled bool, err error)` covering PEL recovery, missing/expired, skip/ACK, record-attempt, finish. `block < 0` → `XReadGroupArgs{Block: -1}` (go-redis omits BLOCK when negative) and `handled=false` when nothing is available. `WatchCmdDelivery` keeps its behaviour by calling it with `time.Second`; its existing tests are the guard.
- Drain sets `Stale`/`AgeS` from `ServerTime` vs id; returns when `handled==false`.
- CLI: `subscribe --drain <agent>` → one JSON line per entry (`subEvent` gains `Stale bool json:"stale,omitempty"`, `AgeS int64 json:"age_s,omitempty"`; no `rearm`); exit `0` if ≥1, `64` if none, `75` on error (including after output, e.g. ACK failure). `--drain` with `--since`, `--loop` or an `idle_secs` argument → usage error exit 2.

- [ ] **Step 1: Failing tests** — `TestDrainDeliversPendingThenUnreadInOrder`; `TestDrainEmptyReturnsFast` (<300 ms, 0); `TestDrainCreatesMissingGroup`; `TestDrainAcksNonAddressedSilently`; `TestDrainFlagsStale`; `TestDrainCallbackErrorLeavesPending`; `TestDrainReceiverBusy` (hold `ReceiverKey` → `ErrReceiverBusy` after ~5 s; use a 200 ms override via unexported var in test); CLI `TestSubscribeDrainJSONLAndExitCodes` (0/64, every line `"v":1`), `TestSubscribeDrainRejectsIncompatibleFlags`, `TestSubscribeDrainOutputThenAckFailureExit75` (writer that succeeds, then lease deleted before finish → exit 75, one line written).
- [ ] **Step 2: Run** `go test ./bus ./cmd/agentbus -run 'Drain' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./bus ./cmd/agentbus -count=1` — PASS (all existing delivery tests too).
- [ ] **Step 5: Commit** `feat: subscribe --drain empties an agent's queue and exits`

---

### Task 4: `request block` notifies the requester (idempotent)

**Files:** Modify `bus/requests.go` (`requestTransition` Lua); test `bus/requests_test.go`.

**Interfaces — Produces:** on `block`, when previous state ≠ `blocked` or `blocked_reason` differs, XADD `{from: agent, target: r.from, type: "blocked", ref: r.thread, command: reason, task, text_complete: "yes"}` in the same script. Return value unchanged.

- [ ] **Step 1: Failing tests** — `TestBlockNotifiesRequesterOnce`; `TestBlockNewReasonNotifiesAgain`; `TestBlockAfterAcceptNotifies`; `TestBlockedEntryWakesRequester` (`Obligation(requester).Unread==1`).
- [ ] **Step 2: Run** `go test ./bus -run Block -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./bus -count=1` — PASS.
- [ ] **Step 5: Commit** `feat(bus): a blocked request wakes its requester, once per reason`

---

### Task 5: Dispatcher lease, fenced wake records, fenced publishes

**Files:** Create `bus/dispatchkeys.go`, `bus/dispatchkeys_test.go`; modify `bus/projects.go`.

**Interfaces — Produces:**
```go
var ErrNotHolder = errors.New("dispatch lease not held")
func DispatchKey(project string) string
func WakeKey(project, agent string) string
func (b *Bus) AcquireDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error)
func (b *Bus) RenewDispatch(ctx context.Context, token string, ttl time.Duration) (bool, error)
func (b *Bus) ReleaseDispatch(ctx context.Context, token string) error
func (b *Bus) DispatchAlive(ctx context.Context) (bool, error)
func (b *Bus) HoldsDispatch(ctx context.Context, token string) (bool, error)
type WakeRecord struct {
    State         string // prompting | prompted | unobserved | deferred
    At            int64  // server ms of last reservation; 0 = never prompted
    Tries         int
    HighID        string
    PID           int
    PIDStart      uint64
    Escalated     string // "" | "pending" | "true"
    LastDelivered string // progress marks at last reservation
    PendingTotal  int64
    GapNotified   bool
    Reason        string
}
func (b *Bus) Wake(ctx context.Context, agent string) (WakeRecord, bool, error)
func (b *Bus) ReserveWake(ctx context.Context, agent, token string, next WakeRecord, retry, minPTTL time.Duration) (WakeRecord, bool, error)
func (b *Bus) PutWake(ctx context.Context, agent, token string, rec WakeRecord) error    // ErrNotHolder
func (b *Bus) DeleteWake(ctx context.Context, agent, token string) error                 // ErrNotHolder
func (b *Bus) FencedCmd(ctx context.Context, token, from, target, typ, command string) (string, error)
func (b *Bus) FencedNotify(ctx context.Context, token, from, message string) (string, error)
```
All writes are Lua scripts that first check `GET DispatchKey == token` (`ReserveWake` also `PTTL >= minPTTL` and `At==0 || now-At >= retry`, `now` from `TIME`). `FencedCmd`/`FencedNotify` XADD with the same `MAXLEN ~` as `add`. Add `"dispatch"` to `projectKinds`.

- [ ] **Step 1: Failing tests** — `TestDispatchLeaseExclusive`; `TestRenewDetectsLoss`; `TestReserveWakeWindow` (50 ms retry); `TestReserveWakeRequiresLeaseAndPTTL`; `TestFencedWritesRefuseWithoutLease` (PutWake/DeleteWake/FencedCmd/FencedNotify → `ErrNotHolder`); `TestFencedWritesDoNotRecreateDeletedProject` (`DeleteProject`, then each fenced write → error and `EXISTS` of every `{p}:*` key = 0); `TestProjectsSeesDispatchKey`; `TestWakeKeyDoesNotForgeProject`.
- [ ] **Step 2: Run** `go test ./bus -run 'Dispatch|Wake|Fenced' -count=1` — FAIL.
- [ ] **Step 3: Implement** (wake record as hash fields).
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(bus): dispatcher lease with fenced wake records and publishes`

---

### Task 6: Pure decision policy (+ snapshot identity fields)

**Files:** Create `dispatch/decide.go`, `dispatch/decide_test.go`; modify `bus/stream.go` (`AgentSnapshot`, `AgentIdent`, `Status` copy).

**Interfaces — Produces:**
- `bus.AgentSnapshot` and `bus.AgentIdent` gain `PID int json:"pid,omitempty"`, `PIDStart uint64 json:"pid_start,omitempty"`, `Terminal string json:"terminal,omitempty"`; `Status` copies them.
```go
type Inspection struct {
    State   string // idle|working|blocked|done|unknown
    Client  string
    Focused bool
    PIDs    []int
    Starts  map[int]uint64
    Ready   bool
    Busy    bool
    Gone    bool
    Err     error
}
type Action int // ActNone, ActPrompt, ActDefer, ActEscalate
type Decision struct{ Action Action; Rule int; Reason string; ResetTries bool }
type Policy struct{ Retry, EscalateAfter time.Duration; MaxTries int; Supported map[string]bool }
type Input struct {
    Obligation bus.Obligation
    Armed      bool
    Snap       *bus.AgentSnapshot
    In         Inspection
    Wake       *bus.WakeRecord
    OldestAge  time.Duration
    SinceWake  time.Duration // server now - Wake.At
}
func Decide(p Policy, x Input) Decision
func Progressed(w *bus.WakeRecord, o bus.Obligation) bool        // LastDelivered advanced OR PendingTotal decreased
func IdentityChanged(w *bus.WakeRecord, snap *bus.AgentSnapshot) bool
```
Rules = spec §3.2 rows 1–9 (`Rule` = row). `ResetTries` = `Progressed || IdentityChanged`; when set, rules 3–5 evaluate as if `Tries==0`, `Escalated==""` and `At==0`. Identity ok = `Snap.PID ∈ In.PIDs && In.Starts[Snap.PID]==Snap.PIDStart`. Ready = `(idle|done) && !Busy && Ready && !Focused`. Escalate only when `Escalated==""`; `Escalated=="pending"` → `ActEscalate` (resend after crash). `working` escalates only when `OldestAge > 4*EscalateAfter`. Client not in `Supported` → rule 7.

- [ ] **Step 1: Failing tests** — table `TestDecide`, one named case per rule plus: `burst-within-window-none`; `tries-cap-escalates`; `progress-resets-tries-prompts`; `growing-pel-is-not-progress`; `identity-change-resets-and-defers-if-unverified`; `restart-same-pid-new-start-defers`; `kimi-done-but-busy-defers`; `focused-forever-escalates-once` (second call with `Escalated=="true"` → none); `escalation-pending-resends`; `escalated-then-progress-can-escalate-again`; `working-escalates-only-after-4T`; `unsupported-client-never-prompts`; `stale-only-none`. `TestStatusCopiesIdentity` (bus).
- [ ] **Step 2: Run** `go test ./dispatch ./bus -run 'TestDecide|CopiesIdentity' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat(dispatch): pure wake decision policy; snapshot identity fields`

---

### Task 7: Process identity + herdr runtime

**Files:** Create `dispatch/ident.go`, `dispatch/ident_test.go`, `dispatch/herdr.go`, `dispatch/herdr_test.go`, `dispatch/testdata/screens/<client>-<case>.txt`, `dispatch/testdata/herdr/*.json`, `dispatch/testdata/fake-herdr` (shell script answering from fixtures); modify `cmd/agentbus/main.go` (`status` ~116); test `cmd/agentbus/status_ident_test.go`.

**Interfaces — Produces:**
```go
func ProcStart(pid int) (uint64, error)                    // field 22 of /proc/<pid>/stat
func AncestorAmong(candidates []int, self int) (int, bool) // walk ppid chain from self
type Runtime interface {
    Inspect(ctx context.Context, pane string) Inspection
    Prompt(ctx context.Context, pane, text, nonce string) (observed bool, err error)
}
type Herdr struct{ Bin string } // "" → "herdr"
func (h Herdr) Inspect(ctx context.Context, pane string) Inspection
func (h Herdr) Prompt(ctx context.Context, pane, text, nonce string) (bool, error)
func (h Herdr) ForegroundPIDs(ctx context.Context, pane string) (pids []int, terminal string, err error)
type Signature struct{ Ready, Busy []*regexp.Regexp; ANSI bool }
var Signatures map[string]Signature
func Supported() map[string]bool // keys of Signatures
```
- Inspect: `herdr agent get` (state, client, focused, terminal), `herdr pane process-info --pane` (pids → `ProcStart` each), `herdr pane read --source recent --lines 40` (`--ansi` per signature) twice 200 ms apart; differing reads → `Ready=false`. Unknown pane → `Gone`.
- Prompt: `herdr agent prompt <pane> <text>` (3 s; `agent_prompt_stalled` is not an error), then `herdr pane wait-output <pane> --match "[agentbus#<nonce>]" --timeout 5000` → observed.
- Signatures (spec §10): codex Ready = prompt line `›` followed by dim (`\x1b\[2m`) text and no `enter/esc latest` footer (ANSI); kimi Ready `^\s*│ >\s*$`, Busy `^\s*(🌑|🌒|🌓|🌔|🌕|🌖|🌗|🌘)(\s|·|$)`; grok Ready `^\s*│ ❯\s*$`; agy Ready = a line `^>\s*$` between two lines of `─`.
- `agentbus status`, when `HERDR_ENV=1` and `HERDR_PANE_ID` set: `ForegroundPIDs` (1 s) → `AncestorAmong(pids, os.Getpid())` → `ProcStart` → `AgentIdent{PID, PIDStart, Terminal}`; any failure leaves them empty, status still succeeds. `HERDR_BIN` env overrides the binary (test seam).

- [ ] **Step 1: Probe Claude** in a dedicated herdr pane running `claude`: capture empty input, typed input (`send-text`, then `ctrl+u`), running turn, and a permission prompt, with `--ansi`; save fixtures; derive Ready/Busy; update spec §10 Claude column. If no reliable signature: leave `claude` out of `Signatures` and say so in §10.
- [ ] **Step 2: Capture fixtures** for codex/kimi/grok/agy: empty, typed, busy/modal (codex scroll, kimi spinner, agy approval menu), plus an **old-prompt** screen (an earlier `[agentbus#abc123]` line visible) for the nonce test; copy real `agent get` / `process-info` JSON.
- [ ] **Step 3: Failing tests** — `TestAncestorAmongFindsParent`; `TestProcStartStable`; `TestSignatures` (table over fixtures: `agy-approval` Ready=false, `codex-scroll` Ready=false, `kimi-spinner` Busy=true, typed → Ready=false); `TestParseAgentGet`; `TestParseProcessInfo`; `TestPromptObservesOnlyNewNonce` (fake-herdr: old-prompt screen + new nonce absent → observed=false); `TestInspectUnsettledNotReady` (fake-herdr returns two different screens); CLI `TestStatusRecordsIdentityViaFakeHerdr` (fake-herdr reports the test process's parent pid → `agents --json` shows `pid`, `pid_start`, `terminal`).
- [ ] **Step 4: Run** `go test ./dispatch ./cmd/agentbus -run 'Ancestor|ProcStart|Signatures|Parse|Nonce|Unsettled|Identity' -count=1` — FAIL.
- [ ] **Step 5: Implement.**
- [ ] **Step 6: Run** — PASS. Manual (isolated Redis): from this pane, `REDIS_URL=$AGENTBUS_TEST_REDIS_URL agentbus --project probe status me idle && REDIS_URL=$AGENTBUS_TEST_REDIS_URL agentbus --project probe agents --json` shows this pane's agent pid.
- [ ] **Step 7: Commit** `feat(dispatch): herdr runtime, client signatures, process identity`

---

### Task 8: Reconciliation loop and `agentbus dispatch`

**Files:** Create `dispatch/loop.go`, `dispatch/loop_test.go`, `dispatch/testhelp_test.go`; modify `cmd/agentbus/main.go` (new `dispatch` case + usage line); test `cmd/agentbus/dispatch_test.go`.

**Interfaces — Consumes:** Tasks 1–7. **Produces:**
```go
var ErrAlreadyRunning = errors.New("dispatch already running")
var ErrLeaseLost = errors.New("dispatch lease lost")
type Config struct {
    Horizon, Retry, EscalateAfter, Tick, Lease, Renew, MinPTTL time.Duration
    MaxTries   int
    EscalateTo string
    Log        io.Writer
}
func DefaultConfig() Config
func Run(ctx context.Context, b *bus.Bus, rt Runtime, cfg Config) error
func WakeText(project, agent, nonce string, n int64) string
```
`Run`: acquire (`false` → `ErrAlreadyRunning`); renew goroutine (lost → cancel → `ErrLeaseLost`). Loop: `XREAD BLOCK Tick` on `{p}:cmd` from `$` (signal only), then reconcile agents = union(`Agents` keys, `GroupNames`, `CmdTargets`). Per agent:
1. If no group and `CmdTargets` contains it → `EnsureGroup`, then `Obligation`.
2. `Armed`, `Wake`, snapshot, `ServerTime` → if obligation wakes and not armed: `Inspect`.
3. `Decide`; then act:
   - **prompt**: re-`Inspect` and re-`Decide` (spec §3.3 step 0; abort to defer if it changed) → `ReserveWake` (`Tries` = 1 if `ResetTries` else +1; marks from obligation; PID/PIDStart from snapshot) → `HoldsDispatch` → `rt.Prompt(WakeText(..), nonce)` → `PutWake` `prompted`/`unobserved`.
   - **defer**: `PutWake` keeping `At`, `Tries`, marks, `Escalated`; set `State=deferred`, `Reason`.
   - **escalate**: `PutWake Escalated="pending"` → `FencedCmd(token, "dispatch", EscalateTo, CmdEscalation, body)` (skip if agent == EscalateTo) + `FencedNotify` → `PutWake Escalated="true"`. Body `"<agent>: <reason>; <n> waiting, oldest <id>"`.
   - **none** with no obligation → `DeleteWake`.
   - `GapPossible && !GapNotified` → `FencedNotify` once, then `GapNotified=true`.
   Any `ErrNotHolder` → return `ErrLeaseLost`. Other errors: log, backoff 1 s doubling to 30 s, continue.
4. One JSON log line per decision: `{"agent","action","rule","reason","high_id","latency_ms"}`.

CLI: `agentbus [--project P] dispatch [--horizon D] [--escalate-after D] [--escalate-to NAME]` (wires `dispatch.Herdr{Bin: os.Getenv("HERDR_BIN")}`); `ErrAlreadyRunning`/`ErrLeaseLost` → message, exit 0. `agentbus dispatch --status` → `dispatch up|down`, exit 0/1.

- [ ] **Step 1: Failing tests** (fake `Runtime` with scripted inspections, recorded prompts and injectable hooks; ms-scale `Config`):
  `TestRunWakesUnarmedIdleAgent`; `TestRunSkipsArmedAgent`; `TestRunBurstOnePrompt`;
  `TestRunCrashAfterPromptNoSecondPromptInWindow` (hook cancels `Run` right after `Prompt` returns, before `PutWake`; restart → no prompt until window passes);
  `TestRunLeaseLostAfterReserveNoPrompt` (hook deletes the lease after `ReserveWake` → no `Prompt`, `Run` returns `ErrLeaseLost`);
  `TestRunReadinessChangedAtRecheckDefers` (second `Inspect` returns `Focused` → no prompt);
  `TestRunCrashDuringEscalationResendsOnce` (hook cancels after `Escalated="pending"` → restart sends exactly one escalation);
  `TestRunSecondDispatcherExits`; `TestRunLeaseLapseWakes`; `TestRunRequestDoneWakesRequester`; `TestRunRequestBlockWakesRequester`; `TestRunStaleOnlyNoWake`; `TestRunNoWakeEntryNoPrompt`; `TestRunCreatesGroupForUngroupedTarget`; `TestRunGapNotifiedOnce`; `TestRunEscalationTargetsRoleNotPilot`; `TestRunSelfEscalationNotifiesOnly`;
  Review Focus: `TestRunRuntimeErrorDefersAndEscalatesOnce` (#1), `TestRunUnregisteredTargetEscalatesOnce` (#2), `TestRunProjectDeletedStops` (#4: `DeleteProject` mid-run → `ErrLeaseLost`, then zero `{p}:*` keys), `TestRunLargeBacklogOnePromptThenDrainInOrder` (#5: 1000 entries → one prompt containing `1000 message(s)`; `DrainCmd` then returns 1000 ids ascending);
  CLI `TestDispatchStatusExitCodes`, `TestDispatchAlreadyRunningExit0`.
- [ ] **Step 2: Run** `go test ./dispatch ./cmd/agentbus -run 'TestRun|TestDispatch' -count=1` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test ./... -count=1 -race` with `AGENTBUS_TEST_REDIS_URL` — PASS; `go vet ./...`.
- [ ] **Step 5: Commit** `feat: agentbus dispatch wakes unarmed agents through herdr`

---

### Task 9: busmon indicators

**Files:** Modify `cmd/busmon/render.go` (`statusBar` ~185, `agentLabel` ~532), `cmd/busmon/main.go` (1 s ticker); test `cmd/busmon/render_test.go`.

**Interfaces — Consumes:** `DispatchAlive`, `Obligation`, `Wake`. **Produces:** `statusData.dispatch` (`"" | "up" | "down"`) → `dispatch ●` / `dispatch ○` (nothing for `""`); `agentState` gains `waiting int64, stale int, gap bool, wake *bus.WakeRecord` → suffixes `✉N` (waiting>0, no wake record), `⏰` (state deferred/prompting/prompted/unobserved), `⚠` (`Escalated` non-empty), `⌛N`, `✂?`.

- [ ] **Step 1: Failing tests** — `TestStatusBarDispatch`; `TestAgentLabelWakeBadges` (one case per badge; none when zero).
- [ ] **Step 2: Run** `go test ./cmd/busmon -run 'Dispatch|WakeBadges' -count=1` — FAIL.
- [ ] **Step 3: Implement**; ticker errors keep the last value (as for budgets).
- [ ] **Step 4: Run** `go test ./cmd/busmon -count=1` — PASS.
- [ ] **Step 5: Commit** `feat(busmon): dispatcher liveness and per-agent wake badges`

---

### Task 10: Bootstrap tab, skills, docs, release

**Files:** `scripts/bootstrap` (`write_template` Python block ~71–85), `tests/bootstrap_test.sh`, `skills/agent-bus/SKILL.md`, `skills/agent-bus-master/SKILL.md`, `skills/agent-bus-sentinel/SKILL.md`, `CLAUDE.md`, `docs/AGENT-BUS-GUIDE.md`, `README.md`, `docs/memory/decisions/2026-10-10-event-driven-dispatch.md` + `docs/memory/INDEX.md`, `docs/releases/v0.8.0.md`.

**Content:**
- `bootstrap`: the template gains a `[[tabs]] name = "dispatch"` whose command runs `agentbus --project <p> dispatch` (next to the existing `busmon` tab); `--no-dispatch` omits it. Contract unchanged: bootstrap still only writes the template; the human opens the workspace.
- `agent-bus`: wake line → `subscribe --drain <self>` first, never a plain `subscribe` (it acks older entries unseen); don't re-arm to wait while `dispatch ●`; never act on `stale` entries without confirmation.
- `agent-bus-master`: sleep allowed only while `agentbus dispatch --status` is up; v0.7 armed rule otherwise; manual pane wake = fallback; `escalation` and `blocked` types explained.
- `agent-bus-sentinel`: each cron wake runs `agentbus dispatch --status`; exit 1 → `notify` the human.
- `CLAUDE.md`: replace the two "don't reintroduce …daemon" lines with the spec §11 rule; document `dispatch/`, `{p}:dispatch`, `{p}:wake:{agent}`, `--drain`, `wake=no`, fenced writes.
- GUIDE/README: `--drain`, `--no-wake`, `stale`/`age_s`, `escalation`/`blocked`, `dispatch` and `dispatch --status`.
- Memory decision note per `docs/memory/PROTOCOL.md` (decision, reason, evidence: spec, probes, PR), linked from `INDEX.md`.

- [ ] **Step 1:** Add to `tests/bootstrap_test.sh`: template contains a `dispatch` tab running `agentbus --project <p> dispatch`; `--no-dispatch` → no such tab. Run `bash tests/run.sh` — FAIL.
- [ ] **Step 2:** Implement bootstrap; edit skills, CLAUDE.md, GUIDE, README, memory note.
- [ ] **Step 3:** Run `bash tests/run.sh && python3 scripts/check-memory.py && git diff --check` — PASS.
- [ ] **Step 4: Live acceptance** on an **isolated Redis** (`REDIS_URL=$AGENTBUS_TEST_REDIS_URL`), binary built from this branch (`go build -o /tmp/claude-1000/agentbus-v08 ./cmd/agentbus`, record `git rev-parse HEAD` and `agentbus version`), project `v08-accept`, panes codex/kimi/grok/agy(/claude if supported) each running `agentbus status <name> idle` once then left unarmed. Run `agentbus dispatch`. Record in `docs/releases/v0.8.0.md`: (a) `request send` to each → one prompt each (dispatch log), drain, accept/done; publish→prompt latency; (b) requester asleep → woken by the reply; (c) focused peer pane → no prompt, `⏰` then `⚠` with `--escalate-after 1m`; (d) text typed in a peer's input → deferred; (e) 30 min idle: dispatch log shows zero prompts, herdr state stays `idle` for every pane (sampled each minute), and token evidence where the client exposes it (Claude transcript usage lines, Codex `~/.codex/sessions` jsonl, Kimi/agy/Grok: none available — stated as such).
- [ ] **Step 5: Commit** `docs: v0.8 dispatch — bootstrap tab, skills, guide, release notes`
