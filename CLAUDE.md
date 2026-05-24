# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A self-contained multi-agent coordination bus over Redis **pub/sub** (no application keys, no
persistence relied upon), plus the Go tooling around it. One Go module
(`github.com/netbja/agent-bus-monitor`, Go 1.26) holds the broker config, the shared `bus`
package, and two `cmd/` binaries. Nothing depends on any other project. See `README.md` for the
user-facing reference (panes, channel tables, deployment topology, SSH tunneling).

## Commands

```bash
docker compose up -d                  # start broker: redis:8-alpine on localhost:6380
go build -o busmon  ./cmd/busmon      # build the TUI dashboard
go build -o agentbus ./cmd/agentbus   # build the CLI client
go install ./...                      # install both to $GOBIN
go build ./... && go vet ./...        # compile + vet everything
```

There are **no tests** in the repo yet; `go test ./...` is a no-op. Add `_test.go` files next to
the code if you write any. Both binaries (`busmon`, `agentbus`) are gitignored build artifacts.

## Architecture

**`bus/bus.go` is the single source of truth.** Every convention — connection resolution, channel
naming (`StatusChannel`, `CmdChannel`, `NotifyChannel`), the wire format, the publish helpers
(`Status`/`Cmd`/`Notify`), and `Parse` — lives in this one file. Both binaries import it and
contain almost no logic of their own. This is deliberate: swapping the transport (pub/sub → Redis
Streams) or changing the wire format is meant to be a one-file change. **Put protocol changes in
`bus/bus.go`, not in the binaries.**

The two binaries (each a single `main.go`):
- **`agentbus`** — fire-and-forget CLI: `status`/`cmd`/`notify`/`listen`/`report`. Parses args
  manually (no flag library beyond the `--host` shim); trailing words are joined into one message.
  `report <agent> [--auto] <msg>` publishes on `hermes:report:{agent}` (`kind|message`, kind
  `note`/`auto`), sanitized + truncated in `bus.go`; consumed by hermes_laptop, not the other agents.
- **`busmon`** — `tview`/`tcell` TUI. `PSUBSCRIBE status:* hermes:*` runs in a goroutine that
  pushes UI updates via `app.QueueUpdateDraw`; the `agents` map is mutex-guarded; a 1s ticker
  re-renders so chips age into `idle`/`offline` even with no new traffic. The `agents` map is
  populated from **both** `status:` (authoritative state) and `report:` (liveness only — a
  report-only agent shows state `active`). The ACTIVITY feed live-tails via tview's `trackEnd`
  (set once at start, **not** per message, so scrolling up sticks); `Tab` focuses it to scroll,
  and `activityTitle` recomputes the `[live]`/`[↑ pause · N]` indicator from `GetWrappedLineCount`/
  `GetScrollOffset`/`GetInnerRect`. Mouse wheel works (`EnableMouse(true)`).

### How the bus is actually consumed (lives outside this repo)

The canonical inbound bridge to a Claude Code session is **`bus_watch.sh`**, in the *separate*
`adv-trading-ai` repo (`tools/bus_watch.sh`), not here. It's a one-shot watcher armed as a Claude
background task: blocks on `hermes:cmd:<agent>` / `hermes:notify`, prints the first match (or
`__HEARTBEAT__`) and exits, and that exit re-invokes the session. A prior in-repo prototype,
`cmd/busbridge` (relay to herdr panes via `herdr pane send-text/send-keys`), was **removed** in
favour of it. Don't reintroduce a pane-relay daemon without checking `bus_watch.sh` first.

There is also a parallel notification path that is **not** on this bus: an `adv-trading-ai` `Stop`
hook POSTs (via `hermes-notify`) to a hermes gateway on the VDR (`:8644`) that delivers to Signal.
It never touches Redis — see README "Deployment topology".

### Things that bite

- **Allowlists are hardcoded** in `bus.go`: `ValidAgents` (`claude1`, `claude2`, `hermes_laptop`,
  `hermes_vdr`) and `ValidStates` (`working`, `idle`, `blocked`, `done`). `agentbus` rejects
  anything else. Adding an agent or state means editing these maps.
- **Wire format** is `state|message`, split on the *first* `|` only (`SplitN(data, "|", 2)`), so
  messages may contain `|`. A bare `state` with no message is valid.
- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD` (defaults `localhost`/`6380`/`AgentBus2025!`).
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` cmd-sender identity defaults to `hermes_vdr`,
  overridable via `AGENT_BUS_AGENT`.
- **`docker-compose.yml` binds `6380` to loopback** (`127.0.0.1:6380:6379`) so the bus is not
  LAN-reachable (the password travels in plaintext); the SSH tunnel is the only remote path. Don't
  revert to a bare `6380:6379` (= `0.0.0.0`) without a reason.
- **Liveness is passive** — there is no heartbeat channel. busmon derives `idle`/`offline` from
  the timestamp of each agent's last `status:` *or* `report:` message (`idleAfter` 2m, `staleAfter`
  10m, constants atop `cmd/busmon/main.go`). Agents are one-shot CLI calls, so each status/report
  publish *is* the heartbeat; don't add a heartbeat unless agents become long-running daemons.
