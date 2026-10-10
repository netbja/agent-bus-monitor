# Event-Driven Dispatch (v0.8) — Design Spec

- **Date**: 2026-10-10
- **Status**: Revision 3 — Codex spec and plan reviews integrated, runtime probes run
- **Base**: `origin/main` @ 6848194 (v0.7.0 — provider-neutral role profiles)
- **Scope**: a deterministic `agentbus dispatch` process that wakes disconnected
  agents through herdr when Redis holds work for them; `subscribe --drain`;
  additive protocol changes; skill rule changes; busmon indicators.
- **Reviews**: Codex counter-audit (C1…C18) and Codex spec review of 9734c8c
  (R-B1…R-B5 blocking, R-1…R-9 to fix); each is answered in place and in §13.

## 1. Problem

Since PR #38 an idle peer **disconnects**: it stops re-arming `subscribe`, so it
costs no tokens. A `cmd` or tracked request addressed to it is retained in Redis,
but nothing hands it over. Today the **master** — an LLM — brings the peer back by
resolving its pane and injecting text with `herdr pane send-text`. That is
mechanical work done by the most expensive participant, and it requires the
master itself to stay armed (`agent-bus-master` SKILL: "You are the exception:
you stay armed"), i.e. to keep waking on every idle window.

Goal: **an agent can stay idle indefinitely at zero token cost; a task published
for it activates it automatically; its result reaches the requester the same way;
no LLM takes part in ordinary routing.** Client agnostic across the clients v0.7
can launch and herdr can detect (§10).

## 2. What already exists (do not rebuild)

| Need | Existing mechanism |
|---|---|
| Blocking read, no polling | `Bus.WatchCmdDelivery` — `XREADGROUP BLOCK`, one group per agent (`bus/delivery.go`) |
| Unacked recovery | PEL drained before new entries; ACK only after complete output; a PEL entry whose body was trimmed → `missing` |
| One consumer per agent | Receiver lease `{p}:receiver:{agent}` with fencing token |
| Request state | Board state `requested`/`accepted`/`blocked`/`done`; separately, delivery `queued`/`output_written`; TTL makes a request's *availability* `expired` (derived, not a board state) (`bus/requests.go`) |
| Duplicate request guard | A task name cannot be re-published **while its board entry exists**; delivery exposes `duplicate_possible` + `attempt` |
| Correlation | Threads (`ref`); `CompleteRequest` atomically XADDs a `reply` **targeted at the requester** |
| Pane location | `AgentSnapshot.Pane` (= `HERDR_PANE_ID`), `Session` (= `CLAUDE_CODE_SESSION_ID`) in `{p}:agents` |
| Runtime state | herdr `agent get`: `agent_status`, `focused`, `terminal_id`; `pane process-info`: foreground pid(s); `agent prompt`; `pane wait-output` |

Delivery exists; **execution is not guaranteed** (C1): a crash after ACK but before
`accept` is not replayed. v0.8 keeps that limit. Entries trimmed by the ~1000 cap
**before** an agent's group read them are lost to that agent (they never reach its
PEL); v0.8 does not change the cap, it reports the gap (§6) (R-1).

## 3. Architecture

```
peer X ──cmd/request──▶ {p}:cmd ──XREAD BLOCK (signal only)──▶ agentbus dispatch
                                                              │ reconcile every agent A
  obligation(A) = PEL(group A) ∪ unread entries addressed to A, excluding wake=no
  A armed (receiver lease present)?  → nothing to do; subscribe owns it
  else identity(A) = pane pid/start-time match → runtime.Inspect(pane)
     ready (idle|done, not busy by client signature, input empty, prompt line
            visible, pane NOT focused) → reserve wake → runtime.Prompt → observe nonce
     anything else → defer (tick revisits); escalate when the obligation is older than T
A woken → `agentbus subscribe --drain A` → handles → request accept/done
done → reply targeted at requester → same path wakes the requester (completion bridge)
A has nothing left → status idle, does not re-arm → 0 tokens
```

The dispatcher holds **no durable local state**: every decision is recomputed from
Redis (groups, receiver leases, `{p}:agents`, `{p}:wake:*`) plus herdr. `XREAD
BLOCK` on `{p}:cmd` from `$` only shortens latency; a tick (default 5 s) revisits
deferred agents. A restart recomputes everything (C4, C8, C9).

### 3.1 Obligation

For agent A with group A on `{p}:cmd`:

- **PEL**: pending entries of group A, filtered like unread entries (addressed to
  A, not `wake=no`, not stale). A pending entry whose body was trimmed is
  wake-relevant (the drain must report it as `missing`) unless its id is stale.
- **Unread**: `XRANGE (last-delivered-id +` filtered to `target == A`, bounded by
  the stream cap.
- **No group**: entries in the retained stream addressed to A also count. The
  dispatcher creates the missing group (§5.1) and, if A has no usable snapshot,
  the obligation follows the escalation path (R-B2).
- Entries with `wake=no` do not count (they are still delivered by a drain).
- **Stale** entries (older than `--horizon`, default 24h, from the id timestamp)
  never trigger a wake on their own. They **are** delivered by a drain that runs
  for another reason, flagged `"stale": true` with their age; the agent skill
  says: never act on a stale entry without confirmation from its sender or master
  — report it instead. Nothing is acknowledged unseen, nothing is silently
  discarded (lesson `pel-is-not-backlog`) (R-B3).

### 3.2 Readiness and decision policy (pure function)

`decide(obligation, armed, snapshot, inspection, wake record, now) → action`,
action ∈ {`none`, `prompt`, `defer`, `escalate`}. Evaluated top to bottom:

| # | Situation | Action |
|---|---|---|
| 1 | No obligation, or only stale entries | none; delete `{p}:wake:A` |
| 2 | Receiver lease present | none (if it lapses with the obligation still there, a later tick handles it) (C4, C5) |
| 3 | `wake.escalated` for this obligation and no progress since | none (already escalated; human/master owns it) |
| 4 | `wake.tries` ≥ 3 and no progress since the last wake | escalate (C6) |
| 5 | Last wake `at` younger than retry delay (60 s) — **whatever the new ids** | none (one wake per agent per window; bursts cannot bypass it) (R-2) |
| 6 | No snapshot, or identity mismatch (§5.2) | defer; escalate once oldest obligation age > T (C10, C11) |
| 7 | Runtime error / pane gone / client unsupported (§10) | defer; escalate at T |
| 8 | Not *ready* (below) | defer; escalate at T (`working` alone never escalates before 4×T) |
| 9 | Ready | prompt |

**Ready** = all of: herdr state `idle` or `done`; the client's **busy signature**
does not match (herdr's state is not trusted alone — Kimi 2.1 reports `idle`/`done`
while its spinner runs, §10); the client's **ready signature** matches **on the
current input box only** — the region from the last line matching the client's
input-line marker (`Prompt`; for claude and agy from the `─` rule above it) to the
end of the screen, so an old box in scrollback never counts; no marker → not ready
— (empty input line visible, no modal footer — e.g. Codex transcript-scroll mode where Enter does
not submit); the screen is **settled** (two reads 200 ms apart identical); and the
pane is **not focused** in herdr (R-B1). A focused pane with no herdr client
attached is not a typing risk; whether `herdr status client` exposes attachment
reliably is verified during implementation — until then focused means defer (the
focused agent then reaches the human through escalation at T).

**Progress** = group A's `last-delivered-id` advanced, or its pending count
**decreased**, since the last reservation (a growing PEL is not progress).
Progress, or an identity change (pid or pid start time differs from the wake
record), resets `tries` and `escalated` — a new stall can be escalated again.
A `defer` keeps `at`, `tries`, the progress marks and `escalated` unchanged.

**Escalation clock**: the age of the oldest obligation entry, read from its stream
id. It is durable by construction — a dispatcher restart does not reset it (R-2).
T defaults to 10 min (`--escalate-after`).

### 3.3 Wake procedure (bounded duplicates)

0. **Re-check** — immediately before reserving, re-run `Inspect` (focus and
   readiness can change between the first inspection and the prompt).
1. **Reserve** — Lua CAS on `{p}:wake:A`, fenced by the dispatcher lease and
   requiring lease PTTL > 5 s: write `{state: prompting, at, pid, tries+1,
   high_id}` only if rule 5 still allows it. A crash after this point and before
   the prompt costs one retry window, never a burst.
2. **Prompt** — `GET {p}:dispatch` == token, then `herdr agent prompt <pane>
   <wake text>` with a 3 s timeout.
3. **Observe** — `herdr pane wait-output --match "[agentbus#<nonce>]"` (5 s), where
   `<nonce>` is 6 random hex chars new to this prompt, so an old screen cannot
   match. This observes that the line reached the screen; it is **not** proof the
   agent submitted or took the work (the drain/progress is). Herdr's
   `agent_prompt_stalled` is not used (it fired for Kimi on a successful
   submission). Record `state: prompted` or `state: unobserved`.

Bound (R-B4): at most **one prompt per agent per 60 s window** and **3 per
obligation** before escalation, across dispatcher crashes. An old dispatcher
whose lease expired cannot reserve. **Residual, accepted:** a dispatcher process
suspended for longer than the 5 s minimum PTTL *between* the token check of step 2
and the herdr call can still emit one extra prompt; nothing in Redis can fence an
external side effect against an arbitrary pause. An extra wake is not free:
it costs one short turn (an empty drain). It never lands in an active turn,
because readiness excludes busy agents.

The prompt carries **no message body** — peer content is never typed into a
terminal:

```
[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus --project <project> subscribe --drain <agent>
```

### 3.4 Escalation

An `escalation` cmd targeted at the **escalation recipient** — `--escalate-to`,
default the role name `master` — not the current pilot-lease holder, which expires
while master sleeps (R-5). Body: agent, reason, obligation size, oldest id. Plus a
`notify` for the human. It wakes master through the same path. Order: write
`escalated: pending` (fenced) → XADD escalation + notify (fenced Lua, so a deleted
project is never recreated) → `escalated: true`. A crash in between resends on
restart: at-least-once, a duplicate only after a crash. If the stuck agent
**is** the recipient: `notify` only (no recursion), `⚠` in busmon. One escalation
per obligation (recorded in the wake hash).

## 4. Components

- **`dispatch/` (new package)** — reconciliation loop, `decide`, wake procedure,
  the `Runtime` interface. No herdr import outside `dispatch/herdr.go`.

  ```go
  type Inspection struct {
      State     string // herdr: idle|working|blocked|done|unknown
      Client    string // herdr "agent": claude|codex|kimi|grok|...
      Focused   bool
      PIDs      []int  // pane foreground processes
      Ready     bool   // client ready signature matched on a settled screen
      Busy      bool   // client busy signature matched
      Gone      bool
  }
  type Runtime interface {
      Inspect(ctx context.Context, pane string) Inspection // errors in Inspection.Err
      Prompt(ctx context.Context, pane, text, nonce string) (observed bool, err error)
  }
  ```
  The wake prompt *is* the signal; delivery stays in Redis. An ACP runtime (v0.9)
  would implement the same two methods.

- **`dispatch/herdr.go`** — the only `Runtime` in v0.8. Per-client **signature
  table** (input-line marker + ready + busy + modal regexes; ready is matched only
  on the last input box, busy and modal on the whole screen; matched on `herdr pane read --source recent` with
  ANSI where needed). Entries ship only for clients that passed §10; any other
  client is unsupported → never prompted.

- **`bus/`** (protocol primitives, per CLAUDE.md) — `Bus.Obligation`, dispatcher
  lease, wake-record CAS, group creation, `block` notification, `wake=no`, the
  `stale` flag in `ParseEntry`/drain.

- **`cmd/agentbus`** — `dispatch`; `subscribe --drain`; `cmd --no-wake`.

- **`cmd/busmon`** — indicators (§5.6).

## 5. Protocol changes (all additive; `ProtocolVersion` stays 1)

### 5.1 Group created early, at the horizon floor
The agent's group on `{p}:cmd` is created at id `<now − horizon>-0` (not `$`) by
`agentbus status` and by the dispatcher when it finds addressed entries for an
agent without a group (`XGROUP CREATE … MKSTREAM`, ignore `BUSYGROUP`, idempotent).
Commands sent shortly before an agent registers are therefore captured; a failed
creation in `status` is retried by the dispatcher (R-B2).

The existing contract is unchanged: a plain `subscribe` (no `--since`) still
floors at the server's "now" (`ServerFloor`) and **acknowledges older entries
unseen** (`cmd/agentbus/main.go`, `subscribe` case). That is exactly why a woken
agent must use `--drain` (no floor), and why the `agent-bus` skill says: on a wake
line, drain first, never a plain `subscribe`. Documented in the GUIDE.

### 5.2 Snapshot gains process identity
`agentbus status`, when `HERDR_ENV=1`, resolves `herdr pane process-info --pane
$HERDR_PANE_ID`, keeps the foreground pid that is **an ancestor of its own
process** (walking `/proc/<pid>/stat` ppid), and records `AgentSnapshot.PID` plus
`PIDStart` (field 22 of `/proc/<pid>/stat`). The dispatcher prompts only if that
pid is among the pane's current foreground processes **and** its start time
matches — so a recycled pane id, or an agent restarted inside the same terminal,
is never mistaken for the registered agent (R-B5). `terminal_id` is recorded too,
for display only. A snapshot without `PID` (old binary, non-herdr) is
unverified → never prompted. Requires dispatcher and herdr on the same host —
true by construction (it calls herdr's local socket).

### 5.3 Stream fields and types
- `wake=no` on cmd entries suppresses dispatcher wakes; `agentbus cmd --no-wake`.
  `shutdown` uses it: a disconnected peer is already stopped. `shutdown` entries
  also carry `expires_at` = Redis TIME + 10m (`Bus.CmdNoWakeTTL`), so a drain by a
  later session yields a payload-less `expired` error, never a live stop order.
- `escalation` cmd type (§3.4).
- `request block` XADDs a `type=blocked` entry targeted at the requester inside
  the existing Lua transition, **only when the state changes into `blocked` or the
  reason changes** — a repeated identical block publishes nothing (R-6).
- `report` and `notify` remain non-waking by design; only `{p}:cmd` drives the
  dispatcher (C16).
- Drain/subscribe events gain `stale` (bool) and `age_s` (additive).

### 5.4 New keys
- `{p}:dispatch` — lease (`SET NX PX 15000`, token-fenced renew every 5 s /
  release). One dispatcher per project; a second exits 0 with a message. Liveness
  signal for busmon/sentinel/master. Added to `projectKinds`.
- `{p}:wake:{agent}` — hash `{state, at, tries, high_id, pid, pid_start, escalated,
  last_delivered, pending_total, gap_notified, reason}`. Two colons → cannot forge a project.

### 5.5 `subscribe --drain <agent>`
`WatchCmdDelivery` semantics (lease, PEL first, ACK after output), **no floor, no
`--since`** — the group cursor only. One JSON object per entry (JSONL, each with
`"v":1`), exits as soon as PEL and unread set are empty. Exit 0 if ≥1 entry, 64 if
none. The one-shot `subscribe` contract is unchanged.

### 5.6 busmon
STATUS bar: `dispatch ●` / `dispatch ○`. Agent chip: `✉N` waiting obligation,
`⏰` deferred wake, `⚠` escalated, `old:N` stale entries, `✂?` possible gap
(entries may have been trimmed before the group read them).

## 6. Error handling

| Failure | Behaviour |
|---|---|
| Dispatcher crash / restart | Recompute from Redis; wake reservations and escalation clocks survive (C8) |
| Old dispatcher after lease loss | Cannot reserve (§3.3 step 1); ≤1 in-flight prompt (R-B4). `dispatch.Run` returns `ErrLeaseLost` |
| Lease lapsed while the project lives on (suspend, broker restart) | `agentbus dispatch` sees `ErrLeaseLost`, checks `Bus.HasKeysBesidesDispatch` (`ProjectKeys` minus `{p}:dispatch`): keys remain → logs, backs off 1 s doubling to 30 s and runs `dispatch.Run` again, which re-acquires; errors while re-acquiring (broker still down) are retried, never fatal. If another dispatcher took the lease meanwhile → `ErrAlreadyRunning`, exit 0 |
| Project deleted while a dispatcher runs | Same `ErrLeaseLost`; no key besides the lease → exit 0 without re-acquiring, so no key is recreated |
| Two dispatchers | Lease; loser exits |
| Burst of N entries | One wake per agent per window (R-2) |
| Lease lapses with obligation pending | Next tick handles it (C4) |
| PEL entry whose body was trimmed | Drain emits `missing` |
| Entry trimmed before the group read it | Unrecoverable for that agent and not countable exactly. Gap *possible* when group `last-delivered-id` < first retained id and the stream has entries added since; busmon `✂?`, one `notify` (flag persisted in the wake record) (R-1) |
| Redis or herdr error | JSON log line (correlation id = cmd entry id), capped backoff to 30 s; never exits on a transient error |
| herdr socket absent | Every agent deferred → escalation path; dispatch keeps running |
| Escalation recipient is the stuck agent | `notify` only, `⚠` |
| `busmon --delete` while a dispatcher runs | Accepted residual: `DeleteProject` is SCAN then DEL, so a fenced write landing in that gap (a `{p}:wake:*` key, or a `notify` stream) can be orphaned. It cannot forge a project, and the dispatcher stops on its next lease check (`ErrLeaseLost`) |

Logs: one JSON line per decision on stderr (`agent`, `action`, `rule`, `high_id`,
`latency_ms` from oldest obligation entry to prompt).

## 7. Skill and role changes

| Skill | Change |
|---|---|
| `agent-bus` | Do not re-arm to *wait for an answer* when `dispatch ●`; the dispatcher wakes you when the reply lands. On a wake line: `subscribe --drain`, handle, `status idle`, stop. Never act on a `stale` entry without confirmation. Armed `subscribe` remains valid. |
| `agent-bus-master` | **Conditional**: while `dispatch ●`, master may sleep — replies, blocked requests and escalations wake it — **only if its pane is not the one the human works in**: the dispatcher never types into a focused pane (§3.2), so while the human is (or may be) focused on master's pane, master stays armed while it awaits replies or escalations. While `dispatch ○`, the v0.7 rule stands unchanged: master stays armed and wakes peers manually (R-3). Manual pane injection by master is the sanctioned fallback, not a violation of §11. |
| `agent-bus-sentinel` | On each external (cron) wake: check `{p}:dispatch`; if absent, `notify` the human. The sentinel is woken by machine cron, not by the dispatcher, so it can detect the dispatcher's death (R-3). |
| `scripts/bootstrap` | Opens a `dispatch` pane running `agentbus --project <p> dispatch`; opt-out flag. No systemd. |
| `CLAUDE.md` | Replace the two "don't reintroduce a pane-relay / watcher daemon" lines with the §11 rule. |

## 8. Compatibility

- Without a dispatcher, behaviour is v0.7: master stays armed and wakes manually
  (§7 is conditional on `dispatch ●`).
- Stream formats, one-shot `subscribe` JSON and board schema unchanged; new
  fields/types are additive. §5.1 changes *when* a group is created; documented.
- Old-binary agents (no `PID` in snapshot) are never auto-prompted.

## 9. Testing

- **Unit (no Redis)**: `decide` table-driven, one case per §3.2 row and §6 line,
  including burst-within-window, tries cap, progress reset, identity change,
  focused pane, busy-signature override of herdr `done`.
- **Signature fixtures**: captured pane screens (empty / typed / modal / busy /
  approval) per supported client, from the §10 probes.
- **Integration (isolated Redis only, `AGENTBUS_TEST_REDIS_URL`)**, fake `Runtime`:
  crash after reservation and after prompt (≤1 extra wake per window); lease loss
  (no reservation); two dispatchers; burst; lease lapse; `request done` and
  `request block` wake the requester; repeated block → one notification; stale
  entry → no wake, delivered flagged by a later drain; `wake=no`; group created at
  horizon floor captures a command sent before registration; trimmed-before-read
  counted.
- **`--drain`**: PEL first, then unread; ACK after output; empty → exit 64.
- **Identity**: pid-ancestor resolution; restarted agent in same pane rejected.
- **Live acceptance** (recorded in `docs/releases/v0.8.0.md`): idle Claude, Codex,
  Kimi, Grok panes — publish→prompt latency; zero transcript growth over 30 min
  idle; no prompt while the pane is focused or input non-empty; full loop
  master → coder request → done → master woken.

## 10. Runtime probes (2026-10-10, herdr session `busmon`)

| Probe | Claude 2.1.296 | Codex 0.162.1 | Kimi 2.1.1 | Grok | agy (Antigravity) |
|---|---|---|---|---|---|
| herdr detects client | yes (`claude`) | yes (`codex`) | yes (`kimi`) | yes (`grok`) | yes (`agy`) |
| `agent prompt` submits | yes (dedicated pane, Task 7) | yes | yes (but returned `agent_prompt_stalled`) | yes | yes |
| herdr `working` during a turn | yes (`idle→working→done`) | yes (`idle→working→done`; controller probe 2026-10-10 15:12, `working` for 13 s during a `sleep 8` turn) | **no** — reported `idle`, then `done`, while spinner ran | yes (`idle→working→done`) | yes (`idle→working→idle`) |
| herdr `blocked` on approval | yes, on an `AskUserQuestion` menu (permission prompt not reproducible: the session runs in bypass-permissions mode) | — | yes | — (ran without approval) | yes |
| Empty-input signature | `❯` + NBSP directly under a `─` rule, optionally a **dim** (`ESC[2m`) placeholder or prompt suggestion; the glyph turns grey during a turn | `› ` + **dim** (`ESC[2m`) placeholder | `│ >` + spaces | `│ ❯` + spaces | `>` alone between two `─` rules |
| Non-empty input visible | yes (not dim) | yes, after render delay | yes | yes | yes |
| Modal hazard | question menu `❯ 1. …` / `Esc to cancel` replaces the input (→ blocked) | transcript-scroll mode: footer `enter/esc latest`, Enter does not submit | approval panel (→ blocked) | — | approval menu has a `> 1. Yes…` line (→ blocked); `esc to dismiss` banner |
| Clear input | `ctrl+u` | `ctrl+u` | `ctrl+u` | `ctrl+u` | `ctrl+u` |
| Busy signature (beyond herdr state) | spinner line `✶ Kneading… (2s · ↓ 91 tokens)` (glyph `·✢✳✶✻✽`, verb + `…`); the input box stays empty during a turn | — | moon glyph `🌑…🌘` + space/`·` | — | — |

Gemini CLI is no longer used here; Antigravity (`agy`) replaces it.

Task 7 additions (2026-10-10): Claude was probed on a dedicated pane (Claude Code
2.1.296); its signature ships. Kimi's and Grok's input boxes carry a right border
on screen (`│ >   …   │`, `│ ❯   …   │`), so their ready signatures allow it.
`herdr pane wait-output --match` is a literal substring search that includes
output already on screen, and it matched the wake line while it was still in the
input box — observation is "reached the screen", as §3.3 says. Errors from the
herdr CLI are JSON objects on **stderr** with exit 1 (`agent_not_found`,
`pane_not_found`, `timeout`); a pane running no detected agent also answers
`agent_not_found`. Fixtures and their provenance: `dispatch/testdata/screens/README.md`.

Pane ids were not reused within the session (`w1:p2` then `w1:p3`); `terminal_id`
is unique per pane. §5.2 makes identity independent of both.

Findings driving the design: herdr state is necessary but **not sufficient** (Kimi
busy detection: herdr's `kimi.toml` 2026.06.10 moon-spinner rule expects a line
with only the glyph; Kimi 2.1 prints `🌑 · Tip: …`) → per-client busy signature;
screens render asynchronously (Codex) → settle check; modal states exist →
positive ready signature rather than "input not non-empty". **Claude must be probed
on a dedicated pane before its signature ships** (§4: unprobed client → never
prompted). Reporting the Kimi rule upstream to herdr is a separate decision.

## 11. Revising the "no pane-relay daemon" decision

CLAUDE.md says "Don't reintroduce a pane-relay daemon" and "Don't reintroduce a
`Restart=always` watcher daemon". **This spec revises that decision on purpose: `agentbus
dispatch` is a permanent process that injects wake lines into panes** (R-7).

The recorded reason for dropping `cmd/busbridge` (commit 7818b39, 2026-05-23): it
relayed `hermes:cmd:*` into herdr panes **with hard-coded pane IDs**, and
`bus_watch.sh` "needs no pane map and rides Claude Code's background-task model
directly". What changed since:

- Clients other than Claude Code are first-class (v0.7). Whether their
  background-task exit wakes a session is **unverified**; v0.7's own `agent-bus`
  skill says to verify it before relying on it. The dispatcher does not depend on
  that capability for any client (R-8).
- Staying reachable through wake-on-exit means re-arming every idle window — the
  cost PR #38 removed by disconnecting idle peers.
- A pane map now exists and is self-registered (`AgentSnapshot.Pane`), and v0.8
  adds a verifiable process identity (§5.2) — the hard-coded map was the problem.

What differs from `busbridge`, per 7818b39: no message content is typed (a fixed
wake line only); no hard-coded pane ids (self-registered, pid-verified); readiness
checks (busy, modal, input, focus) before typing; delivery stays in
`WatchCmdDelivery`; only unarmed agents with a real obligation are touched.

New CLAUDE.md rule: *the only automated process that types into panes is `agentbus
dispatch`, and it types a wake line, never a message; master's manual pane
injection remains the fallback while no dispatcher runs.*

## 12. Out of scope (v0.9+)

An ACP runtime (Kimi/OpenCode ship ACP servers; Claude/Codex via
adapters). Whether it would own headless sessions or attach to existing ones is
an open architecture choice, not something ACP imposes (R-9, C18). Also: `codex
queue`; task-level retry/backoff and DLQ; per-task token metrics; structured memory
and skill self-improvement (Brigade P2).

## 13. Review traceability

| Review point | Resolution |
|---|---|
| R-B1 human typing race (C12) | Pane must be unfocused (keyboard goes to the focused pane) + settled empty ready signature; residual: focus switch inside the inspect→prompt gap (<100 ms) |
| R-B2 no group / late group (C11, C8) | Ungrouped addressed entries count; group created at horizon floor by status and dispatcher (§3.1, §5.1) |
| R-B3 stale vs drain (C14) | Stale delivered flagged, never acted on without confirmation; never acked unseen (§3.1) |
| R-B4 old dispatcher / duplicate bound (C6, C7) | Re-check, fenced reservation, token check before prompt, one per window, 3 per obligation; residual under process suspension stated (§3.3) |
| R-B5 identity (C10) | pid + start time, ancestor-verified (§5.2) |
| R-1 trimmed before read | Stated as a limit; surfaced as a *possible* gap, not a count (§2, §6) |
| R-2 burst / clock | Window per agent regardless of ids; clock from oldest entry id (§3.2) |
| R-3 master sleep vs compat; sentinel | Conditional on `dispatch ●`; sentinel cron check (§7) |
| R-4 expired is availability, not state | §2 corrected |
| R-5 escalation target | Durable role name, not pilot holder (§3.4) |
| R-6 block idempotence; report/notify | Publish on transition/reason change only; non-waking stated (§5.3) |
| R-7 own the revision | §11 |
| R-8 v0.7 skill claim; busbridge sources | Reworded; 7818b39 cited (§11) |
| R-9 ACP wording | §12 |
| "stateless" wording | "no durable local state" (§3) |
