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

## Deployment topology (current: laptop ⇄ VDR)

The bus is consumed by a concrete two-host setup. This is how the pieces wire up
today — and, importantly, where they *don't* connect.

- **Broker** runs on the **laptop** (`docker compose up`, redis:8-alpine on `:6380`).
- The **VDR** (`bot@vdr`) reaches it through an SSH tunnel it opens *to* the laptop:
  `ssh -L 6380:localhost:6380 sysnet@sysnet-laptop.local -N`. So `agentbus --host 127.0.0.1`
  on the VDR publishes onto the laptop's bus.
- Two Claude Code sessions run on the laptop under **herdr** in `~/Projects/adv-trading-ai`
  (agents `claude1`, `claude2`); a **hermes agent** runs on the VDR.

**Inbound to the laptop Claudes — `bus_watch.sh` (the canonical bridge).**
`adv-trading-ai/tools/bus_watch.sh <agent> [heartbeat_secs]` is a one-shot watcher armed as a
Claude Code background task: it blocks on `hermes:cmd:<agent>` and `hermes:notify`, prints the
first match (or `__HEARTBEAT__` on timeout) and exits — and that exit re-invokes the Claude
session that armed it. Each session re-arms after every fire. `busmon` runs alongside as the
human dashboard.

> An earlier prototype, `cmd/busbridge`, relayed `hermes:cmd:*` into herdr panes via
> `herdr pane send-text/send-keys` with hard-coded pane IDs. It was dropped in favour of
> `bus_watch.sh`, which needs no pane map and rides Claude Code's background-task model directly.

**Separate notification path — NOT the bus.** The `Stop` hook in
`adv-trading-ai/.claude/settings.local.json` calls `hermes-notify`, which HMAC-signs a POST to
the VDR's hermes **gateway** at `http://<vdr>:8644/webhooks/claude-notify`. That route is
`Deliver: signal`: it pings a human over Signal when a Claude task stops. The gateway never
touches Redis, so this path is **independent of the agent bus** — webhook traffic does not appear
on `hermes:*`, and the bus carries nothing back to Signal.

## Run the broker

```bash
docker compose up -d        # redis:8-alpine on :6380, compose project "agent-bus"
docker compose ps
```

Password defaults to `AgentBus2025!`; override via `REDIS_PASSWORD` (see
`.env.example` → copy to `.env`). Redis is used purely for pub/sub — there are no
application keys — so the volume and AOF only matter if stateful features are
added later (e.g. a move to Redis Streams).

> The broker is bound to **loopback only** (`127.0.0.1:6380:6379` in `docker-compose.yml`), so it
> is not reachable from the LAN — the SSH tunnel below is the only remote path. This matters
> because the Redis password travels in plaintext. (It previously mapped `6380:6379`, i.e. bound
> `0.0.0.0`; verify the active bind with `ss -tlnp | grep 6380`.)

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

The broker is bound to loopback (`127.0.0.1:6380`) and must stay that way — the Redis password
travels in plaintext, so the bus is never exposed raw over the network. To watch a bus on another
box, forward its port through SSH and point a tool at the local end of the tunnel:

```bash
ssh -NL 6381:localhost:6380 user@192.168.1.5 &   # tunnel VDR bus -> local :6381
REDIS_PORT=6381 ./busmon                          # watch it

# one shot, with automatic tunnel teardown:
./remote-bus.sh user@192.168.1.5
```

> The deployed laptop⇄VDR setup runs this **in reverse**: the VDR opens the tunnel *into* the
> laptop's bus (`ssh -L 6380:localhost:6380 …`) rather than the laptop reaching out. See
> **Deployment topology** above.

## Tuning

Idle/offline thresholds are the `idleAfter` (2m) and `staleAfter` (10m) constants
at the top of `cmd/busmon/main.go`.
