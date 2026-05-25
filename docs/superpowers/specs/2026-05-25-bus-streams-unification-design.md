# Bus Streams unification — design

- **Date**: 2026-05-25
- **Status**: Draft (brainstorm complete), pending spec review
- **Repos touched**: `agent-bus-monitor` (this repo), `adv-trading-ai` (thin wrapper only)
- **Supersedes**: realizes the "Phase 3 — move to Streams" milestone deferred in
  `2026-05-23-curated-bus-notifications-design.md` (decision 6).

## Problem

Two coordination mechanisms coexist on the same Redis broker (`:6380`), and they don't talk to
each other:

1. **This repo's pub/sub bus** — `status:{agent}`, `hermes:cmd:{agent}`, `hermes:notify`,
   `hermes:report:{agent}`. Ephemeral (no history), wire format `state|message` split on the first
   `|`. A piloting command published while the target agent isn't currently blocked on the bus is
   **lost**.
2. **A separate `agent:queue`** — a durable Redis list with JSON payloads (legacy / `adv-trading-ai`
   convention) where "VDR-Hermes status" lands.

Same broker, two transports, two wire formats, two naming conventions. The bus *works* but is
hard to reason about ("on se parle à moitié dans le vide"). Three concrete needs drive the redesign:

- **Unify** onto one transport, **but namespaced per project** so multiple projects on the same
  broker never collide.
- **Meaningful agent naming** — `claude1`/`claude2` are positional and tell Hermes nothing about
  who does what.
- **A control model**: Hermes pilots the worker agents *unless* its budget is exhausted, in which
  case the workers continue **autonomously**. Plus peer-to-peer **4-eyes challenges** that gate an
  agent regardless of who is driving.

## Goals

- One transport: **Redis Streams** replaces both the pub/sub channels and `agent:queue`.
- One naming rule: **`{project}:{kind}`** (project prefix solves multi-project isolation).
- Role-based agent identity; drop the hardcoded `claude1/claude2` allowlist.
- Durable, ordered, replayable delivery — a piloting command or a challenge is **never lost**
  across an agent's one-shot restarts.
- **Pilot lease**: Hermes drives while it renews a TTL lease; lease lapses ⇒ workers go autonomous.
- **Peer challenge with a strict 4-eyes gate**: any agent can challenge any agent; an open
  challenge blocks the challenged agent from proceeding until a verdict closes it — in *both* pilot
  and autonomous modes.
- The inbound watcher (`bus_watch.sh`'s logic) is **brought into this repo** as `agentbus watch`,
  reusing `bus.go` so transport/format changes propagate to the consumer automatically.
- busmon gains **history** (it only showed live) and renders pilot mode + open gates.

## Non-goals

- No change to the off-bus Signal path (`hermes-notify` → VDR `:8644` gateway → Signal).
- No backward compatibility with the old pub/sub channels — this is a **clean cutover**, not a
  dual-run. Keeping both alive would re-create the "two systems" problem this spec exists to kill.
- No consumer groups where a plain tail suffices (status/report/notify are read-only tails).
- The agent-side *decision logic* (obey a directive, wait on a gate) lives in each agent's prompt,
  not in `bus.go`. The bus delivers and labels; it does not enforce agent behavior.

## Decisions (locked in brainstorm)

1. **Transport** = Redis Streams, single transport. (User: "Streams comme transport unique → OK".)
2. **Naming** = `{project}:{kind}` channel rule; agent identity = **role** (lowercase, numbered only
   for genuinely interchangeable instances). `hermes` is the orchestrator role; the `_laptop`/`_vdr`
   suffix is dropped (the project prefix replaces it) unless two Hermes truly run in parallel.
3. **`cmd` is peer-to-peer**, not Hermes-only — any agent may address any agent.
4. **`cmd` is a single shared stream per project** (`{project}:cmd`) with a **per-agent consumer
   group** for delivery — *not* one stream per agent. Rationale below (§ Stream topology).
5. **Watcher** = a Go subcommand `agentbus watch <agent>` reusing `bus.go`. Not the removed
   `cmd/busbridge` pane-relay daemon — a one-shot blocking watcher only.
6. **4-eyes gate is strict**: an open challenge blocks the challenged agent until a verdict; the
   gate state is durable and queryable.
7. **`ValidAgents` hardcoded allowlist is removed**; `AGENT_BUS_PROJECT` becomes required; agent
   names are validated by regex. `ValidStates` is kept.

## Design

### 1. Naming rule

Every stream key is `{project}:{kind}`. `project` is a short slug (`busmon`, `trading`, …);
`kind ∈ {status, report, notify, cmd}`. The agent and (for `cmd`) the target live in the entry
fields, not the key. This is the entire convention.

Agent identity is a role: `dev`, `review`, `strat`, … numbered (`dev1`, `dev2`) only when there are
interchangeable instances. `hermes` is the orchestrator role.

### 2. Stream topology — 4 fixed streams per project

| Stream | Pattern | Consumed by | Fields (native XADD k/v, no nested JSON) |
|---|---|---|---|
| `{project}:status` | fan-in, tail | busmon | `agent`, `state`, `message` |
| `{project}:report` | fan-in, tail | hermes, busmon | `agent`, `kind` (`note`/`auto`), `message` |
| `{project}:notify` | broadcast, tail | busmon | `from`, `message` |
| `{project}:cmd` | addressed, consumer group | every agent + busmon | `from`, `target`, `type`, `ref`, `command` |

**Why `cmd` is one shared stream, not one per agent.** Redis Streams have **no pattern read**
(no `PSUBSCRIBE status:*` equivalent), so a tool can only `XREAD` a fixed set of keys. Per-agent cmd
streams would mean busmon cannot see piloting/challenge traffic without first discovering and
enumerating every agent. A single `{project}:cmd` stream solves three things at once:

- **busmon visibility** — it tails one key and sees all directives/challenges/replies/verdicts.
- **Guaranteed addressed delivery** — each agent creates its **own consumer group** (group name =
  the agent's name) on the shared stream. In Redis Streams, every *group* keeps an independent
  cursor over the full stream, so each agent's group sees every entry; the agent acts only on
  entries where `target == self` and `XACK`s the rest. The cursor lives server-side, so a one-shot
  agent that restarts never misses a command (this is the core durability win over pub/sub).
- **Key count stays flat** — 4 streams per project regardless of agent count.

`cmd.type ∈ {directive, challenge, reply, verdict}`:
- `directive` — `from = hermes`, a piloting order. Gated by pilot mode (§4).
- `challenge` — `from = peer`, a 4-eyes contestation. Always delivered, gates the target (§5).
- `reply` — a response to a challenge, correlated by `ref`.
- `verdict` — closes a challenge (`approve`/`reject`), correlated by `ref`, resolves the gate.

`ref` correlates a challenge with its replies/verdict (empty for fire-and-forget directives).

### 3. Delivery & retention

- **status / report / notify** → plain `XREAD` from the last-seen ID. busmon and hermes tail these;
  no acknowledgement needed. busmon gains replayable history for free.
- **cmd** → agents consume via `XREADGROUP` + `XACK` on a per-agent consumer group
  (`XGROUP CREATE … MKSTREAM`, idempotent on first watch; group = agent name, consumer = host/pid).
  busmon reads the *same* `{project}:cmd` stream as a plain `XREAD` **observer** — a plain read does
  not touch any group cursor, so busmon never competes with the agents for delivery.
- **Retention** → every `XADD` uses `MAXLEN ~ 1000` (approximate trim) so no stream grows unbounded.
  Acknowledged cmd entries are trimmed by `MAXLEN` like any other.

### 4. Pilot lease — `{project}:pilot`

A single durable key encodes "who is driving":

- Key `{project}:pilot`, value = driver id (`hermes`; optional `hermes|budget=<n>` for busmon
  display), `EX 90`.
- Hermes does `SET {project}:pilot hermes EX 90` **every ~30s while it has budget**.
- A worker, on each wake-up, reads the key:
  - **present** ⇒ **piloted** — the worker blocks on `{project}:cmd` (its group) and acts only on
    `directive`s addressed to it.
  - **absent** ⇒ **autonomous** — the worker proceeds on its own plan and only `report`s.
- **Budget exhaustion = stop renewing.** No "out of budget" message to emit (which could be lost),
  no separate "Hermes crashed" case: a silent Hermes ⇒ lease expires ⇒ autonomy. The lease is the
  single source of truth for the mode.

### 5. Peer challenge & strict 4-eyes gate — `{project}:gate:{agent}`

Challenges flow on the shared `{project}:cmd` stream; the *gate* is a small durable structure so
"blocked until verdict" is enforceable and queryable (mirrors the lease pattern):

- `{project}:gate:{agent}` is a Redis **hash**: field = `ref`, value = `<challenger>|<summary>`.
  No TTL — a gate must be explicitly resolved, never silently expire.

Lifecycle (4-eyes review of `dev` by `review`):

1. `review` challenges `dev`:
   `XADD {project}:cmd … from=review target=dev type=challenge ref=C1 command="justify X"`
   **and** `HSET {project}:gate:dev C1 "review|justify X"`.
2. `dev`'s watcher surfaces the challenge. `dev` cannot mark itself `done`/proceed while
   `{project}:gate:dev` is non-empty (strict gate). `dev` replies:
   `XADD {project}:cmd … from=dev target=review type=reply ref=C1 command="because …"`.
3. `review` issues a verdict:
   `XADD {project}:cmd … from=review target=dev type=verdict ref=C1 command="approve"`
   **and** `HDEL {project}:gate:dev C1` (resolves the gate).

**Mode independence:** challenges and the gate operate identically in piloted and autonomous mode.
The pilot lease gates only Hermes `directive`s; a 4-eyes control is a safety barrier orthogonal to
who is driving.

The *enforcement* ("don't continue while gated") lives in the agent's prompt; the bus provides the
queryable gate state (`agentbus gate <agent>`) and the labeled stream entries.

### 6. `bus.go` API surface (the single source of truth)

Keys: `StatusStream(p)`, `ReportStream(p)`, `NotifyStream(p)`, `CmdStream(p)`, `PilotKey(p)`,
`GateKey(p, agent)`.

An `Event` struct replaces the `(agent, kind, state, message)` tuple, populated from the stream
entry's field map; `Parse` reads `map[string]string` → `Event` (no more first-`|` splitting).

- Publish (all `XADD … MAXLEN ~ 1000`): `Status`, `Report`, `Notify`,
  `Cmd(ctx, r, p, from, target, type, ref, command)`.
- Consume: `Tail(ctx, r, fromID, fn, streams…)` (`XREAD`);
  `WatchCmd(ctx, r, p, agent, fn)` (`XREADGROUP`+`XACK`, blocking, returns first matching entry).
- Pilot: `ClaimPilot`, `RenewPilot`, `ReleasePilot`, `PilotDriver(ctx, r, p) → driver|""`.
- Gate: `OpenChallenge(ctx, r, p, agent, ref, meta)`, `ResolveChallenge(ctx, r, p, agent, ref)`,
  `OpenChallenges(ctx, r, p, agent) → map[ref]meta`.

All Streams complexity stays inside `bus.go`; the binaries keep almost no logic of their own
(unchanged principle from `CLAUDE.md`).

### 7. `agentbus` CLI surface

Global: `--project` / `AGENT_BUS_PROJECT` (**required**, no default — forces discipline),
`--host`, self identity via `AGENT_BUS_AGENT`.

- `agentbus status <agent> <state> [msg…]`
- `agentbus report <agent> [--auto] <msg…>`
- `agentbus notify <msg…>`
- `agentbus cmd <target> <command…>` — `from=$self`, `type=directive`
- `agentbus challenge <target> [--ref R] <msg…>` — `type=challenge`, opens the target's gate
- `agentbus reply --ref R <target> <msg…>` — `type=reply`
- `agentbus verdict --ref R <target> approve|reject [msg…]` — `type=verdict`, resolves the gate
- `agentbus watch <agent>` — one-shot `XREADGROUP BLOCK` on `{project}:cmd` (group = agent) +
  heartbeat on timeout; prints the first entry targeting the agent (labeled with `type` and the
  current pilot mode) and exits, so it can be armed as a Claude background task
- `agentbus pilot claim|renew|release|status`
- `agentbus gate <agent>` — lists open challenges; **exit code ≠ 0 if the agent is gated**
- `agentbus listen <stream…>` — debug tail (`XREAD`)

The user-facing `status`/`cmd`/`report` invocations are unchanged in shape; `bus.go` does the XADD.

### 8. `busmon` changes

- `PSUBSCRIBE` → `XREAD` over the project's `status`, `report`, `notify`, `cmd` streams (with a
  starting `$`/last-id; backfill a bounded history window on startup).
- A **project selector** (env `AGENT_BUS_PROJECT` or a UI switch) — busmon shows one project at a
  time; nothing bleeds across projects.
- Header shows pilot mode from `{project}:pilot`: `[piloté par hermes]` / `[autonome]`.
- Per-agent **gate badge** (e.g. 🔒 with open-ref count) read from `{project}:gate:{agent}`.
- ACTIVITY renders challenge threads grouped by `ref`.

### 9. Outside this repo (`adv-trading-ai`)

`bus_watch.sh` becomes a **thin wrapper** that calls `agentbus watch <agent> --project <p>`. The
Claude-background-task arming mechanism stays in `adv-trading-ai`; all bus logic moves here. The
old in-repo `cmd/busbridge` pane-relay daemon is **not** revived.

## Validation & identity

- `AGENT_BUS_PROJECT` required; no default (a missing project is a hard error, not a silent global
  namespace).
- Agent-name regex: `^[a-z][a-z0-9_-]{0,31}$`. Replaces the `ValidAgents` map.
- `ValidStates` kept (`working`, `idle`, `blocked`, `done`).

## Migration — suggested phasing (for the implementation plan)

Clean cutover, no dual-run with pub/sub.

1. **`bus.go` Streams foundation** — keys, `Event`, publish helpers (XADD+MAXLEN), `Tail`,
   `WatchCmd`, pilot helpers, gate helpers, regex validation. Unit tests next to the code
   (the repo currently has `bus/bus_test.go` — extend it).
2. **`agentbus`** — `--project` plumbing; publish via Streams; new `cmd`/`challenge`/`reply`/
   `verdict`/`pilot`/`gate`/`watch` subcommands; `listen` → `XREAD`.
3. **`busmon`** — `XREAD` tailing, project selector, pilot mode + gate rendering, startup backfill.
4. **`agentbus watch` finalize + `adv-trading-ai` wrapper swap** — document the `bus_watch.sh`
   refactor and the heartbeat/arming contract.

## Risks & open questions

- **Consumer-group startup**: first `watch` must `XGROUP CREATE … MKSTREAM` idempotently and decide
  a starting position (`$` = only new, `0` = replay backlog). Default `$` to avoid replaying stale
  commands on a fresh agent; revisit if missed-while-down replay is wanted.
- **PEL hygiene**: an agent's group reads commands targeting *other* agents too; it must `XACK`
  them (acting only on its own) so the pending-entries list doesn't grow. Low volume (one-shot
  agents) makes this negligible.
- **Heartbeat semantics**: preserve today's `__HEARTBEAT__`-on-timeout so the watcher re-invokes the
  session on a quiet bus.
- **Gate deadlock**: a strict gate that is never resolved blocks an agent forever. Mitigation:
  `agentbus gate` makes open gates visible in busmon; consider an explicit `agentbus verdict` escape
  for a stuck `ref`. (No TTL by design — auto-expiry would defeat a safety gate.)
- **Budget signal source**: this spec assumes Hermes decides when to stop renewing the lease; how
  Hermes *measures* its budget is out of scope here.

## Out of scope

- Signal delivery path and Hermes's LLM judgment of `auto` reports (covered by the curated-
  notifications design).
- How Hermes measures/forecasts its own budget.
- Cross-project orchestration (a single Hermes driving multiple projects at once) — each project is
  an isolated namespace; multi-project Hermes is a later concern.
