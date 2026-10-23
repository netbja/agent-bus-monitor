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
`specs/2026-10-10-event-driven-dispatch-design.md`.

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
gates and cmd. Spec: `specs/2026-10-11-missions-design.md`.

**`plan/`** (v0.22) is the batch's plan as the person reads it: `docs/plans/<name>.json` (strict,
`Parse`), published at a commit by `agentbus plan load` into `{p}:plan` (the file kept opaque:
cjson would rewrite `[]`) plus master's facts in `{p}:plan:facts` (`bus/plan.go`: PR, done, note,
Redis `TIME`; one script each, a task not in the plan refused); neither key is in `projectKinds`,
and a load is refused for a project with no such key. `Derive` (`derive.go`) is pure: a board
request `done` is *delivered*, so **in review**; done needs a recorded `plan done` (shown as a
dated claim) or `merge:false` + delivered + approved; the review is `bus.RollUp` over the task's
subject (default its board task) plus `pr:<n>`; asks match only by `AskDef.Task`; a never-read
input reads `?`. busmon reads the plan every `requestPoll` tick through the optional
`planReader` (the ledger incrementally, `VerdictsAfter` from the last seen id), derives in
`pollOnce`, and shows the header item, the `P` overlay and a recap section. Spec:
`docs/superpowers/specs/2026-10-22-plan-view-design.md`.

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
| `{p}:notify`        | `from message`; optional `for=person` (a dispatcher notice only the person can act on: busmon marks it unread) | busmon ACTIVITY          |
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
`{p}:pause` (v0.12.5, a JSON string: until in Unix ms from Redis `TIME`, reason, by, notify; absent = not
paused; in `projectKinds`), `{p}:services` (v0.12, Redis hash: the `installed` marker `agentbus up` writes, plus one JSON state per
service kind — state, detail, `at` from Redis `TIME`, pid, host, last error, log path; in `projectKinds`).
`AgentSnapshot` also carries `pid`/`pid_start`/`terminal`, recorded by `agentbus status` inside
herdr — the identity the dispatcher checks before typing into a pane — or by `agentbus bind`
(v0.10.1, `Bus.Bind` in `bus/ident.go`) for a client whose commands do not descend from its pane
(Codex's shared app-server). cmd `type` gains
`escalation` (dispatcher → `--escalate-to`, default `master`; for the services, `agentbus up
--escalate-to` records it as `AGENT_BUS_ESCALATE_TO` in the project's env file), `blocked` (written by `request
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
  `verdict`/`verdicts`/`pilot`/`gate`/`agents`/`bind`/`usage`/`budget`/`board`/`plan`/`shutdown`/`refresh`/`request`/`dispatch`/`missions`/`mission`/`mem`/`roles`/`up`/`down`/`service`/`subscribe`/`watch`/`listen`/`version`. Parses args manually;
  trailing words are joined. Every command's usage, flags and examples are one table
  (`cmd/agentbus/commands.go`, types in `cli/`): `agentbus help [<cmd>]` and `<cmd> --help`
  render it above the project-required exit, and `expandArgs` rewrites the table's short flags
  (`-p` project and `-H` host before the command only, the command's own shorts after it) to the
  long forms the parsers read; `globalArgs` never reads `--project`/`--host` from the message of a
  `Text` command (`notify hi --project x` stays in the current project); internal
  entry points (`plugin`, `statusline`, `service run`, the scripts' `roles` verbs) are never
  expanded. `TestEveryDispatchedCommandIsInTheTable` walks `main()`'s AST, so a new command
  without a table entry fails the suite, and `TestEveryFlagLiteralIsInTheTable` does the same
  for a new `--flag`. busmon's flags are `cmd/busmon/flags.go` (`busmonFlags`, long + short,
  `-h` rendered from it). `subscribe [--since <cursor>] <agent> [idle_secs]` is a one-shot
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
  `specs/2026-10-10-busmon-ui-redesign-design.md`). On launch it backfills only
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
  `detail.go` + `detail_agent.go` (overlays), `compose.go` (v0.16: the compose window, a `tview.TextArea` with a rune limit of its own — `SetMaxLength` counts bytes — used for a message and for a free answer; `ui.send` is the one send path of the line and the window, which passes recipient and body as fields, never an `@name body` line), `keys.go` (one key table per mode drives the handler,
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
  hostile fixture and checks every frame's corners and borders. Each panel has its colour
  (`panelHue`, v0.14.5: Agents accent, Activity ok, Board dispatch, Requests attention): its frame
  is that colour when focused and faded towards the background when not (`Theme.FrameBorder`), and
  its title's name is a pill when focused (`frameTitle`, `Seg.Pill`, `Theme.Pill`) without changing
  the title's width, which the tab click spans rely on. The tick reads every second
  (pilot, armed, lag, agents, usage, budgets, board, challenges) and every 5th tick the dispatcher
  facts (`DispatchAlive`, `Obligation`, `Wake`); a failed read keeps the last value (queue shows
  `N?`). The usage scopes stay apart: account budget (used %) in the header, an agent's own context
  (raw tokens) in its row. The declared state is never overwritten by silence or by a report.
  Since v0.12.1 an agent silent for over a day (`dormantAfter`), not armed, not the pilot and owed
  nothing is **hidden** from the panel and from `@all` (`a` shows it), never relabelled; `d`/`Delete`
  then `y` forgets an agent (`Bus.ForgetAgent`, `agentbus agents forget`, refused while it listens
  or declared within 10 min) or closes an untracked thread with a `human=yes`, `wake=no` reply
  (`Bus.HumanClose`). The Requests count covers only threads younger than `bus.DefaultHorizon`.
  Activity auto-scroll (v0.12.3): selecting, clicking or opening a line keeps it live; only `f`
  (`ListView.Pause`) and a wheel-up leave it.
  Attention marks (spec `docs/superpowers/specs/2026-10-21-busmon-ux-design.md`): an idle agent whose
  status message names the person (`forYouHint`, `foryou.go`; never for the default `pilot`) gets
  the flag `for you`: a hint, it never relabels, rings or counts. Activity entries for the person
  (an ask, a cmd to the person's name, a notify with `for=person`, an agent's newest report while it
  waits for the person) carry a mark column before the time (`unread.go`: ● unread, ○ superseded by a
  newer one from the same sender, an answer or a closed ask); opening the line reads it, saved in the
  local cache (`read`, only ids still retained), never on the bus; nothing older than `last_visit`
  is marked; the header counts `● N unread`, `u` jumps to the next, `R` lists them first. The line
  and the compose window share one `@` completion (`recipientCompletions`, `completionNames`: the
  @all targets, plus dormant agents while `a` is on); in the compose window Tab on a text that is
  only `@prefix` makes it the recipient, but the text is never read as a recipient at send time.

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

There is also a parallel notification path that is **not** on this bus: a `project-c` `Stop`
hook POSTs (via `hermes-notify`) to a hermes gateway on host1 (`:8644`) that delivers to Signal.
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
  Specialist roles (spec `docs/superpowers/specs/2026-10-21-specialist-roles-design.md`):
  `roles.toml` is schema 2 since then (profiles; `rolecfg/migration_test.go` keeps every launch
  equal to the last schema-1 manifest, `rolecfg/testdata/roles.v1.toml`);
  `examples/roles.specialists.toml` holds six pop roles with prompts in `roles/`. Verbs:
  `roles reviewers` (models of other providers than coder's that can review; never the author's
  own maker served elsewhere), `roles skill-users <skill>...` (whom to tell when skills change),
  `roles draft <role> --task …` (an inert draft in `.agent-bus/drafts`, skills from the launcher's
  roots, least privileged) and `roles adopt <role> --ask <id>` (needs the project's bus, like
  `doctor`: only after an `adopt` answer not given by the asker; appends to the manifest after
  checking it loads, never overwrites a prompt).
- **`setup.sh` is the installer; tests of it must stub `docker` and `herdr`.** `install`,
  `update`, `uninstall`, `status`. It stamps `buildinfo.Version` (from
  `git describe` when it is a `v*` tag, else `dev`) and `buildinfo.Repo` via ldflags; `agentbus up`
  finds `scripts/bootstrap` through `AGENTBUS_REPO`, then `buildinfo.Repo`, then the binary's
  directory. A copy of the repository uses the same compose project and container name as the live
  broker, so an unstubbed `uninstall --yes` in a test stops the production Redis.
  `tests/installer_test.sh` puts fakes first on `PATH`; keep it that way. Since v0.20 setup
  links *this checkout* as the herdr plugin `netbja.agentbus` (`herdr plugin link`, the plugin
  root is the checkout, no download, no `[[build]]`); herdr-plus is no longer installed, and a
  herdr-plus an older setup installed is reported by `status`, never removed. setup owns only
  what it wrote: it unlinks only the registration it recorded, and replaces only its own,
  unedited key-binding block.
- **Templates live in agent-bus's own directory (v0.20).** `rolecfg.ProjectsDir`:
  `AGENTBUS_PROJECTS_DIR`, else `$XDG_CONFIG_HOME/agentbus/projects`, else
  `~/.config/agentbus/projects`; bash reaches it through `agentbus roles projects-dir` (handled
  before the manifest is loaded, so it needs no `roles.toml`). There is deliberately **no migration
  code** and no fallback to herdr-plus's directory: old templates are moved by hand, once.
  `tests/lib.sh` points `AGENTBUS_PROJECTS_DIR` at a temp dir, so a suite that forgets it never
  writes the real `~/.config`. `up` takes the herdr session from `--session`, `HERDR_SESSION`, then
  `HERDR_SOCKET_PATH` (`…/sessions/<name>/herdr.sock`, or `…/herdr/herdr.sock` for `default`), and refuses services when a socket path
  names none.
- **The herdr plugin (v0.20): `plugin` and `--pick` sit above the project-required exit.**
  `agentbus plugin projects|busmon|busmon-pane` and `busmon --pick` need no project (they answer
  "which project?"), so their blocks in `main()` stay above that exit, and `--pick` above
  `--reset`/`--delete` too. The picker (`projlist/`: templates in the projects directory x the
  broker's `bus.Projects`, state present/absent/`?`) writes nothing to Redis and **never
  provisions**: it only runs `agentbus up <p>` for a project the broker confirms; an absent one is
  `agentbus up <p> <path>` by hand. Calls to herdr go through `herdrBin`; the plugin's panes get
  `HERDR_SESSION`/`HERDR_SOCKET_PATH`/`HERDR_BIN_PATH` from herdr and the cwd is the plugin root
  (`./agentbus`, `./busmon`). Decision:
  `docs/memory/decisions/2026-10-19-herdr-plugin.md`.
- **Relaunch (v0.21) never types into a pane that already exists.** Whether a shell waits at its
  prompt cannot be proven from outside: a builtin `read` keeps the shell's argv, and `read -e` or
  an earlier `stty -icanon` even leave the terminal non-canonical (Codex's reviews, real PTYs).
  So `agentbus up <p>` on an open workspace (`relaunch.go`: `decideRelaunch`, a pure table) runs a
  dead agent's template command in a **new pane split from its tab** (`splitPane`), or in a new tab
  when the tab is gone, and leaves the old pane as it is. A tab's command counts as gone only when
  no live snapshot says otherwise (`snapLife`: pid + `pid_start`, Linux only), herdr sees no agent in
  any of its panes, and **every** pane of the tab is a plain interactive shell (`idleShell`, argv
  only: a classification, never a licence to type); anything running in any pane (maybe the agent
  relaunched before, under a name herdr does not detect) makes it `skipped (busy)`. Duplicate tab labels are
  `unknown`. The invariant test is `typedIntoExisting`. `upLock` serialises ups per project and
  fails closed. The workspace hook (`agentbus plugin workspace-event`, herdr's
  `workspace.created`/`workspace.focused`, the only `[[events]]` the manifest test allows) touches
  no pane: it only starts stopped services with `Manager.Start`, never `startServices`/`Install`,
  and never provisions (template + broker `present`). After a reboot, agents are herdr's native
  session restore's job (`pane report-agent … -- <resume argv>`, which runs in a shell herdr has
  just spawned); making the launcher report its own resume command is the next step (open: the
  Claude Code integration `herdr:claude` already holds those panes and reports a raw
  `claude --resume`, which bypasses agent-launch). Units always run `buildinfo.Bin` (setup's
  `$BIN_DIR/agentbus`) when it exists. `agentbus projects` and `agentbus identity` sit above the
  project-required exit. `missions` without a Git repository publishes `waiting-git` and waits.
  **Trust gate** (`trust.go`, specialist roles spec part B): when `up` opens a workspace whose
  folder a client (Claude Code, Codex) does not trust yet, it starts the first agent of that client
  alone, says to answer the client's own dialog in its pane, polls the trust (2 s, 10 min) and only
  then starts that client's other agents; a timeout starts nothing more and keeps the workspace
  (`errTrustPending`). The readers (`claudeTrusted`, `codexTrusted`) read only the trust flag, keyed
  as Claude Code documents (`trustKey`: git root, a worktree's main checkout, else the folder) and
  return a bool, never file content (`~/.claude.json` can hold an API key). agentbus never writes
  those files. Nil `upDeps.Trusted` = no gate.
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
  `env_refs` names it; elevated options are announced on stderr. v0.17 (`rolecfg/toolrules.go`):
  a role with `pops = true` gets `Bash(agent-spawn <role> *)` per other role (allow, or ask when
  that role has `approval = "person"`) plus its own `tool_rules`, in one `--settings` JSON for
  Claude Code only (other clients: a note); free-form rules naming `agent-spawn` or allowing all
  of Bash are refused. The ask matches command text only (a quoted role reaches the classifier):
  a convenience, not a lock. A missing skill warns and the
  agent still starts; `roles install-skills` stays strict. Top-level `[tool_rules]` rules are
  merged into every role before its own rules; the shipped Claude Code Read/Edit denies cover
  common secret-file names even with `bypassPermissions`. This is defense in depth, not a boundary:
  processes an agent starts (tests, dotenv, and similar tools) can still read environment files.
  `setup.sh install/update` also links `agent-launch` and `agent-spawn` into `BIN_DIR`; those links
  resolve back to this repository's scripts, and uninstall removes only links to these exact files.
  Resume Claude Code with `agent-launch <role> <project> --resume <session-id>` to retain role
  settings and identity; never use `claude --resume` by hand. Other clients currently refuse resume.
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
- **`ValidStates`** (`working`, `idle`, `blocked`, `waiting`, `done`) is still a hardcoded map in
  `bus.go`. Adding a state means editing that map. `waiting` (v0.12.3) means a *person* must decide:
  busmon sorts it first with ♙, fades the word `waiting` in and out (3 s, `fadeLevel`, ~10 redraws a second only while one waits; true colour only), counts it in the header and rings the bell once per new
  declaration (`ui.onTick`, on the event loop after the poll, never in a draw).
- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD` (defaults `localhost`/`6380`/`AgentBus2025!`).
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` sender identity defaults to `hermes`,
  overridable via `AGENT_BUS_AGENT`.
- **`docker-compose.yml` binds `6380` to loopback** (`127.0.0.1:6380:6379`) so the bus is not
  LAN-reachable (the password travels in plaintext); the SSH tunnel is the only remote path. Don't
  revert to a bare `6380:6379` (= `0.0.0.0`) without a reason.
- **Attention and recap (v0.18)** — busmon derives answered `waiting` declarations only from a
  later addressed `human=yes` command or a reply in the agent's thread; open asks remain separate
  attention items. Due reminders wait until drafts and other modals clear, then group oldest-first
  and snooze locally. `R` opens a recap built from current snapshots and retained events; capped
  history boundaries and failed reads are labelled instead of treated as empty. The optional
  summary directive requires an explicit `y` confirmation and uses the normal `ui.send` path.
  Global settings are in `~/.config/agentbus/busmon.toml` (or `$XDG_CONFIG_HOME/agentbus/`), and
  local last-visit/snooze data is atomically cached under `~/.cache/agentbus/` with mode `0600`.
- **Project health (v0.19)** — the dispatcher's separate bounded health loop writes
  `{p}:health`, transition-only `{p}:health:log`, and `{p}:health:notes` through fenced scripts;
  Redis `TIME` owns persisted timestamps. Health keys do not discover a project or make
  `HasKeysBesidesDispatch` true. Missing/stale evidence is `unknown`, never `ok`; remediation text
  is static guidance. A new `fail` episode wakes sentinel once, plus one separate daily directive.
  Sentinel may restart an identified failed project service once within its budget, or refresh the
  local model table; everything else is escalated. Usage refresh is at most every 15 minutes with
  a fenced writer; model refresh is daily under a host-local lock. busmon shows the health pill, F5
  details and retained transitions in its R recap. Runaway is warning-only. Scanner, canary, key
  inventory, project-defined checks and password rotation belong to later releases; health is not
  a security boundary.
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
  wake text and the skills pointing there, never at a plain `subscribe`. One exception (v0.14.2,
  `Bus.WatchCmdDeliveryFromNow`, `keptForPerson`): a person's waking entry to that agent
  (`human=yes`, not `wake=no`, retained, within `DefaultHorizon`) is delivered, so an answer that
  arrives while the agent re-arms without its cursor is never lost. An explicit `--since` keeps
  the strict floor.
- **An unverified `status` never overwrites a verified identity.** Every `{p}:agents` write goes
  through `putSnapshot` (`bus/ident.go`, one WATCH/MULTI transaction, `mergeSnapshot`): a status
  with no pid keeps the stored pane/pid/pid_start/terminal, a verified one replaces them, an
  older entry never regresses state, `bind` changes only the identity, and a verified status
  replaces the identity only if it is newer than `ident_ts` (the last proof). Don't write the
  snapshot with a bare HSET — Codex's app-server env carries another session's `HERDR_PANE_ID`,
  and a blind write would erase the bound pane. One runtime per agent name.
- **Quotas (v0.13): first-party readers behind one contract, policy in the dispatcher.**
  Package `quota/`: `Reading` (provider, windows: used %, reset, minutes; observed_at vs
  collected_at; unknown/unlimited) and readers — Claude via `agentbus statusline` (Claude Code's
  `statusLine` stdin `rate_limits` merged into `~/.cache/agentbus/quota-claude.json`, plus a
  transcript backstop for a live rejection), Codex via `codex app-server` `account/rateLimits/read`
  (session logs as fallback), declarative file readers (Grok's billing log line is one, built in),
  an external command, unlimited; configured in `~/.config/agentbus/quota.toml`. No reader opens a
  credential. The dispatch service (`serviceQuota` → `dispatch.QuotaConfig`) collects every minute,
  publishes `{p}:budget` (`windows` etc., additive) and acts in `dispatch/quota.go`: warn once per
  window and reset, hold a provider's agents (`{p}:quota` marks; no prompt, no escalation), pause
  for the coordinator's provider (`FencedQuotaPause`, never replacing another pause). Agents carry
  `client`/`provider` from the launcher's env. Unknown or aged (>30 min) readings never act; every
  write is fenced. A hold ends only when the window that set it is read under the threshold, renewed or past its reset (v0.15.3: an aged reading of it is not set aside for another, lower window). A limit hit read from a transcript is void once the API has answered since
  (v0.14.6: `servedAt`, `Reading.AvailableAt`): with no window to read, a recent answer ends the
  hold and the coordinator's quota pause at once (a reset granted early), and `FencedQuotaUnpause`
  sends master the resume directive like `FencedResume`. v0.15: when a hold ends (released,
  served again or reset passed), the provider's agents with an `accepted` board task or a declared
  `working` get a resume directive, and the escalation target one `wake=no` notice, each once per
  episode (`FencedCmdOnce`/`FencedNotifyOnce`, `bus/once.go`: entry and `resumed:<p>:<agent>` mark
  in one script; the episode is the hold's reset; written before the hold mark is deleted). The
  dispatcher's own quota pause is reconciled even when nobody uses its provider any more: if the
  coordinator now runs on a free provider it ends (`FencedQuotaUnpauseWhy`), on a held one it is
  replaced (`FencedQuotaRepause`, exact record, no resume). A hold whose reset is more than
  `LongHold` away (2 h, `AGENT_BUS_QUOTA_LONG_HOLD`, `0` = off) sends one message per episode
  (`longhold:<p>`): a directive to master naming the held agents with work in flight (master asks
  the person, then `agentbus swap`), or, for master's own provider, a `notify` for the person
  (busmon's pause segment adds "long pause, see Activity"). **`agentbus swap <role> --profile
  <p> [--now] [--force]`** (`cmd/agentbus/swap.go`) replaces an agent by its role on one of its
  `fallback_profiles` (`rolecfg`: schema 2, defined, not its own profile, another provider, at
  most 3; `agentbus roles fallbacks <role>`): everything is checked before anything is closed
  (profile, client on PATH, live verified identity, not its own pane, no open challenge on either
  side — `Bus.ReviewsOf` scans every gate — no mission attempt in progress, no running turn
  without `--now`), then the swap intent `{p}:swap:<role>` (`SET NX PX 180000`; the dispatcher
  neither prompts nor escalates nor resumes that agent), `herdr pane close`, the old pid gone,
  `agent-spawn <role> <p> --profile <p>` with `AGENT_LAUNCH_REQUIRE_BIND=1` (a failed bind is
  fatal), the proof (new pid alive, new pane, the profile's client and provider), and only then
  the resume of its work (`Bus.CmdOnce` on the episode's `resumed:` mark when its provider is
  held). A failure after the close prints the recovery (`agent-spawn <role> <project>`). busmon
  flags `swapping`. v0.15.1 (`bus/swaplog.go`): every swap is in the ledger `{p}:swaps` (`begin` in
  the same script as the intent, `done`/`back` with the fallback state `{p}:fallback` at the proof,
  `failed`/`warning` on every failure path, a `begin` without end and without intent read as
  interrupted and closed by the next swap); `swap --back` returns a role to its own profile and
  model (refused only when profile, client, provider and launched model all match); `agentbus
  retire <role>` closes one role's tab with the same checks (not `shutdown`, which is team-wide);
  `agentbus swaps` lists the ledger. A fallback record keeps `held_episode`, and the release of that
  episode earns master one `back:<p>:<role>` notice; its own provider stays watched. The launched
  model (`AGENT_BUS_MODEL`) is part of the verified identity (`AgentSnapshot.Model`, set only with a
  proof). `agentbus roles doctor [--publish]` compares configured (roles.toml, the current profile),
  launched (bind, else the live process's `--model`) and used (Claude transcript, `usage.ReadUsedModel`)
  models; `--publish` leaves verdicts in `{p}:doctor` for busmon's `model≠` flag (only for the judged
  pid). Claude aliases (`opus`, `sonnet`, `haiku`) are the default in roles.toml; an alias launched as
  an id of its family is never drift. busmon flags `fallback <profile>`. v0.15.2, package `models/`:
  the machine's model table (`~/.cache/agentbus/models.json`, `agentbus models [--refresh]`, no project,
  no broker) read from each client's own free catalogue (Codex `$CODEX_HOME/models_cache.json` with
  its priority, visibility and `upgrade`; Claude Code's aliases plus the model keys of `.claude.json`,
  a *partial* catalogue, so an id missing from it is never `gone`; `grok models`; Kimi's
  `config.toml`; `agy models`, whose other-maker rows carry `family`). No reader opens a credential.
  Tiers (frontier, workhorse, fast, legacy) come from the sources' own words, else
  `~/.config/agentbus/models.toml` rules; a failed read keeps the last good rows, and only a
  successful read under 7 days judges. A schema-2 profile may set `tier` instead of `model`
  (never both): the launch resolves it (`rolecfg.ResolveTier`: the source's default row of that tier,
  else the first in source order) and exports `AGENT_BUS_MODEL_TIER`. The doctor adds `gone` and
  `newer` (only on stated evidence: `upgrade`, generation words, a models.toml family order).
  setup.sh refreshes the table at the end of an install (`AGENTBUS_SETUP_NO_MODELS=1` skips it; the
  installer tests set it); the sentinel refreshes it daily. busmon shows a gauge per provider and `held`. `refresh` no longer reads ccstatusline
  nor writes `{p}:budget` (the service replaces that hash as a whole).
- **Asks (v0.14): a person's decisions are structured questions, not report text.** `bus/asks.go`:
  `{p}:asks`, one flat hash (`<id>` definition, `<id>|q|s|a|<qid>|r|c|e`, `open:<agent>`), every write
  one Lua script (`askCreate`, `askAnswer`, `askWithdraw`); the id is the stream id of the `ask` cmd
  (`target=people`, `wake=no`, no `ref`; `people` is never a cmd target or consumer group). First
  answer wins; an answer to a replaced ask follows `replaced_by` only for an unchanged question; one
  open ask per agent; each answer is a `reply` (ref = ask id, `human=yes`) that wakes the agent. Client
  JSON never carries answers. v0.15.1: `ask <agent> --withdraw <id> --question <qid> --note …` closes one
  question (`AskAnswer.Withdrawn`), the ask ends with the last one; a directive typed to an agent with an
  open ask is held once with a hint (Enter sends, Tab opens its panel prefilled). busmon: `askpanel.go` (ModeAsk, `!`/`w`/F4, Enter in the agent detail), counts and fade follow open
  questions (`waitingQuestions`), bell per ask id; `markdown.go` renders long texts (`renderMarkdown`,
  overlay bodies only; rows stay on `Preview`).
- **Observed is not declared (v0.14.3).** The services' dispatcher inspects every verified pane
  every `ObserveEvery` (15 s; `Config.Observe`, 0 = off, so the default config and its tests
  inspect nothing) and replaces `{p}:observed` (`bus/observed.go`, fenced, whole hash, `PEXPIRE`
  120 s), after the pause check (a paused project is not observed), within a 5 s budget per pass (inspections and time reads; the final write has its own 2 s bound),
  the interval counted from the pass's end. It only reads panes: no typing, no state change; a pane
  failing `identityOK` is not observed. `{p}:observed` is not in `projectKinds` and
  `HasKeysBesidesDispatch` ignores it, so a write racing `busmon --delete` never revives a project. busmon reads it with the dispatcher facts and adds the flag `busy` (and an `Observed`
  detail row) when the pane runs a turn but the agent does not declare `working`. Never let it
  rewrite or replace the declared state. Client updates (spec
  `docs/superpowers/specs/2026-10-21-client-reload-design.md`): the launcher fingerprints the client
  it resolved (`clientfp`, `AGENT_BUS_CLIENT_FP`, kept in the verified identity as `client_fp`); the
  observe pass re-checks it at most once a minute (or reads the Claude Code `versions/` layout for
  agents without one) and adds `update` and `client_judged` to `{p}:observed`; sentinel gets one
  directive per update episode (`FencedCmdOnce`, mark `update:<client>`, episode the installed
  hash); health `client-updates` warns; busmon flags `update`. Metadata only: the dispatcher never
  runs a client, and nothing restarts one: the person reloads (`/exit`, `agentbus up <p> --resume`).
  v0.15: an observation carries `since` (first reading of
  that state by the same pid, carried forward) and a failed inspection keeps the previous reading
  (`Observation.Fresh`, 120 s); a pane seen `blocked` (its client's own menu or permission prompt)
  reads `waiting` with the flag `prompt` (`promptWaiting`), one bell per `since`, unless the agent
  declares blocked/waiting or has an open ask. An escalation caused by the pane's state (rules
  7-8, `Decision.Cause` → wake `esc_cause`, or read from the reason of an older record) is
  re-armed once the pane is ready: the loop inspects such a record before `Decide`, tries clear
  and `at` stays. When the escalation target cannot act (it is the agent, its provider is held,
  its tab is blocked), the escalation is a `notify` for the person only (`recipientCannotAct`).
- **A project pause is a bus fact, not a sleep.** `agentbus pause --for|--until|--until-reset`
  writes `{p}:pause` (`bus/pause.go`); the dispatcher's `paused` check at the top of each pass
  wakes and escalates nobody while it runs, and once due ends it with `FencedResume` — one script
  that checks the lease, that the record is unchanged and that Redis `TIME` is past the deadline,
  then deletes it and XADDs the resume directive to `notify`. `agentbus resume` (`Bus.Resume`) ends
  it early. busmon shows it in the header and in the state column (v0.14.4, `stateLook`: an agent
  that declared working or idle reads `paused`, or `quota` when its provider is held; blocked,
  waiting and done are never covered; the detail keeps the declared state, the panel counts what
  is shown); `@all` targets only agents in `{p}:agents`
  (`agentState.registered`) and a human fan-out within a second reads as one `@all (N)` line
  (`sameFanOut`, display only: one entry per agent stays on the bus).
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
