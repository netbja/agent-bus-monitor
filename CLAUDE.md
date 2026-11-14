# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A self-contained multi-agent coordination bus over **Redis Streams**, plus the Go tooling around it.
One Go module (`github.com/netbja/agent-bus-monitor`, Go 1.26) holds the broker config, the shared
`bus` package, the `dispatch` package (the wake dispatcher), and two `cmd/` binaries. Nothing
depends on any other project. See `README.md` for the user-facing reference (panes, stream
tables, deployment topology, SSH tunneling).

## Shared memory — read it before you start

What this team has decided and learned lives in the repository, not in any agent's
conversation: start at [`docs/memory/INDEX.md`](docs/memory/INDEX.md), read only the notes your
task touches, and follow [`AGENTS.md`](AGENTS.md) — the entry point every agent shares,
whatever the model. The rules for reading and writing are in
[`docs/memory/PROTOCOL.md`](docs/memory/PROTOCOL.md).

The index, imported so it is in context from the first turn:

@docs/memory/INDEX.md

Two habits matter more than the format:

- **Check a note before trusting it.** Each carries `scope`, `status`, `last_verified` and its
  evidence. For what is true *now*, read the source it cites — Git for what merged, the host
  for what is deployed, the board for request states. A dated note says what was true then.
- **Your own memory is a draft.** Claude Code's per-project memory is fine for preferences and
  links; anything the team must rely on gets promoted here, with sources. It is not shared
  automatically: another agent reaches it only by being told the path and reading the file, and
  it is not on their boot path, not reviewed, and not versioned with the code. If it matters to
  them, it is not shared until it is in the repository, committed.

Write only a durable fact: a decision and its reason, a pitfall the next agent would hit, a
question left open. No new fact, no new note.

## Commands

```bash
docker compose up -d                  # start broker: redis:8-alpine on localhost:6380
go build -o busmon  ./cmd/busmon      # build the TUI dashboard
go build -o agentbus ./cmd/agentbus   # build the CLI client
go install ./...                      # install both to $GOBIN
go build ./... && go vet ./...        # compile + vet everything
go test ./... -count=1                # unit tests; Redis integration cases skip without explicit opt-in
go run ./cmd/agentbus mem check       # shared-memory structure, links, index copy in AGENTS.md
AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1 # isolated Redis only
```

Both binaries (`busmon`, `agentbus`) are gitignored build artifacts.

Tests must never use the production broker. `AGENTBUS_TEST_REDIS_URL` must point
to an independently started, isolated disposable Redis instance; ambient
`REDIS_URL` does not authorize test connections. An unavailable explicitly
configured test broker fails the suite. See [Redis test setup](docs/testing-redis.md).

## Rules that bite every agent

Short forms; the full text, with the reasons, is in the two documents below.

- **The project is required.** Pass `--project <p>` or set `AGENT_BUS_PROJECT`; names match
  `^[a-z][a-z0-9_-]{0,31}$` (`ValidName`). Commands that answer "which project?" (`busmon --list`,
  `--pick`, `agentbus plugin`, `mem`, `start`, `backup`, `suspend`…) sit above the project-required
  exit in `main()`: keep them there.
- **Protocol changes go in `bus/`** (`stream.go`, `bus.go`, `board.go`), not in the binaries.
- **Every dispatcher and conductor write is fenced** by a Lua script that checks the lease token;
  persisted times come from Redis `TIME`. Never add an unfenced write, and never let the dispatcher
  run repository code: the conductor alone runs a mission's tests.
- **The dispatcher is the one automated pane writer**, and it types only the fixed wake line (and
  `/clear` after a declared hand-off; its self-repair (built in step 7b) runs a role's template command in
  a pane it has just created, never in an existing one, decision 2026-11-09; and it closes the pane of a
  pop judged done, the same act as `retire --forget`, decision 2026-11-13). Don't add a second pane
  writer or make it type message text.
- **A woken agent runs `subscribe --drain`**, never a plain `subscribe`, which acknowledges older
  entries unseen.
- **Never write a board entry from a Go struct** (`json.Marshal` + `HSET` drops unknown fields);
  `TestNoStructWriterOfTheBoard` enforces it.
- **Write `{p}:agents` only through `putSnapshot`**: an unverified status never overwrites a
  verified identity.
- **The verdict ledger is the audit**, the gate only a lock: ask `agentbus verdicts`, and close a
  stale gate with `agentbus gate close`, never with a verdict of your own.
- **Tests never use the production broker** (above). Installer tests stub `docker` and `herdr`,
  service tests fake `systemctl`/`launchctl`.
- **`busmon --reset` is `XTRIM`, `--delete` is `DEL`**, and a project name is validated before any
  `SCAN MATCH` glob is built from it.
- **The `subscribe` JSON is versioned** (`"v"`): additive changes only within a version.
- **Settings live in one registry**, `config/keys.go`; a registry env name written as a literal elsewhere
  fails the suite.
- **AGENTS.md carries a copy of the memory index**: edit `docs/memory/INDEX.md`, then run
  `agentbus mem sync`.

## Where the rest lives

Moved unchanged from this file on 2026-11-06:

- [docs/architecture.md](docs/architecture.md): Architecture (`bus/`, `dispatch/`, `mission/`,
  `plan/`, `bus/projects.go`), Stream layout, Budget vs usage, the `ValidAgents` allowlist, The two
  binaries (`agentbus`, `busmon`), and How the bus is actually consumed.
- [docs/operator-notes.md](docs/operator-notes.md): Things that bite (services, installer, roles
  and launcher, settings, health, history, backups, notifications, quotas, asks, pause and suspend,
  fencing, versioning, request priority, merge queue, reports).
