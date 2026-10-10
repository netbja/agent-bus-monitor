# Event-Driven Dispatch (v0.8) — Design Spec

- **Date**: 2026-10-10
- **Status**: Approved in brainstorm (sections 1–3), pending written-spec review
- **Base**: `origin/main` @ 6848194 (v0.7.0 — provider-neutral role profiles)
- **Scope**: a deterministic `agentbus dispatch` process that wakes disconnected
  agents through herdr when Redis holds work for them; `subscribe --drain`;
  additive protocol changes; skill rule changes; busmon indicators.
- **Reviewers**: Codex counter-audit (2026-10-10) shaped §4–§6; its numbered
  points are cited as (C1)…(C18).

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
no LLM takes part in ordinary routing.** Model/client agnostic across the clients
v0.7 can launch (Claude Code, Codex, Kimi; Gemini later).

## 2. What already exists (do not rebuild)

| Need | Existing mechanism |
|---|---|
| Blocking read, no polling | `Bus.WatchCmdDelivery` — `XREADGROUP BLOCK`, one group per agent (`bus/delivery.go`) |
| Unacked recovery | PEL drained before new entries; ACK only after complete output; trimmed bodies → `missing` |
| One consumer per agent | Receiver lease `{p}:receiver:{agent}` with fencing token |
| Task state | Tracked requests on the board: `requested`/`accepted`/`blocked`/`done`, delivery `queued`/`output_written`, TTL → `expired` (`bus/requests.go`) |
| Idempotence | A task name cannot be re-published; `duplicate_possible` + `attempt` on delivery |
| Correlation | Threads (`ref`); `CompleteRequest` atomically XADDs a `reply` **targeted at the requester** |
| Pane identity | `AgentSnapshot.Pane` (= `HERDR_PANE_ID`), `Session` (= `CLAUDE_CODE_SESSION_ID`) in `{p}:agents` |
| Runtime state | herdr: `herdr agent list/get` exposes `agent_status` (`idle`/`working`/`blocked`/`done`/`unknown`), `terminal_id`, and for Claude `agent_session`; `herdr agent prompt` refuses a `blocked` agent; `herdr agent wait` |

Delivery exists; **execution is not guaranteed** (C1): a crash after ACK but before
`accept` is not replayed. The dispatcher does not change that guarantee — it only
makes sure an obligation is never left without someone to deliver it to.

## 3. Architecture

```
peer X ──cmd/request──▶ {p}:cmd ──XREAD BLOCK (signal only)──▶ agentbus dispatch
                                                              │ reconcile every agent A
  obligation(A) = PEL(group A) ∪ unread entries addressed to A, newer than horizon,
                  excluding wake=no
  A armed (receiver lease present)?  → nothing to do; subscribe owns it
  else verify identity(pane, terminal_id, session) → runtime.Inspect(pane)
     idle|done + input empty → runtime.Prompt(pane, wake text)  → record {p}:wake:A
     working | input not empty → defer (timer revisits)
     blocked | unknown | pane gone | identity mismatch | never registered → defer, escalate after T
A woken → `agentbus subscribe --drain A` → handles → request accept/done
done → reply targeted at requester → same path wakes the requester (completion bridge)
A has nothing left → status idle, does not re-arm → 0 tokens
```

The dispatcher is **stateless by design**: every decision is recomputed from Redis
(agent groups, receiver leases, `{p}:agents`, `{p}:wake:*`) plus herdr. `XREAD
BLOCK` on `{p}:cmd` from `$` only shortens latency; a periodic tick (default 5 s)
revisits deferred agents. Restart therefore loses nothing and replays nothing old
(C4, C8, C9). Coalescing is structural: one obligation per agent, whatever the
number of entries (C6).

### 3.1 Obligation

For agent A with group G = A on `{p}:cmd`:

- **PEL**: `XPENDING` count of G > 0. Conservative: a pending non-addressed entry
  is acknowledged by the drain without output, as `WatchCmdDelivery` already does.
- **Unread**: `XRANGE (last-delivered-id +` filtered to `target == A`. Bounded by
  the ~1000-entry stream cap.
- Entries carrying `wake=no` do not count (they are still delivered by the drain).
- Entries older than `--horizon` (default 24h, from the entry id) do not trigger a
  wake. They are **counted, never discarded or acknowledged** (lesson
  `pel-is-not-backlog`: age alone authorizes neither discard nor replay); busmon
  shows the count; the human decides.
- No group yet → no obligation. §5.1 creates the group at registration.

### 3.2 Decision policy (pure function)

`decide(obligation, armed, snapshot, inspect, wake record, now) → action`, where
action ∈ {`none`, `prompt`, `defer`, `escalate`}:

| Situation | Action |
|---|---|
| No obligation, or only horizon-stale entries | none (clear `{p}:wake:A` if present) |
| Receiver lease present | none — but if the lease later lapses with the obligation still there, the next tick handles it (C4, C5) |
| `{p}:wake:A` exists, covers `high_id` ≥ current max, and `now - at` < retry delay (60 s) | none — wait for the drain |
| Snapshot missing (never registered) | defer; escalate after T (C11) |
| Pane gone, or `terminal_id` ≠ snapshot, or herdr `agent_session` present and ≠ snapshot `session` | defer; escalate after T — never guess a pane from role or cwd (C10) |
| `idle` or `done`, input empty | prompt (C13) |
| `idle`/`done`, input **not** empty | defer; escalate after T (C12) |
| `working` | defer (C9) |
| `blocked` / `unknown` / herdr error | defer; escalate after T (C13) |
| `tries` ≥ 3 without the obligation shrinking | escalate; no further automatic wake for this obligation |

T defaults to 10 min (`--escalate-after`). An escalation is sent **once per
obligation** (recorded in the wake hash).

### 3.3 Wake text

The prompt carries **no message body** — peer content is never typed into a
terminal. Exactly:

```
[agentbus] <project>: <n> message(s) waiting for <agent>. Run: agentbus --project <project> subscribe --drain <agent>
```

### 3.4 Escalation

An escalation is a `cmd` of type `escalation` targeted at the master (the agent
holding `{p}:pilot`), body = agent, reason, obligation size, oldest id; plus a
`notify` for the human. It wakes the master through the same dispatcher path. If
the stuck agent **is** the master, or no pilot is held: `notify` only (no
recursion) and busmon shows `⚠`.

## 4. Components

- **`dispatch/` (new package)** — reconciliation loop, `decide` policy, and the
  `Runtime` interface. No herdr import outside `dispatch/herdr.go`.

  ```go
  type Inspection struct {
      State      string // idle|working|blocked|done|unknown
      Terminal   string // herdr terminal_id
      Session    string // herdr agent_session value, "" if not exposed
      Client     string // herdr "agent" field: claude|codex|kimi|...
      InputEmpty bool
      Gone       bool
  }
  type Runtime interface {
      Inspect(ctx context.Context, pane string) (Inspection, error)
      Prompt(ctx context.Context, pane, text string) error
  }
  ```
  `wake` and `deliver` are not separate: the wake prompt *is* the signal; delivery
  stays in Redis. This is the minimum an ACP runtime will also need (v0.9).

- **`dispatch/herdr.go`** — the only `Runtime` in v0.8: `herdr agent get <pane>`
  (state, terminal, session, client), `herdr pane read <pane> --source recent`
  (input line), `herdr agent prompt <pane> <text>`. Input-empty detection is a
  **per-client pattern table** filled from observations on real panes during
  implementation (Claude Code, Codex, Kimi). Unknown client → `InputEmpty=false`
  (never inject on a guess; defer → escalate).

- **`bus/` (protocol primitives, per CLAUDE.md rule)** — `Bus.Obligation(agent,
  horizon)`, dispatcher lease, wake-record read/write, group creation at
  registration, `block` notification, `wake=no` field.

- **`cmd/agentbus`** — new `dispatch` command; `subscribe --drain`; `cmd --no-wake`.

- **`cmd/busmon`** — indicators (§5.4).

## 5. Protocol changes (all additive; `ProtocolVersion` stays 1)

### 5.1 Group created at registration
`agentbus status` creates the agent's group on `{p}:cmd` at `$` if absent
(`XGROUP CREATE … MKSTREAM`, ignore `BUSYGROUP`). From then on every addressed
entry is captured — removing the "first subscribe skips what came before" trap.
Best-effort like the snapshot HSET: a failure never fails the status publish.

### 5.2 Snapshot gains `terminal`
`AgentSnapshot.Terminal` = herdr `terminal_id` of `HERDR_PANE_ID`, resolved by
`agentbus status` via `herdr pane get` when `HERDR_ENV=1` (best-effort, short
timeout). It is what proves a recycled pane id is no longer this agent. A
snapshot without `terminal` (old binary) is **unverified** → defer/escalate,
never prompted. Agents become auto-wakeable after one status publish with the new
binary.

### 5.3 New stream fields / types
- `wake` field on cmd entries: `no` suppresses dispatcher wakes (entry is still
  delivered). `agentbus cmd --no-wake`. `shutdown` uses it — a disconnected peer is
  already stopped; waking it to say "stop" is pure cost.
- `escalation` cmd type (§3.4).
- `request block` additionally XADDs a `type=blocked` entry targeted at the
  requester, inside the existing Lua transition (atomic), so a sleeping requester
  learns its request is blocked (C16).

### 5.4 New keys
- `{p}:dispatch` — string lease (`SET NX PX`, token-fenced renew/release, like the
  receiver lease). One dispatcher per project; a second instance exits 0 with a
  message (C6). Doubles as liveness for busmon/sentinel. Added to `projectKinds`.
- `{p}:wake:{agent}` — hash `{session, terminal, at, high_id, tries, state,
  escalated}`. The two-colon key cannot forge a project (`projectFromKey` rule).

### 5.5 `subscribe --drain <agent>`
Uses `WatchCmdDelivery` semantics (lease, PEL first, ACK after output) with **no
floor** and **no `--since`**: only the group cursor. Emits one JSON object per
delivered entry (JSONL, each with `"v":1`), then exits as soon as the PEL and
unread set are empty — no idle window. Exit 0 if ≥1 entry, 64 if none. The
one-shot `subscribe` contract is unchanged.

### 5.6 busmon
- STATUS bar: `dispatch ●` (lease held) / `dispatch ○` (absent).
- Agent chip: `✉N` waiting obligation, `⏰` deferred wake, `⚠` escalated,
  `⌛N` horizon-stale backlog.

## 6. Error handling

| Failure | Behaviour |
|---|---|
| Dispatcher crash at any point | Restart recomputes from Redis; nothing lost (C8) |
| Crash between prompt and wake-record write | Possible second wake; harmless — drain reads the group cursor and an empty drain exits doing nothing (C7) |
| Two dispatchers | `{p}:dispatch` lease; loser exits (C6) |
| Burst of N entries | One obligation → one wake (C6) |
| Lease lapses with obligation pending | Next tick wakes (C4) |
| Body trimmed before drain | Drain emits `missing`, as `subscribe` does (C14) |
| Redis or herdr error | Structured log line (correlation id = cmd entry id), capped backoff to 30 s; never exit on a transient error |
| herdr socket absent | Every agent deferred → escalation path; `dispatch` keeps running |
| Escalation target is the master itself / no pilot | `notify` only, `⚠` in busmon |

Logs: one JSON line per decision on stderr (`agent`, `action`, `reason`,
`high_id`, `latency_ms` from oldest obligation entry to prompt).

## 7. Skill and role changes

| Skill | Change |
|---|---|
| `agent-bus` | Do not re-arm to *wait for an answer* — the dispatcher wakes you when the reply lands. On a wake prompt: `subscribe --drain`, handle, `status idle`, stop. Armed `subscribe` remains valid for tight in-flight exchanges. |
| `agent-bus-master` | Drop "you stay armed": master sleeps too; replies, escalations and blocked requests wake it. Manual pane wake (resolve → read → inject) becomes the **fallback** when `dispatch ○`. |
| `agent-bus-sentinel` | Add: check `{p}:dispatch`; if absent, `notify` the human. |
| `scripts/bootstrap` | Open a `dispatch` pane running `agentbus --project <p> dispatch`. Opt-out flag. No systemd. |
| `CLAUDE.md` | Replace the two "don't reintroduce a pane-relay / watcher daemon" lines with the rule in §11. |

## 8. Compatibility

- Without a dispatcher, behaviour is exactly v0.7: master wakes manually.
- Stream formats, one-shot `subscribe` JSON, board schema unchanged; new
  fields/types are additive (consumers must ignore unknown fields).
- Old-binary agents (no `terminal` in snapshot) are never auto-prompted.

## 9. Testing

- **Unit (no Redis)**: `decide` table-driven, one case per row of §3.2 and §6.
  Herdr runtime: parsing of `herdr agent get` JSON; input-empty patterns from
  captured pane fixtures per client.
- **Integration (isolated Redis only, `AGENTBUS_TEST_REDIS_URL`)** with a fake
  `Runtime`: crash-after-prompt + restart (≤1 duplicate wake, no loss); two
  dispatchers (one active); burst (one wake); lease lapse (wake); `request done`
  wakes requester; `request block` wakes requester; horizon-stale backlog (no
  wake, counted); `wake=no` (no wake, still drained); group created at status.
- **`--drain`**: PEL first, then unread; ACK after output; empty → immediate
  exit 64; non-addressed pending acked silently.
- **Live acceptance (manual, recorded in `docs/releases/v0.8.0.md`)**: in herdr
  with an idle Claude and an idle Codex — publish→prompt latency; zero transcript
  growth over 30 min idle; prompt deferred while the human types; full loop
  master → coder request → done → master woken.

## 10. Validations required before implementing the driver (C17)

Verified on this machine (2026-10-10, herdr in session `busmon`): `agent_status`,
`terminal_id`, Claude `agent_session`, `agent prompt` refusal on `blocked`.
Still to verify during implementation, before relying on them:
1. Does herdr detect Kimi panes (`agent` field), and Gemini?
2. Does a recycled pane id get a new `terminal_id`?
3. Input-line patterns for Claude Code 2.1.x, Codex 0.162, Kimi 2.1 with empty
   vs non-empty input, read via `herdr pane read`.
4. Does `herdr agent prompt` submit correctly to each client (Enter handling)?

A client failing 1, 3 or 4 is unsupported for auto-wake (defer → escalate) and
documented as such.

## 11. Why this is not `cmd/busbridge` revived

CLAUDE.md says "Don't reintroduce a pane-relay daemon" and "Don't reintroduce a
`Restart=always` watcher daemon". The 2026-05-25 streams-unification spec dropped
`busbridge` in favour of the one-shot `subscribe`, whose exit wakes a Claude Code
background task. No other rationale is recorded. This design deliberately revisits
that decision, because its premise no longer holds:

- **Wake-on-exit is Claude-specific.** v0.7 made roles client-neutral and its own
  `agent-bus` skill now says background-task exit wakes a session "only when that
  client supports completion notifications". Codex and Kimi panes have no such path;
  today only the master's manual pane injection reaches them.
- **Wake-on-exit is not free at idle.** Staying reachable means re-arming every idle
  window, which is exactly the cost PR #38 removed by disconnecting idle peers.

What differs from `busbridge`:

| `busbridge` (removed) | `dispatch` (v0.8) |
|---|---|
| Relayed **message content** into panes | Injects only a fixed wake line; content stays in Redis (§3.3) |
| No busy / blocked / human-typing check | herdr state + input-empty check; defers otherwise |
| No pane identity proof | `terminal_id` (+ session) must match the snapshot |
| Replaced the delivery path | Delivery remains `WatchCmdDelivery` (lease, PEL, ACK) via `--drain` |
| Ran for every agent | Touches only **unarmed** agents with a real obligation |

It is not a restart loop: the process is a single long-lived reader in a herdr
pane, with no `Restart=always`, and it never tries to wake a session by exiting.
The CLAUDE.md "things that bite" entries are rewritten in the same change to
state the new rule: *the only process that types into panes is `agentbus
dispatch`, and it types a wake line, never a message.*

## 12. Out of scope (v0.9+)

ACP runtime (Gemini/Kimi/OpenCode native; Claude/Codex via adapters) — note it
means dispatcher-owned headless sessions, not the visible herdr TUI (C18);
`codex queue`; task-level retry/backoff and DLQ; per-task token metrics;
structured memory and skill self-improvement (Brigade P2).
