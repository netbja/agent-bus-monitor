# agent-bus

Self-contained multi-agent coordination bus over **Redis Streams**, plus the Go
tooling around it. Agents publish status, commands, and notifications on a shared
Redis instance under a required project namespace; a TUI visualises the traffic
live. Broker, client, and monitor all live here — nothing depends on any other
project.

## Components

| Piece      | What it is                                                                  |
|------------|-----------------------------------------------------------------------------|
| broker     | `redis:8-alpine` on `localhost:6380` (`docker-compose.yml`)                |
| `bus/`     | Go package: connection, Streams API (`Bus` handle), publish helpers         |
| `agentbus` | CLI client — status/report/notify/cmd/subscribe/listen (`cmd/agentbus`)     |
| `busmon`   | TUI dashboard: AGENTS / ACTIVITY / INPUT (`cmd/busmon`)                     |

## Deployment topology (current: machine ⇄ remote machine)

The bus is consumed by a concrete two-host setup. This is how the pieces wire up
today — and, importantly, where they *don't* connect.

- **Broker** runs on the **laptop/desktop** (`docker compose up`, redis:8-alpine on `:6380`).
- The **remotebox** (`user@remotebox`) reaches it through an SSH tunnel it opens *to* the laptop/desktop:
  `ssh -L 6380:localhost:6380 user@laptop.local -N`. So `agentbus --host 127.0.0.1`
  on the remote box publishes onto the laptop's bus.
- Two Claude Code sessions run on the laptop under **herdr** in `~/myproject`
  (agents `claude1`, `claude2`); a **hermes agent** runs on the remote box.

**Inbound to the laptop Claudes — `agentbus subscribe` (the canonical bridge).**
A session arms `agentbus subscribe <agent> [idle_secs]` as a Claude Code background task. It blocks
on the project's `:cmd` stream via XREADGROUP, emits one JSON object per fire (a `cmd` object, or a
`heartbeat` object after the idle window, default 240s) then exits — and that exit re-invokes the
Claude session that armed it. Each session re-arms after every fire. The whole loop lives in the `agentbus` binary, so
there is **no wrapper script and no watcher daemon** in the agent path. `busmon` runs alongside as
the human dashboard.

> This supersedes `myproject/tools/bus_watch.sh` (a thin shell wrapper over `agentbus watch`)
> and the persistent `~/.hermes/scripts/bus_watch_hdl.sh` logger loop. An even earlier prototype,
> `cmd/busbridge`, relayed `hermes:cmd:*` into herdr panes with hard-coded pane IDs. Don't
> reintroduce a wrapper, a pane relay, or a `Restart=always` watcher daemon — a restart loop never
> wakes a terminal Claude session, which defeats the wake-on-exit model.

**Separate notification path — NOT the bus.** The `Stop` hook in
`myproject/.claude/settings.local.json` calls `hermes-notify`, which HMAC-signs a POST to
the remote box's hermes **gateway** at `http://<remote>:8644/webhooks/claude-notify`. That route is
`Deliver: signal`: it pings a human over Signal when a Claude task stops. The gateway never
touches Redis, so this path is **independent of the agent bus** — webhook traffic does not appear
on `hermes:*`, and the bus carries nothing back to Signal.

## Run the broker

```bash
docker compose up -d        # redis:8-alpine on :6380, compose project "agent-bus"
docker compose ps
```

Password defaults to `AgentBus2025!`; override via `REDIS_PASSWORD` (see
`.env.example` → copy to `.env`). Redis Streams entries are capped at ~1000 per
stream (XADD MAXLEN ~); the pilot lease and challenge gates use ordinary keys/hashes.

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

`AGENT_BUS_PROJECT` (or `--project <p>`) is required for all commands.

> **Agents:** a copy-paste command reference (with the flag/positional traps that
> cause retries) lives in [`docs/AGENT-BUS-GUIDE.md`](docs/AGENT-BUS-GUIDE.md).

```bash
# Set project once in the shell (or pass --project on each call):
export AGENT_BUS_PROJECT=myproject

agentbus status claude1 working "plan 10 shipped"  # trailing words are joined
agentbus notify "soak 24h started"
agentbus cmd claude2 "check status"                # sends a directive to claude2
agentbus report claude1 "bug fixed"                # curated report (note kind)
agentbus report claude1 --auto "soak 24h done"     # auto = Stop-hook safety net
agentbus subscribe claude1                         # block for next cmd then exit (re-arm to stay subscribed)
agentbus subscribe claude1 3600                    # same, with a 1h idle window before the heartbeat object
agentbus listen                                    # debug tail (all four streams)
agentbus pilot claim --ttl 120s                    # claim pilot lease (self = AGENT_BUS_AGENT)
agentbus gate claude2                              # list open 4-eyes challenges; exit 1 if gated
agentbus verdict --pr 25 myagent approve "LGTM"    # write verdict to {p}:verdicts; --ref resolves matching gate
agentbus verdicts --pr 25                          # roll-up 4-eyes state: APPROVED/REJECTED/PENDING (exit 0/3/2); no-arg lists recent
agentbus usage                                     # print every agent's budget; usage <a> '<json>' writes one

busmon --project myproject                         # live dashboard (last 25 lines, then live)
busmon --project myproject --limit 100             # backfill the last 100 lines on launch
busmon --project myproject --limit 0               # replay all retained history (pre-limit behavior)
busmon --project myproject --reset                 # purge the project's streams first (asks to confirm)
busmon --project myproject --reset --yes           # purge without the confirmation prompt
```

On launch, busmon backfills only the **last `--limit` ACTIVITY lines** (default `25`, merged across
all four streams) before live-tailing — set a persistent default with `AGENT_BUS_BUSMON_LIMIT`, or
`--limit 0` to replay everything. `--reset` clears the project's history (`XTRIM` of the four
streams — it keeps consumer groups and the armed/pilot/gate leases, so cmd delivery is unaffected)
after a `[y/N]` confirmation; a piped/non-TTY stdin counts as "no".

## busmon panes

```
  trading  ·  ⬢ MASTER hermes
┌─ AGENTS ───────────────────────────────────────────────────────────────────┐
│ ⬢ hermes: working (plan 10)   claude1: active (soak bug fixed)   claude2: offline │
├─ ACTIVITY  [live] ────────────────────────────────────────────────────────────┤
│ 23:15:12 [claude1] working | plan 10 shipped                                 │
│ 23:16:02 [notify] Soak 24h started                                           │
│ 23:16:40 [report:note->claude2] soak bug fixed                               │
├─ INPUT ──────────────────────────────────────────────────────────────────────┤
│ > _                                                                          │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **STATUS** — top bar showing the project name and the pilot-lease driver as
  `⬢ MASTER <driver>`, or `autonomous (no master)` when no lease is held.
- **AGENTS** — one chip per agent. `{p}:status` entries set the state (color-coded);
  a `{p}:report` entry also counts as liveness, showing the agent as `active` with
  its last report if it never published a status. Badges: `👂` = the agent is armed
  and listening on `{p}:cmd` (a live `subscribe` lease); `⌛N` = N commands are queued
  for it unread (orange when no one is listening — the "stopped re-arming" tell);
  `🔒N` = open 4-eyes challenges; `⧉` = the agent is attached to a herdr pane (its `HERDR_PANE_ID`);
  `[session·reset]` = the agent's latest budget readout (from `{p}:usage`). Chips wrap across rows to fit the terminal width;
  the master's chip carries a `⬢` marker. Past `idleAfter` it shows `idle Nm`; past
  `staleAfter`, `offline`.
- **ACTIVITY** — scrolling, color-coded feed of status, notifications, commands,
  and reports. It live-tails by default; **Tab** moves focus here. While focused,
  **↑↓** / **j k** select a line (highlighted), **g**/**Home** jumps to the oldest
  and **G**/**End** to the newest, and **y** or **Enter** copies the selected line
  to the clipboard (OSC52, so it works over the SSH tunnel). Mouse wheel / PgUp/PgDn
  still scroll. The title shows `[live]`, the browse indicator `[↑ pause · N below]`,
  or the selection position. **Esc** clears the selection and returns to the input,
  resuming the tail.
- **INPUT** — type a message, Enter publishes on `{p}:notify`; an `@<agent> <text>` line sends a
  directed cmd to that agent (type `@` for autocomplete). Esc/Ctrl-C quits.
  The field inherits the terminal's own fg/bg colors (no forced white-on-blue), so
  it stays legible in any theme.

### Liveness model (why no dedicated heartbeat)

Agents are one-shot CLI invocations, not daemons — nothing is alive between
invocations to emit a periodic heartbeat. Liveness is derived **passively** from
the Redis stream-entry timestamp of each agent's last `status` or `report` entry:
every such publish *is* the heartbeat. A dedicated heartbeat stream would buy nothing
the existing traffic doesn't, until agents become long-running.

## Master skill

`skills/agent-bus-master/SKILL.md` is a Claude Code skill the **master** (the pilot-lease driver,
running inside herdr) uses to drive peer agents' panes: **resync** (inject text into an agent's
herdr pane) and **unblock** (detect a herdr-`blocked` agent, alert a human one-way via Signal + the
bus, then inject the human's answer — typed in busmon as `@<agent> <answer>` or via
`agentbus cmd`). Install/symlink it where the master's Claude Code loads skills. The bridge is
`agentbus pane <agent>` (the agent's `HERDR_PANE_ID`, from `agentbus status`).

## Bus conventions

Stream keys are `{project}:{kind}`. Project and agent names must match `^[a-z][a-z0-9_-]{0,31}$`
(validated by `bus.ValidName`).

| Stream              | Key fields                                       | Pane it feeds                   |
|---------------------|--------------------------------------------------|---------------------------------|
| `{p}:status`        | `agent state message`                            | AGENTS + ACTIVITY               |
| `{p}:report`        | `agent kind(note\|auto) message`                 | AGENTS + ACTIVITY + hermes      |
| `{p}:notify`        | `from message`                                   | ACTIVITY                        |
| `{p}:cmd`           | `from target type ref command`                   | ACTIVITY + agents               |
| `{p}:verdicts`      | `subject author reviewer decision message ref`   | audit ledger                    |

Additional keys: `{p}:pilot` (string, pilot lease), `{p}:gate:{agent}` (hash, 4-eyes challenges),
`{p}:armed:{agent}` (string with TTL, the subscribe presence lease behind the `👂` badge).
States: `working`, `idle`, `blocked`, `done`. All transport conventions live in `bus/stream.go`;
transport-neutral primitives (`Connect`, `ValidStates`, `SanitizeReportMessage`) are in `bus/bus.go`.

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
