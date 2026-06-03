# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A self-contained multi-agent coordination bus over **Redis Streams**, plus the Go tooling around it.
One Go module (`github.com/netbja/agent-bus-monitor`, Go 1.26) holds the broker config, the shared
`bus` package, and two `cmd/` binaries. Nothing depends on any other project. See `README.md` for
the user-facing reference (panes, stream tables, deployment topology, SSH tunneling).

## Commands

```bash
docker compose up -d                  # start broker: redis:8-alpine on localhost:6380
go build -o busmon  ./cmd/busmon      # build the TUI dashboard
go build -o agentbus ./cmd/agentbus   # build the CLI client
go install ./...                      # install both to $GOBIN
go build ./... && go vet ./...        # compile + vet everything
go test ./... -count=1                # run all tests (bus + agentbus + busmon)
```

Both binaries (`busmon`, `agentbus`) are gitignored build artifacts.

## Architecture

**`bus/bus.go` + `bus/stream.go` together are the single source of truth.** `bus.go` holds
transport-neutral primitives (`Connect`, `ValidStates`, `ReportNote`/`ReportAuto`,
`SanitizeReportMessage`). `stream.go` holds the entire Streams API: the `Bus` handle, stream key
naming, all publish helpers (`Bus.Status`/`Bus.Report`/`Bus.Notify`/`Bus.Cmd`), `Bus.Tail` for
read-only observation, `Bus.WatchCmd` for per-agent at-least-once cmd delivery, the pilot lease
(`Bus.Pilot`/`Bus.ReleasePilot`/`Bus.PilotDriver`), and the 4-eyes challenge gate
(`Bus.OpenChallenge`/`Bus.ResolveChallenge`/`Bus.OpenChallenges`). Both binaries call `bus.Connect`
then `bus.Open` to get a `*Bus`; they contain almost no logic of their own. **Put protocol changes
in `bus/stream.go` (or `bus/bus.go` for transport-neutral primitives), not in the binaries.**

### Stream layout

Every stream is namespaced `{project}:{kind}`; the project is required (`--project` or
`AGENT_BUS_PROJECT`) and must match `ValidName` (`^[a-z][a-z0-9_-]{0,31}$`). There are four stream
kinds per project:

| Stream key          | Fields written                                  | Consumed by              |
|---------------------|-------------------------------------------------|--------------------------|
| `{p}:status`        | `agent state message`                           | busmon AGENTS+ACTIVITY   |
| `{p}:report`        | `agent kind message` (kind: note/auto)          | busmon ACTIVITY + hermes |
| `{p}:notify`        | `from message`                                  | busmon ACTIVITY          |
| `{p}:cmd`           | `from target type ref command`                  | busmon ACTIVITY + agents |

Additional keys: `{p}:pilot` (Redis string, pilot lease → driver name + TTL), `{p}:gate:{agent}`
(Redis hash, ref→meta; 4-eyes challenges gate the named agent).

### Replacing the `ValidAgents` allowlist

The old hardcoded `ValidAgents` map is gone. Agent and project names are validated by `ValidName`,
a regex (`^[a-z][a-z0-9_-]{0,31}$`). Adding a new agent requires no code change.

### The two binaries (each a single `main.go`)

- **`agentbus`** — fire-and-forget CLI: `status`/`report`/`notify`/`cmd`/`challenge`/`reply`/
  `verdict`/`pilot`/`gate`/`subscribe`/`watch`/`listen`. Parses args manually; trailing words are
  joined. `subscribe <agent> [idle_secs]` is a one-shot XREADGROUP loop (consumer group = agent
  name) that prints the first addressed cmd entry then **exits** — that exit is the wake signal for
  a Claude Code background task — or prints `__HEARTBEAT__` after the idle window (optional positional
  `idle_secs`, default 240s) and exits so the agent re-arms. "Staying subscribed" is the agent
  re-arming after each fire, **not** a long-lived loop (which would never wake a terminal session).
  `watch` is the legacy alias of `subscribe` (same handler). `listen` tails all four streams via
  `Bus.Tail` for debugging.
- **`busmon`** — `tview`/`tcell` TUI. `Bus.Tail` with lastID `"0"` (backfills history, then live)
  runs in a goroutine pushing UI updates via `app.QueueUpdateDraw`; the `agents` map is
  mutex-guarded; a 1s ticker polls `Bus.PilotDriver` + per-agent `Bus.OpenChallenges` and re-renders
  so chips age into `idle`/`offline` even with no new stream traffic. The `agents` map is populated
  from **both** `status` entries (authoritative state) and `report` entries (liveness only — a
  report-only agent shows state `active`). The ACTIVITY feed live-tails via tview's `trackEnd`
  (set once at start, **not** per message, so scrolling up sticks); `Tab` focuses it to scroll,
  and `activityTitle` recomputes the `[live]`/`[↑ pause · N]` indicator from `GetWrappedLineCount`/
  `GetScrollOffset`/`GetInnerRect`. Mouse wheel works (`EnableMouse(true)`).

### How the bus is actually consumed (lives outside this repo)

A Claude Code session subscribes by arming **`agentbus subscribe <agent> [idle_secs]`** as a
background task: it blocks on the project's `:cmd` stream, prints the first addressed entry, and
exits — and that exit re-invokes the session that armed it, which then re-arms. The whole loop is
self-contained in the `agentbus` binary (this repo); there is **no external wrapper script** and
**no daemon** in the agent path. The old `myprojecttools/bus_watch.sh` (and the persistent
`~/.hermes/scripts/bus_watch_hdl.sh` logger loop) are **superseded** — agents call `agentbus
subscribe` directly. Don't reintroduce a wrapper script or a `Restart=always` watcher daemon: a
restart loop just re-runs the watcher and never wakes a terminal Claude session, which defeats the
wake-on-exit model.

A still-earlier in-repo prototype, `cmd/busbridge` (relay to herdr panes via `herdr pane
send-text/send-keys`), was also **removed**. Don't reintroduce a pane-relay daemon either.

There is also a parallel notification path that is **not** on this bus: an `adv-trading-ai` `Stop`
hook POSTs (via `hermes-notify`) to a hermes gateway on the VDR (`:8644`) that delivers to Signal.
It never touches Redis — see README "Deployment topology".

### Things that bite

- **`AGENT_BUS_PROJECT` is required.** Both binaries die with an error if the project is missing.
  Pass `--project <p>` or set the env var. There is no global default namespace — that was the old
  pub/sub collision bug.
- **`ValidName` replaced the `ValidAgents` allowlist.** Agent names must match
  `^[a-z][a-z0-9_-]{0,31}$`; `agentbus` rejects anything else. No code change needed to add agents.
- **`ValidStates`** (`working`, `idle`, `blocked`, `done`) is still a hardcoded map in `bus.go`.
  Adding a state means editing that map.
- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD` (defaults `localhost`/`6380`/`AgentBus2025!`).
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` sender identity defaults to `hermes`,
  overridable via `AGENT_BUS_AGENT`.
- **`docker-compose.yml` binds `6380` to loopback** (`127.0.0.1:6380:6379`) so the bus is not
  LAN-reachable (the password travels in plaintext); the SSH tunnel is the only remote path. Don't
  revert to a bare `6380:6379` (= `0.0.0.0`) without a reason.
- **Liveness is passive** — there is no heartbeat stream. busmon derives `idle`/`offline` from
  the stream-entry timestamp of each agent's last `status` or `report` entry (`idleAfter` 2m,
  `staleAfter` 10m, constants atop `cmd/busmon/main.go`). Agents are one-shot CLI calls, so each
  status/report publish *is* the heartbeat; don't add a heartbeat unless agents become long-running
  daemons.
- **`Bus.Tail` uses XREAD (no consumer group)** — it never touches group cursors, so busmon never
  competes with `agentbus watch` for cmd entries. `Bus.WatchCmd` uses XREADGROUP with the group
  name set to the agent name, giving at-least-once delivery across one-shot restarts.
- **Stream length is capped** at ~1000 entries per stream (XADD MAXLEN ~ in `stream.go`). Older
  entries are trimmed automatically; the busmon backfill on startup replays whatever remains.
