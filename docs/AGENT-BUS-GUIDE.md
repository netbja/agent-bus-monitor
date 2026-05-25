# Agent Bus — guide for Claude & Hermes agents

How to talk on the Streams bus. One transport (Redis Streams), one rule:
every channel is `{project}:{kind}`.

## Setup (every agent)

```bash
export AGENT_BUS_PROJECT=trading   # REQUIRED — your project namespace (no default)
export AGENT_BUS_AGENT=dev         # your role (lowercase ^[a-z][a-z0-9_-]{0,31}$); hermes = orchestrator
```

`agentbus` and `busmon` both refuse to start without a project. `--project P`
overrides the env var; `--host`/`REDIS_*` resolve the broker (default `localhost:6380`).

## Publishing (what you should emit)

```bash
agentbus status <agent> <working|idle|blocked|done> [msg]   # your state — this IS your heartbeat
agentbus report <agent> [--auto] <msg>                      # curated report (note, or --auto safety-net)
agentbus notify <msg>                                       # project-wide announcement
```

Agents are one-shot CLI calls, so every `status`/`report` publish doubles as liveness — busmon
ages you to `idle` (2m) / `offline` (10m) from your last entry.

## Who's driving: piloted vs autonomous

**Hermes pilots the workers while it has budget; when budget runs out, workers are autonomous.**
This is a TTL lease, not a message:

- **Hermes**, on an interval *while it has budget*:
  ```bash
  agentbus pilot claim           # = renew; SET {project}:pilot=hermes EX 90s (default; --ttl to change)
  ```
  Out of budget? **Just stop calling it.** No "I'm done" message. (Or `agentbus pilot release` to hand off now.)

- **Workers**, at each wake-up, check the mode:
  ```bash
  agentbus pilot status          # "piloted by hermes"  OR  "autonomous"
  ```
  - **piloted** → wait for a directive (don't act on your own); `agentbus watch <self>` blocks for it.
  - **autonomous** → proceed on your own plan; just keep emitting `status`/`report`.

The lease expiring (Hermes silent / out of budget / crashed) ⇒ autonomous, automatically.

## Directing & challenging other agents (`{project}:cmd`)

Any agent can address any agent. `cmd.type` ∈ `directive | challenge | reply | verdict`.

```bash
agentbus cmd <target> <command>                       # directive (Hermes piloting) — gated by pilot mode
agentbus challenge <target> [--ref R] <why>           # 4-eyes: opens a gate on <target> (prints the ref)
agentbus reply --ref R <target> <answer>              # respond to a challenge
agentbus verdict --ref R <target> <approve|reject>    # closes the gate
```

### Strict 4-eyes gate

A `challenge` **blocks** the challenged agent until a `verdict`, in **both** piloted and autonomous mode
(it's a safety barrier independent of who's driving). Before you mark work `done` / proceed, check:

```bash
agentbus gate <self>            # lists open challenges; exit code ≠ 0 means you are GATED — do not proceed
```

`verdict` fails loudly if the ref isn't open (catches a stale/typo verdict).

## Inbound (being driven / challenged)

`agentbus watch <agent>` is the one-shot inbound watcher: it blocks on `{project}:cmd` for an entry
addressed to `<agent>` (server-side consumer-group cursor = no missed commands across restarts),
prints `[type from->target ref=R] message` and exits — or prints `__HEARTBEAT__` after ~240s idle.
Arm it as a Claude background task and re-arm after each fire. In `adv-trading-ai`, `tools/bus_watch.sh`
is the thin wrapper that calls it.

## Watching everything

`busmon --project <p>` — TUI: AGENTS (presence + pilot mode header + 🔒 gate badges), ACTIVITY
(status/report/notify/cmd, backfilled from history), INPUT (Enter → `notify`).
Debug from the CLI: `agentbus listen [status report notify cmd]`.
