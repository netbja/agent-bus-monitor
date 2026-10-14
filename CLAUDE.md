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

## Architecture

**`bus/bus.go` + `bus/stream.go` together are the single source of truth**, with two small
satellite files: `bus/projects.go` for the one thing that is *not* project-scoped (see below), and
`bus/board.go` for the shared task-ownership board (`{p}:board`, a hash — not a stream). `bus.go` holds
transport-neutral primitives (`Connect`, `ValidStates`, `ReportNote`/`ReportAuto`,
`SanitizeReportMessage`). `stream.go` holds the entire Streams API: the `Bus` handle, stream key
naming, all publish helpers (`Bus.Status`/`Bus.Report`/`Bus.Notify`/`Bus.Cmd`), `Bus.Tail` for
read-only observation, `Bus.WatchCmd` for per-agent at-least-once cmd delivery, the pilot lease
(`Bus.Pilot`/`Bus.ReleasePilot`/`Bus.PilotDriver`), and the 4-eyes challenge gate
(`Bus.OpenChallenge`/`Bus.ResolveChallenge`/`Bus.OpenChallenges`). Both binaries call `bus.Connect`
then `bus.Open` to get a `*Bus`; they contain almost no logic of their own. **Put protocol changes
in `bus/stream.go` (or `bus/bus.go` for transport-neutral primitives, `bus/board.go` for the
board), not in the binaries.**

Two more `bus` files carry v0.8's dispatcher primitives: `bus/obligation.go` (`EnsureGroup`,
which creates an agent's cmd group at the horizon floor `now − DefaultHorizon` (24h) — called by
`Bus.Status` — plus `Obligation`, `Armed`, `GroupNames`, `CmdTargets`) and `bus/dispatchkeys.go`
(the `{p}:dispatch` lease — `AcquireDispatch`/`RenewDispatch`/`ReleaseDispatch`/`DispatchAlive` —
and the fenced writes `ReserveWake`/`PutWake`/`DeleteWake`/`FencedCmd`/`FencedNotify`/
`FencedEnsureGroup`). `bus/delivery.go` holds `WatchCmdDelivery` and `DrainCmd`.

**`dispatch/`** is the reconciliation loop behind `agentbus dispatch`, built on exported `bus` API
only: `Decide` (`decide.go`, a pure function of obligation, armed, snapshot, inspection and wake
record → none/prompt/defer/escalate), the `Runtime` interface and its only implementation `Herdr`
(`herdr.go`, the one file that shells out to herdr; per-client ready/busy signatures — a client
without one is never prompted), process identity (`ident.go`: `AncestorAmong`, `ProcStart`), and
`Run` (`loop.go`). It holds no durable local state: every decision is recomputed from Redis plus
herdr, so a restart loses nothing. Spec:
`docs/superpowers/specs/2026-10-10-event-driven-dispatch-design.md`.

**`mission/`** (v0.10) runs large changes as **missions**: a versioned `docs/changes/<c>/missions.json`
read from Git at a commit, a pure state machine (`Next` in `state.go`), validation (`validate.go`,
`paths.go`), a typed store over fenced writes (`store.go`), `Git` (`git.go`: every git and test
command in its own process group, killed whole on cancel), and the conductor behind `agentbus
missions` (`conductor.go` — one reconciliation `Pass`: request, check scope and tests, open and read
the 4-eyes review; `integrate.go` — merge into `change/<c>`, run the change suite, `Startup` crash
repair; `ops.go` — operator `load` (`--force`, `--recover`)/`retry`/`reassign`/`skip`/`abort` queued
on `{p}:missionops`; `reload.go` — revisions; `recover.go` — rebuild from merge trailers; `run.go` — flock,
lease, inbox, tick). Its Redis primitives are in `bus/conductorkeys.go`: the `{p}:conductor` lease
(`PX 15000`, renewed every 5s) and fenced writes on `{p}:change:{c}`, `{p}:missions:{c}`, the board,
gates and cmd. Spec: `docs/superpowers/specs/2026-10-11-missions-design.md`.

`bus/projects.go` is the exception that proves the rule: `bus.Projects(ctx, client)` is a **package
function taking a raw client**, not a `*Bus` method, because it answers the question you have
*before* you know a project name. `Open` already demands a valid project, so a method there would be
a circle. It SCANs the keyspace and attributes each key to a project via `projectFromKey`
(suffix in `projectKinds` **and** `ValidName` on the prefix). That second test is what keeps
`{p}:gate:{agent}` and `{p}:armed:{agent}` from forging a phantom project — they split to a
"project" that still holds a colon — so the scanner needs no special case. Foreign keys on a shared
broker are skipped, never guessed at.

### Stream layout

Every stream is namespaced `{project}:{kind}`; the project is required (`--project` or
`AGENT_BUS_PROJECT`) and must match `ValidName` (`^[a-z][a-z0-9_-]{0,31}$`). There are four stream
kinds per project:

| Stream key          | Fields written                                  | Consumed by              |
|---------------------|-------------------------------------------------|--------------------------|
| `{p}:status`        | `agent state message`                           | busmon Agents+Activity   |
| `{p}:report`        | `agent kind message` (kind: note/auto); optional `full` (retained only when it differs from the ≤500 preview) | busmon ACTIVITY + hermes |
| `{p}:notify`        | `from message`                                  | busmon ACTIVITY          |
| `{p}:cmd`           | `from target type ref command`; optional `wake=no` (delivered, never wakes) | busmon ACTIVITY + agents + dispatch |

Additional keys: `{p}:pilot` (Redis string, pilot lease → driver name + TTL), `{p}:gate:{agent}`
(Redis hash, ref→meta; 4-eyes challenges gate the named agent), `{p}:verdicts` (Redis stream,
append-only 4-eyes ledger; one entry per verdict — subject/author/reviewer/decision/message/ref —
capped at `verdictMaxLen()` = 10000, override `AGENT_BUS_VERDICT_MAX`), `{p}:agents` (Redis hash,
agent→`AgentSnapshot`; state + `pane` + `session`), `{p}:usage` (Redis hash, agent→`UsageSnapshot`;
**per-agent** model + context fill), `{p}:budget` (Redis hash, provider→`BudgetSnapshot`;
**account-scope** session/weekly window per provider), `{p}:board` (Redis hash, task→`BoardEntry`;
the shared ownership registry — owner + state + branch, claimed via `agentbus board claim`),
`{p}:dispatch` (Redis string, the dispatcher lease → token, `PX 15000` renewed every 5s; its
presence is what `agentbus dispatch --status` and busmon's `dispatch up/down` report; in
`projectKinds`), `{p}:wake:{agent}` (Redis hash, the dispatcher's per-agent wake record — state,
`at` from Redis `TIME`, tries, pid, escalated, gap flag; two colons, so it cannot forge a project),
`{p}:services` (v0.12, Redis hash: the `installed` marker `agentbus up` writes, plus one JSON state per
service kind — state, detail, `at` from Redis `TIME`, pid, host, last error, log path; in `projectKinds`).
`AgentSnapshot` also carries `pid`/`pid_start`/`terminal`, recorded by `agentbus status` inside
herdr — the identity the dispatcher checks before typing into a pane — or by `agentbus bind`
(v0.10.1, `Bus.Bind` in `bus/ident.go`) for a client whose commands do not descend from its pane
(Codex's shared app-server). cmd `type` gains
`escalation` (dispatcher → `--escalate-to`, default `master`), `blocked` (written by `request
block` to the requester, only on a transition or a new reason) and `complete` (conductor → the
escalation target, once per change revision whose missions are all integrated).

### Budget vs usage: two scopes, two keys, zero cooperation

`agentbus refresh` (run by the sentinel on every wake, from the `usage` package) reads local
artefacts and publishes both halves. **No agent tees anything** — the old status-line tee is gone;
it required a hand-written `statusLine` script, and with a third-party status line (ccstatusline)
it silently never fired.

- **`{p}:budget` is account-scope.** A session/weekly window belongs to the *subscription* every
  agent draws on, so it is keyed by **provider**, not agent — five agents on one account share one
  number. Source: `~/.cache/ccstatusline/usage.json` (ccstatusline's cache of Anthropic's OAuth
  usage endpoint; free to read, costs no tokens). `BudgetSnapshot.Extra` carries provider-specific
  gauges so a new provider needs no schema change.
- **`{p}:usage` is per-agent.** Model + context fill, read from that agent's own transcript at
  `~/.claude/projects/*/{session}.jsonl`. `UsageSnapshot.Weekly`/`Session`/`Reset` are **deprecated**
  (they held account-scope data); new writers leave them empty.
- **How the transcript is found:** `agentbus status` captures `CLAUDE_CODE_SESSION_ID` into
  `AgentSnapshot.Session` from the environment, exactly as it captures `HERDR_PANE_ID` — see
  `bus.AgentIdent`. The agent never types it, so it cannot drift. A peer with no session id (a
  non-Claude-Code agent) is skipped with a note, never invented.
- **Deliberately not reported:** context *percentage* (needs a per-model window table that rots
  every model release) and cost (needs a price table that rots the same way; on a subscription the
  figure is fiction). Raw context tokens are what is actually known.
- `refresh` never fails hard — a missing source becomes a note, because it runs from the sentinel's
  cron wake where a hard exit loses the whole caretaker pass.

### Replacing the `ValidAgents` allowlist

The old hardcoded `ValidAgents` map is gone. Agent and project names are validated by `ValidName`,
a regex (`^[a-z][a-z0-9_-]{0,31}$`). Adding a new agent requires no code change.

### The two binaries (`cmd/agentbus`, `cmd/busmon`: a `main.go` plus small per-command files)

- **`agentbus`** — fire-and-forget CLI: `status`/`report`/`reports`/`notify`/`cmd`/`thread`/`challenge`/`reply`/
  `verdict`/`verdicts`/`pilot`/`gate`/`agents`/`bind`/`usage`/`budget`/`board`/`shutdown`/`refresh`/`request`/`dispatch`/`missions`/`mission`/`mem`/`roles`/`up`/`down`/`service`/`subscribe`/`watch`/`listen`/`version`. Parses args manually;
  trailing words are joined. `subscribe [--since <cursor>] <agent> [idle_secs]` is a one-shot
  XREADGROUP loop (consumer group = agent name) that emits **one JSON object** then **exits** — a
  `cmd` object (exit 0) on an addressed entry, or a `heartbeat` object (exit 64) after the idle
  window (positional `idle_secs`, default 240s). Caller persists the `id` field and passes it back
  as `--since <id>` on re-arm; omitting `--since` starts at "now" (no backlog replay); `--since 0`
  = at-least-once replay of whatever the group still holds — once `status` has created the group
  at the horizon floor (v0.8, `EnsureGroup`), entries older than that floor are never replayed. Re-arm iff `rearm` is true. "Staying subscribed" is re-arming after
  each fire, **not** a long-lived loop (which would never wake a terminal session). `agents [--json]`
  lists every peer's current state. `watch` is the legacy alias of `subscribe` (same handler).
  `listen` tails all four streams via `Bus.Tail` for debugging. `report` now retains the full
  text (newlines/tabs kept, ≤8000, `AGENT_BUS_REPORT_FULL_MAX`) in a `full` field **only when it
  differs from the one-line ≤500 preview**; `reports [--json]` lists recent reports (marking those
  with more via ` (+N)`) and `reports <id>` prints one report's full retained text.
  `listen`/busmon still render the preview. `verdict` is subject-first
  (`--pr N`/`--subject S`), always appends to the `{p}:verdicts` ledger, publishes a live
  `CmdVerdict`, and resolves a matching `--ref` gate only as a best-effort bonus (it no longer
  errors when no challenge is open; a `--ref` open on the *reviewer*, a mission review the conductor
  closes itself, is left alone and not reported as missing — `verdictGateNotice`). `verdicts [--pr N|--subject S]` prints the roll-up 4-eyes
  state (`APPROVED`/`REJECTED`/`PENDING`) with exit codes 0/3/2; no-arg lists recent verdicts
  across all subjects. `cmd [--ref T] <target> <command>` now prints its entry id (the thread
  root) and accepts `--ref` to continue an existing thread; `thread <T>` reads the `:cmd` stream
  and prints every entry whose `ref` or `id` equals `T`, chronologically — the thread-id of any
  message is its `ref` if set, else its own id, so a directive id and a challenge ref both work.
  Live read of `:cmd` (recent ~1000); the verdict ledger remains the durable audit.
  `subscribe --drain <agent>` (`Bus.DrainCmd`) empties the agent's queue — PEL first, then unread,
  **no floor**, ACK after output — as JSONL (one `"v":1` object per entry, with `age_s` on every
  entry and `stale` only for entries older than the 24h horizon) and exits 0 (output), 64 (empty) or 75 (error, e.g. the
  receiver lease is held). It is what a woken agent runs. `cmd --no-wake` writes `wake=no`
  (`Bus.CmdNoWake`; `shutdown` uses it). `dispatch [--horizon 24h] [--escalate-after 10m]
  [--escalate-to master]` runs `dispatch.Run` in the foreground (JSON decision log on stderr; a
  second dispatcher for the same project exits 0; one whose lease lapsed re-acquires while the
  project still has keys, and exits 0 once it is deleted); `dispatch --status` prints `dispatch up`
  (exit 0) or `dispatch down` (exit 1).
- **`busmon`** — `tview`/`tcell` TUI, btop-style since v0.9 (spec
  `docs/superpowers/specs/2026-10-10-busmon-ui-redesign-design.md`). On launch it backfills only
  the **last `--limit` Activity lines** (default 25, or `AGENT_BUS_BUSMON_LIMIT`; `--limit 0` = all
  history) via `Bus.Recent`, then live-tails from the per-stream cursors `Recent` returns through
  `Bus.TailFrom` (no replay, no `"$"` gap); `--limit 0` falls back to `Bus.Tail(ctx, "0", …)`.
  `--reset` purges the four streams first (`Bus.Purge` = `XTRIM MAXLEN 0`, behind a `[y/N]`
  confirmation or `--reset --yes`). `--list` prints the broker's projects to **stdout** and exits
  (plain text, never the TUI). `--delete <p>` retires a project (`bus.DeleteProject` = `DEL` of every
  `{p}:*` key) behind the same gate; the prompt states key count, agent count and last activity.
  `--theme auto|dark|light` (or `AGENT_BUS_BUSMON_THEME`). busmon writes as a **person**, never
  as an agent: `--as <name>` or `AGENT_BUS_HUMAN`, else `pilot` (`humanIdentity`, `human.go`),
  through `Bus.HumanCmd`/`HumanNotify`, whose entries carry `human=yes` (`Event.Human`,
  subscribe's `"human": true`). The header shows `♙ Bernard [human · pilot]`; `@all` fans a
  directive out to every known agent; a plain-text notify needs a second Enter, because nobody
  reads one and nothing wakes for it.

  Files: `theme.go` (colour roles, palettes dark/light/16/none, the closed single-cell glyph set —
  the only place colours and glyphs are named), `cells.go` (preview/detail sanitising, grapheme
  width, `Fit`/`FitLine`, `DrawLine`, `DrawFrame` with the scrollbar), `layout.go`
  (`computeLayout`: pure geometry, three tiers wide ≥120 / medium 80–119 / narrow), `listview.go`
  (the one selectable scrolling list: Agents, Activity, Board and Requests are all instances;
  selection and wheel anchor follow keys, tail mode for Activity), `agents.go` (attention sort,
  flags, ctx-token parser), `activity.go` (event → row, pause cursor), `board.go`, `header.go`,
  `detail.go` + `detail_agent.go` (overlays), `keys.go` (one key table per mode drives the handler,
  the footer and the help), `poll.go` (`pollOnce` behind the `busReader` interface: the 1 s tick and
  its read inventory), `ui.go` (stable primitives, `screenView`, the modal layer), `main.go` (flags,
  CLI paths, tail and poll goroutines).

  **Rules that keep it straight:** every primitive is created once; `screenView.Draw` recomputes the
  layout and refills rows from the `shared` model but never changes focus and never does I/O —
  a size change runs `relayout` via `go app.QueueUpdateDraw` (calling `SetFocus` from a draw would
  deadlock tview). tview delivers keys to the **root**, so `screenView`/`modalView` relay them to
  the focused child. Rows are `Line` values (role-tagged segments), measured in cells; only overlay
  bodies use colour tags, through `tag()`/`tg()`, which resolve to theme roles. No literal outside
  the glyph set (`TestNoStrayNonASCIILiterals`); `TestAlignmentSuite` draws every size × palette ×
  hostile fixture and checks every frame's corners and borders. The tick reads every second
  (pilot, armed, lag, agents, usage, budgets, board, challenges) and every 5th tick the dispatcher
  facts (`DispatchAlive`, `Obligation`, `Wake`); a failed read keeps the last value (queue shows
  `N?`). The usage scopes stay apart: account budget (used %) in the header, an agent's own context
  (raw tokens) in its row. The declared state is never overwritten by silence or by a report.

### How the bus is actually consumed (lives outside this repo)

A Claude Code session subscribes by arming **`agentbus subscribe <agent> [idle_secs]`** as a
background task: it blocks on the project's `:cmd` stream, prints the first addressed entry, and
exits — and that exit re-invokes the session that armed it, which then re-arms. The whole loop is
self-contained in the `agentbus` binary (this repo); there is **no external wrapper script** and
**no daemon** in the subscribe path (the one process that runs alongside is `agentbus
dispatch`, which only types a wake line — see the dispatcher paragraph below). The old `myprojecttools/bus_watch.sh` (and the persistent
`~/.hermes/scripts/bus_watch_hdl.sh` logger loop) are **superseded** — agents call `agentbus
subscribe` directly. Don't reintroduce a wrapper script: a restart loop just re-runs the watcher
and never wakes a terminal session.

A still-earlier in-repo prototype, `cmd/busbridge` (relay to herdr panes via `herdr pane
send-text/send-keys`, with hard-coded pane ids), was also **removed**. v0.8 revised that decision
on purpose (spec §11, `docs/memory/decisions/2026-10-10-event-driven-dispatch.md`): **the only
automated process that types into panes is `agentbus dispatch`, and it types a wake line, never a
message; master's manual pane injection remains the fallback while no dispatcher runs.** The wake
line is fixed — `[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus
--project <project> subscribe --drain <agent>` — and the agent then drains through Redis. The
dispatcher touches only unarmed agents with a real obligation, in a pane that is ready, unfocused
and pid-verified. Don't add a second pane writer, and don't make the dispatcher type message
content. Since v0.12 it runs, with the conductor, as a **user service** (systemd `--user`,
launchd), started by `agentbus up`; herdr tabs remain only as the explicit fallback
(`docs/memory/decisions/2026-10-14-project-services.md`).

There is also a parallel notification path that is **not** on this bus: an `adv-trading-ai` `Stop`
hook POSTs (via `hermes-notify`) to a hermes gateway on the VDR (`:8644`) that delivers to Signal.
It never touches Redis — see README "Deployment topology".

### Things that bite

- **The conductor is the only process that runs repository code, and the only writer of mission
  state.** `agentbus missions` runs the missions' test commands (arbitrary code from agents' commits,
  as any CI does) and every write to `{p}:change:*`/`{p}:missions:*` goes through a fenced script in
  `bus/conductorkeys.go`; the CLI (`agentbus mission load|retry|reassign|skip|abort`) only queues operations on
  `{p}:missionops` and waits for the conductor's answer. Never add an unfenced mission write, and
  never let the dispatcher run code. The conductor writes only to `change/<change>` (validated at
  load) and never pushes or touches `main`.
- **The role scripts run `agentbus roles`, not Python.** `scripts/lib/roles.sh`, `agent-launch`,
  `link-role-skills.sh` and `bootstrap`'s template call `${AGENTBUS_BIN:-$REPO/agentbus} roles …`
  (package `rolecfg/`: manifest, launch, skills, template — the port of the removed
  `role_config.py`; its block in `main()` sits above the project-required exit, like `mem`). The
  repo binary must be built first (`./setup.sh install`); `tests/run.sh` and `tests/lib.sh` build one.
  `agent-launch` `exec`s `agentbus`, which binds and then `syscall.Exec`s the client: the pid
  never changes, which is what `bind` relies on. Outputs are kept byte-identical to the Python
  (dry runs, adapter spec JSON via `pyJSON`, template); `tests/roles_scripts_test.sh` covers the
  scripts end to end.
- **`setup.sh` is the installer; tests of it must stub `docker` and `herdr`.** `install`,
  `update`, `uninstall`, `status` (`install.sh` is an alias). It stamps `buildinfo.Version` (from
  `git describe` when it is a `v*` tag, else `dev`) and `buildinfo.Repo` via ldflags; `agentbus up`
  finds `scripts/bootstrap` through `AGENTBUS_REPO`, then `buildinfo.Repo`, then the binary's
  directory. A copy of the repository uses the same compose project and container name as the live
  broker, so an unstubbed `uninstall --yes` in a test stops the production Redis.
  `tests/installer_test.sh` puts fakes first on `PATH`; keep it that way. herdr-plus is registered
  in herdr's **global** plugin registry (`herdr plugin install`), which every session sees;
  bootstrap only links a checkout when `herdr plugin list` lacks it.
- **Project services (v0.12): the OS manager supervises, nothing of ours does.** `service/`
  writes systemd `--user` units / launchd plists (never enabled at login), the 0600 env file the
  services load, the rotating log and the restart budget. Every unit runs `agentbus --project P
  service run <kind>` (`cmd/agentbus/servicerun.go`), whose exit code *is* the restart rule: 0 is
  final (stopped, project gone, another instance, invalid config, budget spent), 1 restarts. Its
  state goes to `{p}:services` through an `EXISTS`-guarded script (`bus/services.go`); without the
  `installed` marker that `up` writes, a service exits and writes nothing, so a deleted project is
  never resurrected. Don't add a supervisor loop, a `nohup` fallback or a login autostart; don't
  put a secret in a unit; tests use fake `systemctl`/`launchctl` (`tests/installer_test.sh`,
  `service/manager_test.go`) and never the user's manager. busmon only reads the state and the
  local log (`F3`), never starts or parents a service; `busmon --delete` removes the local
  units before the keys.
- **busmon's update check never runs on a `dev` build** (or with `AGENT_BUS_NO_UPDATE_CHECK=1`):
  `cmd/busmon/update.go` asks GitHub once a day, caches in `~/.cache/agentbus/latest.json`, fails
  silently and only adds a muted `↑ vX.Y.Z` to the header. Don't make it block startup or nag.
- **`"docs": "<agent>"` in missions.json** makes the conductor send that agent one directive when
  the change completes, written with the `complete` notice in one fenced script
  (`Store.PutChangeWithCmds` → `FencedCmdsWithChange`). The docs agent opens a PR from
  `pr/<change>` and never merges.
- **The launcher's environment rules** (`rolecfg/config.go`, `launch.go`): `env_refs` may not
  target loader/hook variables (`LD_*`, `DYLD_*`, `HERDR_*`, `BASH_ENV`, `NODE_OPTIONS`, …);
  `ANTHROPIC_API_KEY` is dropped for every client unless `AGENT_LAUNCH_KEEP_API_KEY=1` or
  `env_refs` names it; elevated options are announced on stderr. A missing skill warns and the
  agent still starts; `roles install-skills` stays strict.
- **`agentbus mem` needs no project and no broker.** It works on files under the repository
  root (`memory/` package: `check`, `new`, `sync`), so its block in `main()` sits above the
  project-required exit, like `limits` and `version`. AGENTS.md carries a generated copy of
  `docs/memory/INDEX.md` between `<!-- memory-index:begin -->` and `<!-- memory-index:end -->`:
  edit the index, run `agentbus mem sync`; `TestRepositoryPasses` fails `go test ./...` on drift.
- **`AGENT_BUS_PROJECT` is required.** Both binaries die with an error if the project is missing.
  Pass `--project <p>` or set the env var. There is no global default namespace — that was the old
  pub/sub collision bug. The **one** exception is `busmon --list`, which exists precisely to answer
  "what do I put in `--project`?" — so its block in `main()` must stay **above** the project-required
  exit, and above `--reset` (a read-only query never purges). Move it down and it dies on the error
  it was written to resolve; `TestProjectsFindsALiveProject` will not catch that, only the ordering
  in `main()` will.
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
- **Liveness is passive** — there is no heartbeat stream. busmon shows the age of each agent's
  last declared status as a freshness bar (empty after `staleAfter` 10m, atop
  `cmd/busmon/main.go`) and never infers `offline`. Agents are one-shot CLI calls, so each
  status/report publish *is* the heartbeat; don't add a heartbeat unless agents become long-running
  daemons.
- **`Bus.Tail` uses XREAD (no consumer group)** — it never touches group cursors, so busmon never
  competes with `agentbus watch` for cmd entries. `Bus.WatchCmd` uses XREADGROUP with the group
  name set to the agent name, giving at-least-once delivery across one-shot restarts.
- **Stream length is capped** at ~1000 entries per stream (XADD MAXLEN ~ in `stream.go`). Older
  entries are trimmed automatically; the busmon backfill on startup replays only the last `--limit`
  of whatever remains (default 25; `--limit 0` for all).
- **`busmon --reset` is `XTRIM`, not `DEL`.** `Bus.Purge` trims each stream to `MAXLEN 0`, so the
  visible history is wiped but consumer groups (cmd at-least-once cursors) and the
  `armed`/`pilot`/`gate` keys **survive** — don't switch it to `DEL`, which would reset cmd delivery
  state. The confirmation reads stdin **before** the TUI starts; a piped/non-TTY stdin counts as
  "no", so `--reset` never purges unattended without `--yes`. **`busmon --delete <p>` is the `DEL`**
  — the deliberate opposite: it retires the project, so consumer groups, leases and every hash go
  with it. `--reset` clears a project you are keeping; `--delete` removes one you are not.
- **`ValidName` in `bus.ProjectKeys` is what stands between `--delete '*'` and an erased broker.**
  The `SCAN MATCH` glob is built from the caller's string (`project+":*"`), so the name is validated
  **before** the client is touched — `TestProjectKeysRejectsGlobsBeforeDialing` passes a nil client
  on purpose, so a guard that ran too late would panic rather than pass. The trailing colon also
  makes the prefix exact: `demo:*` never reaches `demo-2574:status`, so a delete cannot take a
  same-prefixed sibling with it. Never build that glob from an unvalidated string.
- **The verdict ledger is the audit source of truth, the gate is just a lock.** `verdict` writes
  `{p}:verdicts` unconditionally (self-approvals included — the roll-up ignores them, but the record
  stays). The roll-up rule: APPROVED iff the latest independent approve (reviewer ≠ author) is newer
  than any reject. Don't gate "was this approved?" on the `{p}:gate:{agent}` hash — that only holds
  *open* challenges; query `agentbus verdicts` instead.
- **A plain `subscribe` acknowledges older entries unseen; `--drain` does not.** With no
  `--since`, `subscribe` floors at the server's "now", so everything that waited for an unarmed
  agent is ACKed without output. That is why a woken agent must run `subscribe --drain` — keep the
  wake text and the skills pointing there, never at a plain `subscribe`.
- **An unverified `status` never overwrites a verified identity.** Every `{p}:agents` write goes
  through `putSnapshot` (`bus/ident.go`, one WATCH/MULTI transaction, `mergeSnapshot`): a status
  with no pid keeps the stored pane/pid/pid_start/terminal, a verified one replaces them, an
  older entry never regresses state, `bind` changes only the identity, and a verified status
  replaces the identity only if it is newer than `ident_ts` (the last proof). Don't write the
  snapshot with a bare HSET — Codex's app-server env carries another session's `HERDR_PANE_ID`,
  and a blind write would erase the bound pane. One runtime per agent name.
- **Every dispatcher write is fenced.** Wake records, escalation/notify XADDs, deletes and group
  creation go through Lua that first checks `GET {p}:dispatch == token`, so a dispatcher that lost
  its lease — or whose project was deleted — writes nothing and recreates no key. Persisted times
  come from Redis (`TIME`, stream ids), never the dispatcher's clock. Don't add an unfenced
  dispatcher write. Accepted residual: `busmon --delete` (SCAN then DEL) can race one fenced write
  into an orphan key.
- **The `subscribe` JSON output is versioned; nothing else is.** Every `subscribe` event carries
  `"v"` (= `bus.ProtocolVersion`, currently 1). The rule: within a `v`, changes are **additive only**
  and consumers **must ignore unknown fields**; **bump `v` only on a breaking change** to the
  subscribe contract (a field removed/renamed/repurposed or a semantics change). A consumer that
  sees a higher `v` than it knows should **fail loud**, not best-effort — that is what turns a format
  cutover (the old sentinel→JSON break) into an explicit signal. `agentbus version` prints the
  constant. Stream entries and `agents`/`usage`/`verdicts` output are **not** versioned (they are
  additive-by-key via `ParseEntry`/`json.Unmarshal`).
- **`report` keeps a bounded preview AND the full text.** The write path stores `message` (the
  ≤500 one-line `SanitizeReportMessage` preview, for `listen`/busmon) and — only when it carries
  more — a `full` field (`SanitizeReportFull`: newlines/tabs kept, ≤8000). Don't raise the preview
  cap to "see more" — that floods the flat viewers; use `agentbus reports <id>` (or the busmon
  ` (+N)` marker) instead. The `full` field lives in the entry and is trimmed with the ~1000-cap
  report stream — `report` is an activity channel, not the durable audit (that's the verdict ledger).
