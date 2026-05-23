# agent-bus-monitor (`busmon`)

Live terminal dashboard for the **Agent Bus** — a Redis pub/sub channel set used to
coordinate multiple AI agents (claude1, claude2, hermes_laptop, hermes_vdr) across
projects. Standalone Go tool: one static binary, no runtime dependencies.

```
┌─ AGENTS ───────────────────────────────────────────────────────────────────┐
│ claude1: working (plan 10)   claude2: idle 3m   hermes_vdr: offline          │
├─ ACTIVITY ───────────────────────────────────────────────────────────────────┤
│ 23:15:12 [claude1] working | plan 10 shipped                                 │
│ 23:15:45 [claude2] idle | waiting                                            │
│ 23:16:02 [notify] Soak 24h started                                           │
├─ INPUT ──────────────────────────────────────────────────────────────────────┤
│ > _                                                                          │
└──────────────────────────────────────────────────────────────────────────────┘
```

## What it does

- **AGENTS** — one chip per agent, driven only by `status:{agent}` messages.
  Color reflects the published state (working/idle/blocked/done). When an agent
  goes silent past `idleAfter` it shows `idle Nm`; past `staleAfter`, `offline`.
- **ACTIVITY** — a scrolling, color-coded feed of every status change,
  notification, and command seen on the bus.
- **INPUT** — type a message, press Enter to publish it to `hermes:notify`.
  Esc or Ctrl-C quits.

## Liveness model (why no dedicated heartbeat)

The agents are one-shot CLI invocations, not daemons — nothing is alive between
invocations to emit a periodic heartbeat. So liveness is derived **passively**
from the timestamp of each agent's last `status:` message: every status publish
*is* the heartbeat. A dedicated `status:<agent>:heartbeat` channel would buy
nothing the existing status traffic doesn't, until agents become long-running.

## Bus conventions (decoupled — this tool imports nothing from any project)

| Channel              | Payload                         | Pane it feeds        |
|----------------------|---------------------------------|----------------------|
| `status:{agent}`     | `state\|message` or `state`     | AGENTS + ACTIVITY    |
| `hermes:notify`      | free text                       | ACTIVITY             |
| `hermes:cmd:{agent}` | command text                    | ACTIVITY             |

Subscribed via `PSUBSCRIBE status:* hermes:*`. States: `working`, `idle`,
`blocked`, `done`.

## Connection

Resolved in the same order as `agent_bus.py`:

1. `REDIS_URL` (e.g. `redis://:pass@host:6380/0`) — takes precedence when set
2. otherwise `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD`
   (defaults `localhost` / `6380` / `AgentBus2025!`)

`--host <host>` overrides `REDIS_HOST`.

## Build & run

```bash
go build -o busmon .
./busmon                       # local bus (agent-bus-redis on localhost:6380)

# install on PATH (binary name = module name = busmon)
go install .                   # -> $GOBIN/busmon
```

## Watching a remote bus over SSH

The bus listens on the host's `localhost:6380` but should not be exposed raw over
the network — the Redis password travels in plaintext. To watch a bus on another
box (e.g. hermes on the VDR), forward its port through SSH and point busmon at the
local end of the tunnel:

```bash
ssh -NL 6381:localhost:6380 user@192.168.1.5 &   # tunnel VDR bus -> local :6381
REDIS_PORT=6381 ./busmon                          # watch it

# one shot, with automatic tunnel teardown:
./remote-bus.sh user@192.168.1.5
```

Cross-box publishing (an agent on the VDR pushing into a bus on the laptop, or vice
versa) is the symmetric concern: run `agent_bus.py` against the tunnelled port the
same way. Which box hosts the canonical bus is a deployment decision, not baked in
here.

## Tuning

Idle/offline thresholds are the `idleAfter` (2m) and `staleAfter` (10m) constants
at the top of `main.go`.
