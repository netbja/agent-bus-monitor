# agent-bus

Self-contained multi-agent coordination bus over Redis pub/sub, plus the Go
tooling around it. Agents (claude1, claude2, hermes_laptop, hermes_vdr) publish
status, commands, and notifications on a shared Redis instance; a TUI visualises
the traffic live. Broker, client, and monitor all live here — nothing depends on
any other project.

## Components

| Piece      | What it is                                                  |
|------------|-------------------------------------------------------------|
| broker     | `redis:8-alpine` on `localhost:6380` (`docker-compose.yml`) |
| `bus/`     | Go package: connection, channels, parsing, publish API      |
| `agentbus` | CLI client — publish status/cmd/notify, listen (`cmd/agentbus`) |
| `busmon`   | TUI dashboard: AGENTS / ACTIVITY / INPUT (`cmd/busmon`)      |

`agentbus` is a drop-in replacement for the former `agent_bus.py`: same channels,
same `state|message` payload, same connection conventions.

## Run the broker

```bash
docker compose up -d        # redis:8-alpine on :6380, compose project "agent-bus"
docker compose ps
```

Password defaults to `AgentBus2025!`; override via `REDIS_PASSWORD` (see
`.env.example` → copy to `.env`). Redis is used purely for pub/sub — there are no
application keys — so the volume and AOF only matter if stateful features are
added later (e.g. a move to Redis Streams).

## Build the tools

```bash
go build -o busmon ./cmd/busmon
go build -o agentbus ./cmd/agentbus
go install ./...            # -> $GOBIN/busmon, $GOBIN/agentbus
```

## Use it

```bash
agentbus status claude1 working plan 10 shipped   # trailing words are kept whole
agentbus notify "soak 24h started"
agentbus cmd claude2 "check status"
agentbus listen "status:*"
busmon                                            # live dashboard
```

## busmon panes

```
┌─ AGENTS ───────────────────────────────────────────────────────────────────┐
│ claude1: working (plan 10)   claude2: idle 3m   hermes_vdr: offline          │
├─ ACTIVITY ───────────────────────────────────────────────────────────────────┤
│ 23:15:12 [claude1] working | plan 10 shipped                                 │
│ 23:16:02 [notify] Soak 24h started                                           │
├─ INPUT ──────────────────────────────────────────────────────────────────────┤
│ > _                                                                          │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **AGENTS** — one chip per agent, driven only by `status:{agent}` messages.
  Color reflects the published state. Past `idleAfter` it shows `idle Nm`; past
  `staleAfter`, `offline`.
- **ACTIVITY** — scrolling, color-coded feed of status, notifications, commands.
- **INPUT** — type a message, Enter publishes to `hermes:notify`; Esc/Ctrl-C quits.

### Liveness model (why no dedicated heartbeat)

Agents are one-shot CLI invocations, not daemons — nothing is alive between
invocations to emit a periodic heartbeat. Liveness is derived **passively** from
the timestamp of each agent's last `status:` message: every status publish *is*
the heartbeat. A dedicated heartbeat channel would buy nothing the existing
status traffic doesn't, until agents become long-running.

## Bus conventions

| Channel              | Payload                         | Pane it feeds        |
|----------------------|---------------------------------|----------------------|
| `status:{agent}`     | `state\|message` or `state`     | AGENTS + ACTIVITY    |
| `hermes:notify`      | free text                       | ACTIVITY             |
| `hermes:cmd:{agent}` | command text                    | ACTIVITY             |

Subscribed via `PSUBSCRIBE status:* hermes:*`. States: `working`, `idle`,
`blocked`, `done`. All conventions live in `bus/bus.go` — the single source of
truth shared by both binaries, which is also what makes a future transport swap
(pub/sub → Redis Streams) a one-file change.

## Connection

Resolved by both `agentbus` and `busmon` in the same order as the old
`agent_bus.py`:

1. `REDIS_URL` (e.g. `redis://:pass@host:6380/0`) — takes precedence when set
2. otherwise `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD`
   (defaults `localhost` / `6380` / `AgentBus2025!`)

`--host <host>` overrides `REDIS_HOST`.

## Watching a remote bus over SSH

The bus listens on the host's `localhost:6380` but should not be exposed raw over
the network — the Redis password travels in plaintext. To watch a bus on another
box (e.g. hermes on the VDR), forward its port through SSH and point a tool at the
local end of the tunnel:

```bash
ssh -NL 6381:localhost:6380 user@192.168.1.5 &   # tunnel VDR bus -> local :6381
REDIS_PORT=6381 ./busmon                          # watch it

# one shot, with automatic tunnel teardown:
./remote-bus.sh user@192.168.1.5
```

## Tuning

Idle/offline thresholds are the `idleAfter` (2m) and `staleAfter` (10m) constants
at the top of `cmd/busmon/main.go`.
