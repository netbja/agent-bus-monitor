# Agentbus trigger reliability — design

- **Date:** 2026-06-03
- **Status:** Approved (brainstorm) — ready for implementation planning
- **Scope:** `bus/stream.go`, `cmd/agentbus`, `cmd/busmon`, `docs/AGENT-BUS-GUIDE.md`

## Problem

Agents struggle to re-arm the `subscribe` trigger and report themselves as
"blocked" while waiting for it. The wake-on-exit model
(`agentbus subscribe <agent>` blocks on the project's `:cmd` consumer group,
prints the first addressed entry, and exits — that exit re-invokes the session,
which then re-arms) has three failure modes observed in practice:

1. **False "blocked".** An agent waiting on the trigger describes itself as
   "blocked" — but `blocked` is a reserved `ValidStates` value meaning *gated by
   a 4-eyes challenge*. The waiting agent should be `idle`. The collision
   pollutes busmon and misleads humans.
2. **Silent re-arm death.** Re-arming after each fire is purely behavioral and
   unenforced. Agents drift, stop re-arming, and the listening loop dies with no
   visible signal.
3. **No observability.** There is no way (for a human in busmon, or for a
   sender) to see whether an agent is *actually armed and listening* versus
   dead, nor whether commands are queued for an agent that isn't consuming.

The operator confirmed the exact invocation pattern is **unknown** — only the
symptoms are visible — so the design must be robust to either misuse and must
make the listening state observable.

## Goals

In priority (ranked) order — observability first, because a reliability fix you
can't see working can't be trusted:

1. **Observability** — a human (busmon) and senders can see which agents are
   armed/listening, and which have a backlog of unconsumed commands.
2. **Correct state** — a waiting agent reads as `idle` (or an explicit
   "listening" badge), never `blocked`.
3. **Reliability** — once an agent starts listening, re-arming is trivial,
   uniform across runtimes, and the failure to re-arm is *visible* rather than
   silent.

## Non-goals / constraints

- **No MCP server in this project.** (Operator decision — rules out the
  `claude/channel` push approach.)
- **Runtime-agnostic.** Subscribing agents include Claude Code, Codex, and
  hermes/shell callers. The fix lives in the `agentbus` binary so every runtime
  benefits; a Claude-Code-only hook is at most an optional later layer
  (Strategy 3, deferred — see below).
- **No daemon, no wrapper script** in the agent-wake path (per CLAUDE.md): a
  restart loop re-runs the watcher and never wakes a terminal session, which
  defeats wake-on-exit.
- **Protocol logic stays in `bus/`**; the two binaries stay thin (per CLAUDE.md).

## Chosen strategy

**Strategy 2** = an in-binary foundation (presence + observability + uniform
exit contract + vocabulary fix) **plus** an opt-in `--loop` continuous mode for
headless callers. Strategy 3 (an optional Claude Code `Stop` hook for hands-free
re-arm) is **deferred** to later docs; it only benefits Claude Code and is not
required for any of the three goals.

## Architecture & boundaries

Three layers, mirroring the existing split (transport-neutral logic in `bus/`,
thin binaries):

### `bus/stream.go` — new presence + introspection primitives

- `ArmedKey(project, agent) string` → `{project}:armed:{agent}` (Redis string,
  value = consumer/host, with a TTL).
- `Bus.Arm(ctx, agent, ttl)` / `Bus.Disarm(ctx, agent)` — set / clear the armed
  lease. Shape mirrors the existing `Pilot` / `ReleasePilot`.
- `Bus.ArmedAgents(ctx)` — `SCAN {project}:armed:*` → the set of currently
  listening agents (consumed by busmon).
- `Bus.CmdLag(ctx)` — one `XINFO GROUPS {project}:cmd` call → `map[group]lag`,
  i.e. how many `:cmd` entries each agent's consumer group has not yet read.
  This is the backlog signal. (Group name = agent name, per `WatchCmd`.)

### `cmd/agentbus` — `subscribe` orchestrates the primitives

Holds no Redis logic itself:

- arms the lease around the existing `WatchCmd` block; disarms on exit;
- prints the uniform exit sentinel and sets the matching exit code;
- gains `--loop` (calls `WatchCmd` with a handler that never returns "done").

### `cmd/busmon` — pure rendering

Reuses the existing 1s ticker (which already polls `PilotDriver` + per-agent
`OpenChallenges`). Adds two reads per tick — `ArmedAgents` + `CmdLag` — and
renders a `👂 armed` badge and a `⌛N` backlog badge. No new goroutine, no new
data path.

### Why a separate `armed` key (not a new status state, not reusing `idle`)

A TTL'd key is **self-healing**: if a subscriber crashes, the lease expires and
the badge clears with zero cleanup logic. A `status` entry cannot expire itself,
and adding a `listening` value to `ValidStates` is a protocol change that
ripples through every consumer. The armed key answers a different question than
status ("blocked-waiting *right now*" vs. "alive / what state"), so the two
coexist cleanly — exactly as the pilot lease does today.

## Presence & observability

### Armed-lease lifecycle

- **On `subscribe` start** → `Arm(agent, ttl)`, `ttl` = the idle window (default
  240s, or the `[idle_secs]` arg). The lease covers the whole blocking window.
- **On any exit** (cmd delivered, heartbeat, error) → `Disarm(agent)`. The
  sub-second gap before the next arm reads as "not armed", which is accurate.
- **Crash safety net:** if the process is killed before `Disarm` runs, the lease
  expires after ≤ttl. No orphaned badges.
- **`--loop` mode:** a lightweight refresher re-arms every ttl/2 so the lease
  never lapses while genuinely listening (see below).

### Waiting state: badge-only (decision)

`subscribe` touches **only** the armed key — it does **not** publish a
`status idle` entry. busmon renders `👂` next to the agent's last real state.
Rationale: zero ACTIVITY-feed noise, and the badge is the unambiguous source of
truth for "listening". The agent guide is updated to instruct: **waiting =
`idle`, never `blocked`.**

### busmon rendering (all from the two new ticker reads)

- `👂` badge on any agent whose armed key is live → "listening right now".
- `⌛N` badge when `CmdLag[agent] > 0` → "N commands queued, not yet consumed".
- **The "silently died" tell:** `⌛N` present **and** `👂` absent = commands
  waiting for an agent that stopped listening. busmon highlights this (amber).
  This is the "trouble re-arming" failure made visible — turning "not sure
  what's happening" into "I can see it."

## Exit contract & re-arm (reliability)

Today, delivery prints the cmd line and idle prints `__HEARTBEAT__` — two shapes
the caller must branch on differently. New rule: **every** exit prints the human
line (if any) then **one final machine line**, and sets a matching exit code:

| Exit reason            | Final stdout line                                      | Exit code |
|------------------------|--------------------------------------------------------|-----------|
| cmd delivered          | `__AGENTBUS__ event=cmd rearm=1 ref=<r> from=<f>`      | `0`       |
| idle window elapsed    | `__AGENTBUS__ event=heartbeat rearm=1`                 | `64`      |
| transient error        | `__AGENTBUS__ event=error rearm=1 msg=<...>`           | `75`      |
| fatal (misconfig/auth) | `__AGENTBUS__ event=fatal rearm=0 msg=<...>`           | `1`       |

Re-arm logic is then identical for every runtime: **"after `agentbus subscribe`
exits, re-arm iff `rearm=1`."** `rearm=0` is the only "stop", and it only occurs
on misconfiguration (bad project/agent name, auth/connection failure, bad args)
where re-arming would just loop-fail.

**Back-compat:** `__HEARTBEAT__` is still printed for one release (existing agent
loops depend on it), marked deprecated in the guide alongside the new contract.

## `--loop` mode (headless callers only)

Calls `WatchCmd` with a handler that prints each addressed cmd and returns
`false`, so it never exits; a refresher goroutine keeps the armed lease alive
(re-arm every ttl/2). It emits **no** re-arm sentinels (none are needed) and
exits only on signal / ctx-cancel / fatal error.

The guide and `--help` state loudly: **headless consumers (hermes / shell /
logger) only — never the agent-wake path**, because a long-lived loop cannot
wake a terminal session. This is the single spot that brushes the CLAUDE.md "no
loop" rule, so it is gated behind an explicit flag and documented as such.

## Error handling

- **Startup failures** (bad project/agent, connection refused) → `fatal
  rearm=0`; no arm is performed.
- **Mid-block broker errors** → best-effort `Disarm` → `error rearm=1`.
- **`Arm` / `Disarm` failures** are non-fatal (logged to stderr): presence is
  observability, never a hard dependency of command delivery. Delivery must
  succeed even if the armed key cannot be written.

## Testing

- `bus/stream_test.go`: `Arm` / `Disarm` / TTL-expiry, `ArmedAgents` SCAN, and
  `CmdLag` against a real entry backlog (the suite already runs against Redis).
- `cmd/agentbus`: assert the exit-code / sentinel matrix per exit reason, and
  `--loop` continuity (multiple cmds delivered without exit).
- `cmd/busmon` (`render_test.go` style): assert the `👂`, `⌛N`, and
  amber-warning states from fixture inputs.

## Build order (ranked)

1. **Observability + correct state** — `Arm`/`Disarm`/`ArmedAgents`/`CmdLag` in
   `bus/`, `subscribe` arming the lease, busmon `👂` + `⌛N` + amber warning, and
   the guide vocabulary fix (waiting = idle). Cheap, and it lets the operator
   *see* the real failure before trusting the reliability work.
2. **Uniform exit contract** — the sentinel + exit-code matrix in `subscribe`,
   `__HEARTBEAT__` back-compat, guide update with the one-line re-arm rule.
3. **`--loop`** — continuous mode for headless callers, with the lease
   refresher and the loud "headless-only" documentation.

## Deferred

- **Strategy 3** — an optional Claude Code `Stop` hook for fully hands-free
  re-arm (Claude Code agents only). Lives in agent config, not this repo's
  deploy; benefits only one runtime. Revisit after Strategy 2 is in use.
