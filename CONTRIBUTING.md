# Contributing to agent-bus

Thank you for your interest. This file gathers the rules that already live in the repository
([README.md](README.md), [CLAUDE.md](CLAUDE.md), [AGENTS.md](AGENTS.md),
[docs/memory/](docs/memory/INDEX.md), [docs/testing-redis.md](docs/testing-redis.md)); when they
differ, those files win.

By taking part you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a
vulnerability, do not open an issue: see [SECURITY.md](SECURITY.md).

## Prerequisites

Linux or macOS, Go (the version in [`go.mod`](go.mod)), Docker with Compose, Git and curl. herdr
(0.9.0 or newer) is needed only to run a team, not to build or test.

## Build

One Go module holds everything (`bus/`, `dispatch/`, `mission/`, `plan/`, … and the two binaries
in `cmd/`).

```bash
go build ./... && go vet ./...        # compile + vet everything
go build -o agentbus ./cmd/agentbus   # the CLI client
go build -o busmon  ./cmd/busmon      # the TUI dashboard
./setup.sh install                    # the full install (binaries, skills, herdr plugin, Redis)
```

`agentbus` and `busmon` at the repository root are gitignored build artifacts; do not commit them.

Protocol changes belong in the `bus` package (`bus/stream.go`, `bus/bus.go` for transport-neutral
primitives, `bus/board.go` for the board), not in the binaries. See the Architecture section of
[CLAUDE.md](CLAUDE.md#architecture) before changing the protocol.

## Test

```bash
go test ./... -count=1                # unit tests; Redis integration cases skip
go run ./cmd/agentbus mem check       # shared-memory structure, links, index copy in AGENTS.md
```

### Never test against the production broker

Tests must never use a live broker (the one on `localhost:6380` that your teams use).

- Plain `go test ./... -count=1` does not connect to Redis: integration cases skip unless
  `AGENTBUS_TEST_REDIS_URL` explicitly names an **isolated, disposable** Redis you started for the
  purpose. Ambient `REDIS_URL`, `REDIS_HOST`, `REDIS_PORT` and the default port do not opt tests in.
- An explicitly configured test broker that is unreachable fails the suite rather than skipping.
- The variable is your assertion of isolation, not a detection of production: never point it at an
  existing project's broker, and do not run `docker compose up` against a shared installation just to
  satisfy tests.

```bash
AGENTBUS_TEST_REDIS_URL=unix:///tmp/agentbus-test/redis.sock go test ./... -count=1
```

A run with skipped integration cases is not full validation. Details:
[docs/testing-redis.md](docs/testing-redis.md).

The shell suites under `tests/` (installer, launcher, bootstrap) put fakes first on `PATH`.
Installer tests must stub `docker`, `herdr` and the service managers (`systemctl`, `launchctl`): a
copy of the repository uses the same compose project and container name as the live broker, so an
unstubbed `uninstall --yes` in a test would stop the production Redis.

### Suites that guard the code

Some tests fail on purpose when a change skips a step; keep them green rather than weakening them:

- a new `agentbus` command or flag must be in the command table (`cmd/agentbus/commands.go`);
- a new settings key goes in the registry (`config/keys.go`), never as a literal env name elsewhere;
- a new `{p}:<kind>` Redis key must be classified in `bus/keykinds.go`;
- busmon goldens are regenerated with `go test ./cmd/busmon -run TestShellGoldens -update`, and
  the diff reviewed.

If a test has to change to bless new behaviour, name it in the pull request and say why.

## Shared memory: `docs/memory` and `AGENTS.md`

What the team has decided and learned lives in the repository, not in any agent's or person's
private notes. [AGENTS.md](AGENTS.md) is the entry point every agent shares, whatever the model; the
rules are in [docs/memory/PROTOCOL.md](docs/memory/PROTOCOL.md).

- **Read** [docs/memory/INDEX.md](docs/memory/INDEX.md) at the start of a task, then only the notes
  the task touches. Check a note's `scope`, `status`, `last_verified` and evidence before trusting
  it: a dated note says what was true then. For what is true now, read its owner (Git for what
  merged, the host for what is deployed, the board for request states).
- **Write** only a durable fact: a decision and its reason, a pitfall the next contributor would
  hit, or a question left open. No new fact, no new note. Start one with
  `agentbus mem new <lesson|decision|question> <slug> --title "…"`; `agentbus mem check` fails until
  its sections and evidence are filled.
- **AGENTS.md carries a generated copy of the index.** Edit `docs/memory/INDEX.md`, then run
  `agentbus mem sync`; `go test ./...` fails on drift.
- Do not copy credentials, raw production exports, personal transcripts or another project's data
  into a note. A memory note is context, not permission to merge, deploy or change a live bus.

## Review: 4-eyes

Every change is reviewed by someone other than its author.

- On this project's own team, the review goes on the bus: `agentbus challenge` opens it,
  `agentbus verdict --pr N` records it in the `{p}:verdicts` ledger, and a merge needs
  `agentbus verdicts --pr N` to read `APPROVED`. The roll-up is APPROVED only when the latest
  independent approve (reviewer ≠ author) is newer than any reject; a self-approval is recorded but
  does not count.
- A delivery is bound to the reviewed commit: `agentbus deliver` pushes only when the newest
  independent approve names the exact head (`agentbus verdict --head <sha>`) with no reject after
  it. Only master and the docs agent deliver
  ([decision](docs/memory/decisions/2026-11-02-forge-delivery.md)).
- Never close a stale gate with a verdict of your own (it counts as an approve): use
  `agentbus gate close`.
- A reviewer stops and reports rather than weakening a test.
- The person releases: merge, tag, release, `./setup.sh install` and service restarts each need the
  maintainer's explicit go ([decision](docs/memory/decisions/2026-10-20-dogfood-team.md)).

For an outside contribution, the pull request review on GitHub plays that role; the maintainer
merges.

## Working in a checkout

- Work in a worktree (`.worktrees/<branch>`), never in the main checkout: it is the herdr plugin's
  root, and a build there changes what the plugin runs.
- Keep each pull request to one subject, and fill in the
  [pull request template](.github/pull_request_template.md).
- Add a line under `## Unreleased` in [CHANGELOG.md](CHANGELOG.md) for a user-visible change.

## Licence

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
