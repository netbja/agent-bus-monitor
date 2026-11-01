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
`pollOnce`, and shows the header item, the `P` overlay, a recap section and the lower panel's fourth tab, Plan (A11: `planTabItems`, key 4, the
narrow tier's sixth view; Enter opens a task or expands done). Spec:
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
| `{p}:report`        | `agent kind message` (kind: note/auto); optional `full` (retained only when it differs from the ≤500 preview) | busmon ACTIVITY          |
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
  `bus.AgentIdent`. The agent never types it, so it cannot drift. The transcript is read only from
  the session proved with the agent's process (`IdentSession`, with a pid; usage-session), like
  `{p}:context`: an unverified status that names another session moves `Session`, never the file
  `{p}:usage` is read from. A peer with no proved session (a non-Claude-Code agent, an unverified
  one) is skipped with a note, never invented.
- **Context fill as a percentage comes from the client, never from a table** (decision
  `docs/memory/decisions/2026-10-27-context-fill.md`): Claude Code's statusLine stdin carries
  `context_window.used_percentage`; `agentbus statusline` records it per session in
  `~/.cache/agentbus/context/<session>.json` (pruned after 24 h), and the dispatcher publishes it
  each minute to `{p}:context` (agent → pct, observed_at, session; fenced, replaced whole,
  `PEXPIRE` 120 s, not in `projectKinds`), reading only `IdentSession`, the session recorded with
  the identity proof (an unverified status never moves it). No reading =
  unknown, never 0. At `AGENT_BUS_CONTEXT_ALERT` % (default 80, `0` = off; `dispatch/contextalert.go`)
  the agent gets one directive to commit WIP and hand off, the escalation target one `wake=no`
  notice, and the desktop one `notify-send` when it is on PATH: once per agent and session
  (`FencedCmdOnce`, marks `context:<agent>`/`context-notice:<agent>` in `{p}:quota`, deleted by a
  fresh reading below the threshold), never during a pause, never on an unknown or aged (>30 min)
  fill. busmon shows the fill in the ctx cell and the agent page; health `context` warns at the
  threshold. **Deliberately not reported:** cost (needs a price table that rots every
  release; on a subscription the figure is fiction). Raw context tokens stay in `{p}:usage`.
- **Tokens** (spec `docs/superpowers/specs/2026-10-28-tokens-dashboard-design.md`, package `tokens/`):
  the dispatch service records each agent's proved session (`IdentSession` with a pid, never
  `Session`) in `{p}:sessions` (session → agent, provider, client, first, last from Redis `TIME`; 8
  days, 512 fields), counts those sessions' Claude Code transcripts (and subagents) incrementally
  with `tokens.ClaudeScan` (one request once by its last `(message.id, requestId)`, offsets in
  memory only, 64 files and 128 MiB per pass, `$CLAUDE_CONFIG_DIR` or `~/.claude`, `AGENT_BUS_TOKENS=0`
  off) and replaces `{p}:tokens` (`day|agent|provider` → in/write/read/out/n, plus `_meta` partial and
  at) every 5 minutes; the quota pass appends `{p}:quota:hist` when a window changes or every 15
  minutes. All fenced; none in `projectKinds`, all ignored by `HasKeysBesidesDispatch`. Codex has no
  proved session: `tokens.CodexScan` (per-session baseline across files) serves only the person's
  `agentbus tokens --unattributed`. No cost, by rule. `agentbus tokens` (`tokenscmd.go`, above the
  project-required exit, read-only) prints every project side by side unless `--project` names one on
  the command line (the ambient `AGENT_BUS_PROJECT` never narrows it), `--by agent|day`, `--days`,
  `--json`; `--unattributed` is the person's host scan (1 GiB budget) of what no project's record names.
  busmon's `U` page (`tokens_view.go`, `ovTokens`; since busmon pages step 3 the list pattern: zones Accounts, Projects,
  Agents, bars against the zone's largest row, its top consumer bold, unknown agents folded by short reason and listed by
  `a`, the title `tokensTitle`'s summary): the account windows with a 24-hour sparkline from
  `{p}:quota:hist` (one cell per hour, its last sample), every project side by side (`ProjectNames`,
  `TokensOf`, read every `requestPoll` tick through the optional `tokensReader`; the open project first,
  7-day bars scaled across projects, `~` while partial, no key = one `no data` line), the open project by
  agent today (`tokens.AgentRows`, shared with the CLI: `?` with its reason, 0 only for a proved session).
  The block glyphs `▁`–`▆` joined the closed set for it. Its honest animation (step 5) rides the fade schedule
  (`animated`, true colour only, `fadeMsg` frames only while something animates): a skeleton row
  shimmers where the open project's numbers will be only while the page is open, the dispatcher is up,
  no reading exists yet and busmon started under `skeletonFor` (2 min) ago (`tokensSkeleton`; after,
  `?`); an agent's `●` pulses only when it declares `working` and its pane is seen busy, fresh (`pulsing`).
- `refresh` never fails hard — a missing source becomes a note, because it runs from the sentinel's
  cron wake where a hard exit loses the whole caretaker pass.

### Replacing the `ValidAgents` allowlist

The old hardcoded `ValidAgents` map is gone. Agent and project names are validated by `ValidName`,
a regex (`^[a-z][a-z0-9_-]{0,31}$`). Adding a new agent requires no code change.

### The two binaries (`cmd/agentbus`, `cmd/busmon`: a `main.go` plus small per-command files)

- **`agentbus`** — fire-and-forget CLI: `status`/`report`/`reports`/`notify`/`cmd`/`thread`/`challenge`/`reply`/
  `verdict`/`verdicts`/`pilot`/`gate`/`agents`/`bind`/`usage`/`budget`/`board`/`plan`/`shutdown`/`refresh`/`request`/`dispatch`/`missions`/`mission`/`mem`/`roles`/`up`/`down`/`service`/`subscribe`/`watch`/`listen`/`config`/`version`. Parses args manually;
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
  `listen` tails all four streams via `Bus.Tail` for debugging. `report` prints its entry id on
  stdout (lifecycle-2), the id `handoff <self> --report <id>` declares (`bus/handoff.go`:
  `{p}:handoff`, agent → report, session, pid, pid_start, `at` from Redis `TIME`, written in one
  script only for the agent's own report from its verified process in its proved session; not in
  `projectKinds`, ignored by `HasKeysBesidesDispatch`; any report and any non-idle status of the
  agent delete it in the same MULTI (`addVoidingHandoff`), so voiding never waits for a pass;
  `Handoff.VoidFor` — another process or session, or working since — is the rule the dispatcher's
  fenced `handoffPass` deletes by and busmon's `handoff` flag shows by; `handoff` alone lists them, read-only for the identity guard; the
  context alert's directive now asks for both). `report` now retains the full
  text (newlines/tabs kept, ≤8000, `AGENT_BUS_REPORT_FULL_MAX`) in a `full` field **only when it
  differs from the one-line ≤500 preview**; `reports [--json]` lists recent reports (marking those
  with more via ` (+N)`) and `reports <id>` prints one report's full retained text.
  `listen`/busmon still render the preview. `verdict` is subject-first
  (`--pr N`/`--subject S`), always appends to the `{p}:verdicts` ledger, publishes a live
  `CmdVerdict`, then closes gates best-effort (`verdictGates`, foryou-gates): on the *reviewer's own*
  gate, the one named by `--ref` plus the same-subject ones opened before it (anchored on the challenges'
  `:cmd` entries, so a verdict on an older commit never closes a newer delta's gate), or without `--ref`
  every one opened for that subject (`Bus.CloseReviewGates`; never
  a conductor's mission gate, which the conductor closes itself), else the author's gate named by
  `--ref`; a self-verdict closes no gate by either path (it no longer errors when no challenge is open — `verdictGateNotice`).
  `challenge --subject S` stores the subject in the gate's meta (`challenger|[subject S] text`,
  `bus.FormatGateMeta`/`ParseGateMeta`; no subject = the old `challenger|text`). `gate close <agent>
  <ref>` or a sweep (`--subject S`, `--mine`, `--by N`, `--dry-run`) closes stale gates without a
  verdict (`Bus.CloseGate`: compare-and-delete plus a `wake=no` reply on the gate's thread in one
  script; never a ledger entry): only the challenger, or a person by an explicit `--as`. `verdicts [--pr N|--subject S]` prints the roll-up 4-eyes
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
- **`busmon`** — a bubbletea v2 + lipgloss v2 TUI since the busmon Charm spec's step c (spec
  `docs/superpowers/specs/2026-10-26-busmon-charm-design.md`; btop-style since v0.9,
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
  subscribe's `"human": true`). The header shows `♙ Charlie [human · pilot]`; `@all` fans a
  directive out to every known agent; a plain-text notify needs a second Enter, because nobody
  reads one and nothing wakes for it.

  Files: `theme.go` (colour roles, palettes dark/light/16/none, the closed single-cell glyph set —
  the only place colours and glyphs are named; its colours are tcell values, tcell's one remaining
  use), `cells.go` (preview/detail sanitising, grapheme width, `Fit`/`FitLine`, `drawLine`,
  `drawFrame` with the scrollbar, on a `cellSink`), `tea_grid.go` (the `grid` sink and the lipgloss
  painter), `layout.go` + `tea_layout.go` (pure geometry: `shellLayoutFor`, three tiers wide ≥120 /
  medium 80–119 / narrow), `listview.go` (`listState`, the one selectable scrolling list: For you,
  Agents, Activity, Board, Requests and the picker are all instances; selection and wheel anchor
  follow keys, tail mode for Activity), `agents.go` (attention sort, flags, ctx-token parser),
  `activity.go` (event → row, pause cursor; rows kept in `activityCache`, keyed by every input a row reads, so a
  message that changes no line lays out none: the person's Backspace lag, 30-40 ms a message with a full feed), `board.go`, `header.go`, `detail.go` +
  `detail_agent.go` (page bodies; their `Label:  value` rows go through `fields.go`, busmon-detail-labels:
  label capitalised with a colon, a block's values in one column after its longest label, a long value
  wrapped under its value, never under the label; the plan task, board task, agent, message and
  service-log pages; a plan task waiting for the person has an `Action:` row under Owner, Enter or
  `s` opening the compose window to master prefilled, busmon-plan-start-ux: the footer advertises a
  binding marked `OnPage` only where `pageOffers` says the page offers it, under the word `pageHint` gives;
  on an agent's page `y` copies the status message and `Y` the whole report the page shows, `Y` offered only where a
  page has something for it), `keys.go` (one key table per mode drives the handler, the footer
  (its F-keys grouped last, ascending; on the main screen room is kept for F1 help and F5 health) and the help; `key.go`'s engine-neutral `Key`, `keyOfTea` its adapter), `poll.go` (`pollOnce`
  behind the `busReader` interface: the 1 s tick and its read inventory), `types.go` (`busWriter`,
  the page kinds, the Activity feed), `main.go` (flags, CLI paths, the tail goroutine feeding the
  program).

  **The Elm shell (`tea_*.go`) is the screen.** One `Model`; `Update` drives every change, `View`
  paints the model and does nothing else: no clock, no bus, no file (`snapshot`, `judgedAt`,
  `m.now`). `pollOnce` runs in a command rescheduled by `tea.Tick(1s)` and returns a `pollMsg`
  (`takeSnapshot` copies `shared` under its lock; the poller replaces maps whole, and the plan's
  ledger is copied); the tail goroutine `Program.Send`s `tailMsg`; every send, answer, thread read,
  removal, resume, recap read and cache write is a command. Rows and page bodies are `Line` values
  (role-tagged segments, never markup), measured in cells (`Width`; `Preview`/`DetailText` make
  every cluster measure the same under uniseg, lipgloss's grapheme width and wcwidth:
  `agreeWidths`); `drawLine`/`drawFrame` write the `grid`, lipgloss only styles spans, so a frame
  never wraps by one cell. `forYou` ranks asks, waits, the person's gates and unread lines, and `decisions` is
  its badge and reminder subset; with nothing for the person it says so, then what is in progress and the next
  decision (`forYouEmpty`: the agents declaring working, the plan's first open person task; no store), and the
  recap's For you section shows only when the For you panel is not on screen (`recapForScreen`); its keys are `ModeShell` (the panel table with the shell's tabs)
  and its clicks hit-test `shellLayout` (`tea_update.go`); Enter expands an Activity or For you line
  in place (`tea_expand.go`; A12: `▼` in the gutter, the text under the message column behind a
  muted `│` guide, a last muted `↵ full text` line), then opens it in the reader, a stack of pages over the lower zone, one
  per overlay, Esc back; on a page opened from Activity or For you, `n`/`p` (→/←) show the list's next or
  previous item in its place (A5, `readerStep`, `openTracked`) (`tea_reader.go`: bodies built on the update path with a stamp, an ask's
  options answered by digit or click, `ModeShellAsk`), and markdown is cached by id, width and
  palette (`tea_cache.go`), so a `fadeMsg` renders none. The input line (`tea_input.go`; its cursor and
  the filter's, busmon-input-cursor: `lineedit.go`, a pure editor on cluster boundaries, the cursor kept as
  `ui.tail`, the bytes after it, so a text set whole has it at the end; drawn reversed, kept in view with a
  quarter of the room after it) and the
  compose window (`tea_compose.go`, our own rune-capped text area: bubbles' textarea caps in cells; a
  selection from Shift with a move, `Key.Shift` on navigation keys only, removed by Backspace/Del, replaced by typing or a
  paste, ended by a plain move, a click or a first Esc; PageUp/PageDown a page of the window's rows)
  send through `send.go` (`planSend` decides, `exec` makes the bus calls in a command, `outcome`
  reads them; recipient and body are fields, never an `@name body` line); the pause popup
  (`drawPausePopup`), the reminder, the recap summary's confirmation and d/y are in
  `tea_dialogs.go`; bells (`tea.Raw`), the fade's `fadeMsg` schedule, the reminder and the recap's
  auto-open in `tea_signals.go`, after each poll or update. `--pick` runs `pickShell`
  (`tea_pick.go`) over `pick.go`'s rules and drawing (`pickFill`, `drawPick`, `upRunner`,
  `dialBroker`). The first-launch tour (`tour.go`, spec
  `docs/superpowers/specs/2026-10-28-busmon-onboarding-design.md`) is a modal over the shell with its
  own key table (`ModeTour`): it opens once per session on a calm update, only when every page it will
  show fits whole (`pageFits`; a cut page shown by F1 then `t` says so and writes no marker), never while
  `busyForModal` nor on the update a bell rang, when the machine's marker
  `~/.cache/agentbus/busmon/_tour.json` (no valid project name, so no project's cache collides with
  it) is behind `tourVersion`, on the pages changed since; any way out writes the marker; F1 then `t`
  reopens it. Its key cells are drawn from the key tables by (mode, action) (`tourKeys`), its header
  rows by the header's own segment functions: `TestTourKeysExist` and `TestTourMentionsNoBareKey`
  keep the prose from naming a key by hand. Goldens in `testdata/golden/` (`go test -run TestShellGoldens -update`), overlay
  body captures in `testdata/overlay/`. No literal outside the glyph set
  (`TestNoStrayNonASCIILiterals`); `TestShellAlignmentSuite` (with `TestShellAlignmentReading` and
  `TestPickShellAlignment`) draws every size × palette × hostile fixture and checks every frame's corners and borders. Each panel has its colour
  (`panelHue`, v0.14.5: Agents accent, Activity ok, Board dispatch, Requests attention): its frame
  is that colour when focused and faded towards the background when not (`Theme.FrameBorder`), and
  its title's name is a pill when focused (`frameTitle`, `Seg.Pill`, `Theme.Pill`) without changing
  the title's width, which the tab click spans rely on. The lower panel's tabs are herdr-style blocks
  (`tabBar`, busmon-tabs): each label is a block, padded by one blank cell on each side in its own colour
  (busmon-tab-pad), plus one blank column (`tabLabels`, the click spans, are those segments); the active tab a bold pill in its panel's colour, dimmed with the frame's fade when
  the panel is not focused; the others grey on `Theme.TabOff` (plain without true colour); `Seg.Tab`
  makes `frameTitle` leave them alone; `←`/`→` step them while one has the focus, wrapping as herdr's
  next/previous tab does (busmon-keys, `ActTabNext`/`ActTabPrev`, `Panels: lowerTabs`; a page keeps
  them for its previous/next item). Each provider has its own colour (busmon-llm-colors: `providerHues` in `theme.go`, one
  role per provider, `providerRole(p, fallback)`; the 16-colour palette takes a hand-picked ANSI name per
  provider (no two alike, none an alert role's colour nor the text roles' gray/silver/teal, openai the
  terminal's own foreground), no-colour none; an unknown provider keeps today's colour): the provider label
  of a header gauge (the fill keeps its 85/95 % roles), the model lines of an agent's page, and the U page's
  account windows and per-agent provider column. `TestEveryQuotaProviderHasItsColour`
  walks `quota.Builtins`, so a new reader without a colour fails the suite. The account gauges (busmon-bars,
  `bars.go`, spec `docs/superpowers/specs/2026-10-29-busmon-bars-design.md`: the header's quota and legacy
  budget bars, the U page's windows) are one function, `gaugeSegs`: in true colour eight steps per cell
  (`█` plus the left blocks `▏`–`▉`, in the glyph set) on a track (`Seg.Track`, the line colour as background),
  a non-zero value never empty and one under 100 never full; 16-colour and no-colour keep `▇`/`░` whole steps.
  A changed value slides there in 0.5 s (ease-out, `Model.bars` set in `applySnapshot`, keyed provider|window
  without the reset; View reads `barView{slides, m.now}`), its leading cell lifted towards the text colour
  (`Seg.Lift`), riding the fade schedule (`animated`); the figure jumps at once, and a first reading, `?`,
  16 colours, no colour and `fade = false` never slide. `appendSegment` joins only segments equal in every
  attribute. The tick reads every second
  (pilot, armed, lag, agents, usage, budgets, board, challenges) and every 5th tick the dispatcher
  facts (`DispatchAlive`, `Obligation`, `Wake`); a failed read keeps the last value (queue shows
  `N?`). The usage scopes stay apart: account budget (used %) in the header, an agent's own context
  (raw tokens, plus its fill % from `{p}:context` when known: `88k 85%`) in its row. The declared state is never overwritten by silence or by a report.
  Since v0.12.1 an agent silent for over a day (`dormantAfter`), not armed, not the pilot and owed
  nothing is **hidden** from the panel and from `@all` (`a` shows it), never relabelled; `d`/`Delete`
  then `y` forgets an agent (`Bus.ForgetAgent`, `agentbus agents forget`, refused while it listens
  or declared within 10 min) or closes an untracked thread with a `human=yes`, `wake=no` reply
  (`Bus.HumanClose`). The Requests tab answers who owes whom an answer (spec
  `docs/superpowers/specs/2026-10-28-requests-view-design.md`; `requests_view.go` pure, `requests_rows.go`
  rows): a thread is the unit, a row a group (section, sender, target, kind); sections owed by you, you wait on,
  tracked requests, reviews still gated, directives between agents, notices (report notices by
  `reportNotice`'s exact prefix, `from = dispatch`, `wake=no`), then answered or older than 7 days folded
  (`o`; `reqFoldAfter`, the person's answer, was 24 h). Since busmon pages step 2 it is the one list pattern: short
  section names, one line per group (`◆` owed or gated, `●`/`○` unread/read, `you` for the person, the kind short
  below 106 cells, `(+N)` for a group), the title a counter summary, one row per open ask (`ask:<id>`), the notices
  in one line (`ntc:all`: Enter shows their groups in place, each opened or closed by `d` as before; `d` on the line
  itself closes nothing), and the sentence a heading carried as the first line of the page a
  row opens (`reqSectionNote`, `page.lead`). A challenge is owed iff its gate is open now, never from its thread; unread/read come from the
  target's cursor (`Obligation.LastDelivered`, `PendingIDs`, read with the dispatcher facts); the title's
  numbers are the sums of their sections. The header reads the same view (`fillLists` keeps it in
  `shellLists.reqView`; busmon-req-header, the person's "orange = what is owed to you"): its orange `N requests
  waiting · oldest X` is the owed section's count (`owedText`, questions included, the oldest dated thread),
  and what agents owe each other a muted `N between agents` (no age, the first item to go for room), each
  absent at 0. Enter expands a group (or `+ N older`), opens a thread, a
  report notice's report, a request, or a notice group's list; `d`/`y` closes each thread of a group once.
  Activity auto-scroll (v0.12.3): selecting, clicking or opening a line keeps it live; only `f`
  (`listState.Pause`) and a wheel-up leave it, and (A3) moving the selection down onto the newest
  line or wheeling back to the bottom resumes it (`f`/End still do).
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
  The notice (busmon-notice, `notice.go`, spec `docs/superpowers/specs/2026-10-31-busmon-notice-design.md`):
  one quiet line at the footer's right end (`footerWithNotice`, the hints computed for the room left)
  for automatic acts seen while busmon runs, never at start (the first poll is the baseline, the tail's
  backfill and anything older than `liveFrom` never count): `{p}:pause` and `{p}:health` transitions
  between two polls (`noticeState.fromPoll`), the dispatcher's resume and `/clear` directives in the live
  tail (`tailNotice`, exact prefixes, `from = dispatch`), and the swap ledger read forward every fifth
  tick through the optional `swapLedgerReader` (`Bus.SwapEventsAfter`: XINFO STREAM then XRANGE
  `(cursor..last-generated-id`, paged 50; `swapCursor`: first read = baseline, entries-added grown by
  more than the events read = one attention line, a count gone down = a new baseline). Only an
  automatic author (`dispatch` or a registered agent, never the person). A queue of 5 plus a collapsed
  rest, 4 s alone and 2 s while others wait, held by a flash or a dialog (`noticeHeld`; one on screen stops
  ageing until it is released), a health recovery `ok again`, or after a fail with a sentinel note written since the fail began
  `sentinel: <note>` (the note, never a claimed restart: no record names the service), blended out
  over the last second in true colour on the fade schedule; no bell, no mark, no count.

### How the bus is actually consumed (lives outside this repo)

A Claude Code session subscribes by arming **`agentbus subscribe <agent> [idle_secs]`** as a
background task: it blocks on the project's `:cmd` stream, prints the first addressed entry, and
exits — and that exit re-invokes the session that armed it, which then re-arms. The whole loop is
self-contained in the `agentbus` binary (this repo); there is **no external wrapper script** and
**no daemon** in the subscribe path (the one process that runs alongside is `agentbus
dispatch`, which only types a wake line — see the dispatcher paragraph below). The old shell
wrapper over `agentbus watch` (and a persistent logger loop) are **superseded** — agents call
`agentbus subscribe` directly. Don't reintroduce a wrapper script: a restart loop just re-runs the watcher
and never wakes a terminal session.

A still-earlier in-repo prototype, `cmd/busbridge` (relay to herdr panes via `herdr pane
send-text/send-keys`, with hard-coded pane ids), was also **removed**. v0.8 revised that decision
on purpose (spec §11, `docs/memory/decisions/2026-10-10-event-driven-dispatch.md`): **the only
automated process that types into panes is `agentbus dispatch`, and it types a wake line, never a
message; master's manual pane injection remains the fallback while no dispatcher runs.** One named
exception (lifecycle-4, `docs/memory/decisions/2026-10-28-agent-lifecycle.md`): after an agent's
own declared hand-off, the literal `/clear` (`Runtime.Clear`, `ClearText`, no text parameter) under
`DecideClear`'s rule, a fenced once-per-session reservation (`clear:<agent>` in `{p}:quota`) and a
proof before the resume directive (`dispatch/clear.go`; a deferred clear records its reason in `ClearConfig.Decisions`
and logs `clear-deferred` once per change of reason, so a hand-off never waits silently); `AGENT_BUS_AUTO_CLEAR=1` turns it on, off
by default until the spec's live checks pass. The fake runtime enforces `dispatchTypes` in every
dispatch test: a prompt that is not a wake line, or a `/clear` without its reservation, panics. The wake
line is fixed — `[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus
--project <project> subscribe --drain <agent>` — and the agent then drains through Redis. The
dispatcher touches only unarmed agents with a real obligation, in a pane that is ready, unfocused
and pid-verified. Don't add a second pane writer, and don't make the dispatcher type message
content. Since v0.12 it runs, with the conductor, as a **user service** (systemd `--user`,
launchd), started by `agentbus up`; herdr tabs remain only as the explicit fallback
(`docs/memory/decisions/2026-10-14-project-services.md`).

There can also be a parallel notification path that is **not** on this bus: a Claude Code `Stop`
hook POSTing to a notification webhook of the person's own. It never touches Redis — see README
"Example deployment".

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
  checking it loads, never overwrites a prompt). `infra` is a pop role (Ansible, Terraform/OpenTofu,
  IT automation), adopted (#187) from the reviewed draft `examples/drafts/infra/` with `roles adopt infra
  --ask <id> --from examples/drafts`: manual profile, approval person; allowed only exact syntax/lint invocations, no wildcard
  (`terraform|tofu fmt -check [-recursive]`, `validate`, `tflint [--recursive]`, `ansible-lint
  --offline`, `yamllint .`); everything else prompts in the pane, and asked as well: what evaluates
  providers or data sources, touches a target or downloads (`plan`, `init`, `refresh`, `get`, `test`, `ansible-playbook --check`/`--diff`,
  ad-hoc `ansible`, `ansible-pull`, `ansible-galaxy`, `tflint --init`) and every change (apply,
  destroy, import, taint, state surgery, force-unlock, workspace) and every `ansible-playbook` run,
  one `terraform *<verb>*` pattern per tool
  so `-chdir=` forms match; denied: state, plan
  and tfvars files, vault passwords, cloud credentials, `output`/`show`/`console`/`state pull|show`,
  `env` (`rolecfg/infra_test.go`, a command table per class; it also holds the manifest to the
  draft's entry byte for byte and `roles/infra.md` to the draft's prompt). A role popped only through
  the person (deploy, infra) is an ask in master's rules; `TestShippedManifestRules` and the
  migration test derive that from `approval`.
- **`setup.sh` is the installer; tests of it must stub `docker` and `herdr`.** `install`,
  `update`, `uninstall`, `status`. Since installer-default it is a thin script that builds
  `agentbus` and runs `agentbus setup` (package `setup/`, every external command through
  `setup.Runner`); the bash installer stays one release as `scripts/setup-legacy.sh`, the fallback
  (`tests/installer_legacy_test.sh` runs the same suite on it), removed in the next, and run only
  when invoked by name (the thin `setup.sh` never execs it); `--force` only warns and is ignored, in
  both installers, and is dropped with the bash one. It stamps `buildinfo.Version` (from
  `git describe` when it is a `v*` tag, else `dev`) and `buildinfo.Repo` via ldflags; `agentbus up`
  finds `scripts/bootstrap` through `AGENTBUS_REPO`, then `buildinfo.Repo`, then the binary's
  directory. A copy of the repository uses the same compose project and container name as the live
  broker, so an unstubbed `uninstall --yes` in a test stops the production Redis.
  `tests/installer_test.sh` puts fakes first on `PATH`; keep it that way. Since v0.20 setup
  links *this checkout* as the herdr plugin `netbja.agentbus` (`herdr plugin link`, the plugin
  root is the checkout, no download; its one `[[build]]` exits 1 with how to install, so
  `herdr plugin install` from herdr's plugin list registers nothing, `TestPluginBuildRefusesInstall`,
  plugin-listing); herdr-plus is no longer installed, and a
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
  when the tab is gone. Once the relaunched agent is proven (its verified identity a new live
  pid bound in the new pane, read for up to 60 s), it closes the exact old pane the dead agent's
  snapshot recorded, if a re-read right before shows it still a plain shell; anything else leaves it
  and says why (`closeOldPanes`, decision `docs/memory/decisions/2026-10-27-relaunch-close.md`;
  master may run `agentbus up agent-bus-monitor --resume` without a prompt, `roles.toml`). A tab's command counts as gone only when
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
  just spawned). Since v0.21.1 every Claude Code launch carries, in its `--settings`, a
  `SessionStart` hook (`agentbus roles report-resume <agent-launch>`, `cmd/agentbus/reportresume.go`)
  that reports `env ROLES_TOML=… agent-launch <role> <p> [--profile <p>] --resume <id>` with
  `herdr pane report-agent-session --source agentbus --agent claude` from a detached sender (retried
  15 s while herdr answers `resume_not_accepted`, logged to `<state>/resume.log`): no state report, no
  authority, so `herdr:claude`'s raw `claude --resume` stays the fallback when herdr refuses. It also
  hands the new session to a detached `roles prove-session` with its ancestor chain read before it
  detaches (`dispatch.Ancestors`; lifecycle spec part D): `Bus.ProveSession` moves `IdentSession`
  only when the stored verified pid and start are in that chain and the hook's foreground stamp is newer
  than the stored `ident_session_at` (a new process resets it), so the session follows `/clear`
  (Linux only, like every verified identity: `/proc`; on macOS no agent is verified, so nothing is proved).
  The sender re-reads the stored identity and that its pid is alive with that start, but trusts the chain:
  a documented residual, since the bus is no boundary against the local user (as with `status`). The hook
  never prints on stdout and always exits 0 (spec
  `docs/superpowers/specs/2026-10-21-launcher-resume-design.md`). Units always run `buildinfo.Bin` (setup's
  `$BIN_DIR/agentbus`) when it exists. `agentbus projects` and `agentbus identity` sit above the
  project-required exit. `missions` without a Git repository publishes `waiting-git` and waits.
  **Trust gate** (`trust.go`, specialist roles spec part B): when `up` opens a workspace whose
  folder a client (Claude Code, Codex) does not trust yet, it starts the first agent of that client
  alone, says to answer the client's own dialog in its pane, polls the trust (2 s, 10 min) and only
  then starts that client's other agents; a timeout starts nothing more and keeps the workspace
  (`errTrustPending`); its message names the pane to answer in (A10). The readers (`claudeTrusted`, `codexTrusted`) read only the trust flag, keyed
  as Claude Code documents (`trustKey`: git root, a worktree's main checkout, else the folder) and
  return a bool, never file content (`~/.claude.json` can hold an API key). agentbus never writes
  those files. Nil `upDeps.Trusted` = no gate. **Agent CLIs** (installer-no-client): `rolecfg.Clients`
  is the launchable clients (manifest name, executable, name, official install page; in step with
  `builtinBinary`, `TestClientsMatchTheLauncher`); setup's step 5 scans them and, with none, warns
  with each install page (never a failure, nothing installed); `up`'s `checkClients`, right after the
  template is read and before any service or tab, warns about the agent tabs whose executable
  (`tabExecutable`, through the tab's manifest, split with `rolecfg.ShellSplit`) `lookAgentCLI` finds
  neither on PATH, nor in the usual per-user dirs, nor through the person's login shell
  (`shellProbe`: `$SHELL -lic 'command -v "$1"'`, fish's `$argv`, 3 s; only command -v's own non-zero status is
  `errCLIAbsent`, read from its own `AGENTBUS_PROBE_DONE <status>` marker so a shell failing at startup is not a miss; anything else `errCLIUnknown`, counted as found: fail open), and starts every tab
  anyway (master: a lookup alone never drops a tab; a CLI confirmed absent opens no trust gate); only with none of the team's found does it
  refuse (exit 1) with the pointers and `agentbus up <p>`. Nil `TabExe`/`LookPath` = no check.
- **Project services (v0.12): the OS manager supervises, nothing of ours does.** `service/`
  writes systemd `--user` units / launchd plists (never enabled at login), the 0600 env file the
  services load, the rotating log and the restart budget. Every unit runs `agentbus --project P
  service run <kind>` (`cmd/agentbus/servicerun.go`), whose exit code *is* the restart rule: 0 is
  final (stopped, project gone, another instance, invalid config, budget spent), 1 restarts. Its
  state goes to `{p}:services` through an `EXISTS`-guarded script (`bus/services.go`); without the
  `installed` marker that `up` writes, a service exits and writes nothing, so a deleted project is
  never resurrected. `up` writes that marker only once the broker answers PING (`waitBroker`,
  up-broker-ready: a 10 s deadline on the clock that counts the pings' own time, each ping through
  `bus.ConnectContext` given at most 2 s and never more than is left, 250 ms between them;
  retrying only LOADING and a refused or reset connection (or EOF): a timeout or another dial
  error fails at once, since a starting local container refuses rather than black-holes; a give-up
  cut short by the budget names the broker's last answer, not the deadline). Don't add a supervisor loop, a `nohup` fallback or a login autostart; don't
  put a secret in a unit; tests use fake `systemctl`/`launchctl` (`tests/installer_test.sh`,
  `service/manager_test.go`) and never the user's manager. busmon only reads the state and the
  local log (`F3`), never starts or parents a service. F3 (busmon-f3, `services_cards.go`) is three cards,
  side by side at ≥110 cells: dispatch and missions (state, pid and since from `/proc`, summary, last
  error, log size of `service.LogMax`, `LogKeep` kept) and Redis (where it runs: `docker inspect
  agent-bus-redis` only when docker is on PATH, else the process from INFO; version, uptime, memory,
  `DBSIZE` keys via `Bus.RedisInfo`), all gathered in a command (`gatherServices`, the records read
  fresh through `ServiceStates`), unknown read `?`; while the Services or Redis page is the top page it
  is read again every `svcRefreshEvery` (5 s, busmon-f3-live: each result schedules the next read, so
  reads never overlap, a page under another keeps its chain without reading, a closed one ends it); ↑↓ choose a service card, Enter or `l` opens its log tail
  (`ovServiceLog`, `serviceLogBody`); the Redis card is selectable too, and Enter opens its read-only page
  (`redis_page.go`, `ovRedis`, busmon-redis-detail: `Bus.RedisDetail` = INFO memory/persistence/clients and
  per project its keys and stream lengths, `docker port` for the loopback bind, `bus.LegacyPasswordInUse`,
  a `docker logs --tail 20` bounded to 8 KiB; unknown read `?`); `busmon --delete` removes the local
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
  A pop's client can stop on its own pre-session dialog (trust, hooks review) before it declares a
  status (A10): `agent-launch` binds the pane first, so the observe pass already inspects it, and a
  dialog herdr reports as `blocked` (Claude Code's trust check, `testdata/screens/claude-trust.txt`)
  gets busmon's `prompt` flag and the prompt-notify ring; `agent-spawn` waits `AGENT_SPAWN_WAIT` s
  (default 30, `0` = no wait; `swap` passes 0, it proves the agent itself) for the first status
  (`agentbus agents wait <agent> --since <ms> --timeout <d>`, read-only) and otherwise names the
  pane. A dialog herdr does not report (Codex's hooks review) is read from the screen: a
  `Signature.Dialog` match, in the last 14 non-blank lines and only when the client is not ready,
  makes the inspection `blocked`, never ready (`screens/codex-hooks.txt`,
  `claude-trust.txt`); only patterns pinned by a real screen.
  `setup.sh install/update` also links `agent-launch` and `agent-spawn` into `BIN_DIR`; those links
  resolve back to this repository's scripts, and uninstall removes only links to these exact files.
  Resume Claude Code with `agent-launch <role> <project> [--profile <p>] --resume <session-id>` to
  retain role settings and identity; never use `claude --resume` by hand. Every Claude launch passes
  `--settings` (the resume hook, plus tool rules when there are any). Other clients currently refuse resume.
- **Settings: one registry, `config/keys.go`** (config-file, spec
  `docs/superpowers/specs/2026-10-29-config-file-design.md`): every tunable's file key, env name,
  default, today's env rule (use, skip to the next source, or invalid) and typed file rule; per key, env
  over `config.toml` over the legacy `busmon.toml` (busmon's keys only) over the default; warnings name
  file, line and key and never echo a value; no secret and no connection in the file. Readers resolve
  through `config.Resolver`; `TestNoEnvLiteralOutsideTheRegistry` fails on a registry env name written
  as a literal elsewhere. busmon's settings are
  migrated (`loadBusmonSettings`), and so is the person's name (`personName` in `identity.go`: the files
  only, `config.toml` then `busmon.toml`; `identity set` writes `config.toml`; `ask` and the plugin keep
  their raw env rule through `config.EnvName`), and so are the services and writers (`config.Machine(getenv)`:
  env, that environment's `config.toml`, `busmon.toml`; `serviceQuota`/`Tokens`/`Clear`, `dispatch.PromptGrace`,
  `bus.ContextAlert`, `runawayThresholdsFromEnv`; the writers' caps per call through `bus.capFiles`, which
  reads no file in a test binary (`inTestBinary`, `testing.Testing`), so the person's own file never changes a test).
  `config.Path` reads `XDG_CONFIG_HOME`/`HOME` through
  the injected getenv only, absolute paths only, so a test's environment never reaches the real home;
  `config` does not import `bus` (bus will read its caps through it),
  so `ValidName` is repeated there, held equal by a test. `agentbus config`
  (`configcmd.go`, above the project-required exit, no broker) prints each value and its source;
  `--project` reads that project's env file for what its services see (only registry keys and the two
  per-project facts, never another line).
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
  declaration (`tea_signals.go`, after the poll, never in View).
- **Connection resolution order** (matches the legacy `agent_bus.py`): `REDIS_URL` wins if set;
  otherwise `REDIS_HOST`/`REDIS_PORT` (defaults `localhost`/`6380`) with the password from
  `REDIS_PASSWORD`, else the password file, else `AgentBus2025!` (redis-password, `bus/password.go`:
  `PasswordFile` = `$XDG_CONFIG_HOME/agentbus/redis.env`, else `~/.config/agentbus/redis.env`, one
  `REDIS_PASSWORD="…"` line, 0600 in 0700, written by a rename; a file that exists but cannot be read
  is an error, never the default, and no error quotes its content; `LegacyPasswordInUse` reads the
  same order). Compose reads the file through `env_file` (`required: false`) and gives redis-server
  its password as a config on stdin (a shell `REDIS_PASSWORD` at `compose up` wins, then the file,
  then the default; `"` and `\` escaped; the healthcheck uses `REDISCLI_AUTH`), never on an argv.
  `setup` writes it (`password` in `setup/redis.go`) only for a fresh broker: no file, none exported nor in the checkout's `.env` (compose would use it;
  status warns about it), no
  `agent-bus-redis` container and no `agent-bus_redis-data` volume, each read for sure. `./setup.sh
  rotate-redis-password` (`setup/rotate.go`, the person's act, never sentinel's) checks everything
  first (none exported, this checkout's broker running, no `REDIS_PASSWORD` in the checkout's `.env`
  nor in any project's services env file, the current one logs in), asks (or `--yes`;
  `--dry-run`), then `RotatePassword`: new file (old kept as `.prev`), `CONFIG SET requirepass`, a
  fresh login, else the broker set back and the file put back byte for byte (an error from CONFIG SET is checked against the broker first: a login with the new one means applied; if neither logs in, or the set-back fails, the
  new file stays, the old in `.prev`: the broker may hold either); then `docker compose up -d
  --force-recreate redis` from the checkout (foureyes: the container keeps the env_file it was created
  with, so a restart would come back on the old password), a fresh login waited for; then the running
  project services restart. Its rollback test starts private `redis-server` processes (CONFIG SET
  changes a whole server, never the shared test broker), opt-in through `AGENTBUS_TEST_REDIS_URL`.
  `scripts/setup-legacy.sh` neither writes nor rotates it; its help is compared to the Go one without
  `RotateHelp`.
  A non-empty `--host` overrides `REDIS_HOST` only. Note the **non-standard port 6380** (compose
  maps host `6380` → container `6379`). `agentbus` sender identity defaults to `anonymous`,
  overridable via `AGENT_BUS_AGENT`; inside a herdr pane (`HERDR_PANE_ID`) with `AGENT_BUS_AGENT`
  unset, a command that writes as the sender is refused with the fix (A8, `identityGuard`:
  relaunch through the launcher or export it; read-only commands still run, an unknown command
  counts as a write).
- **`docker-compose.yml` binds `6380` to loopback** (`127.0.0.1:6380:6379`) so the bus is not
  LAN-reachable (the password travels in plaintext); the SSH tunnel is the only remote path. Don't
  revert to a bare `6380:6379` (= `0.0.0.0`) without a reason.
- **Attention and recap (v0.18)** — busmon derives answered `waiting` declarations only from a
  later addressed `human=yes` command or a reply in the agent's thread; open asks remain separate
  attention items. Due reminders wait until drafts and other modals clear, then group oldest-first
  and snooze locally. `R` opens a recap built from current snapshots and retained events; capped
  history boundaries and failed reads are labelled instead of treated as empty. The optional
  summary directive requires an explicit `y` confirmation and uses the normal send path (`send.go`).
  Global settings are in `~/.config/agentbus/config.toml`, then the legacy `busmon.toml` (or
  `$XDG_CONFIG_HOME/agentbus/`), and
  local last-visit/snooze data is atomically cached under `~/.cache/agentbus/` with mode `0600`.
- **Project health (v0.19)** — the dispatcher's separate bounded health loop writes
  `{p}:health`, transition-only `{p}:health:log`, and `{p}:health:notes` through fenced scripts;
  Redis `TIME` owns persisted timestamps. Health keys do not discover a project or make
  `HasKeysBesidesDispatch` true. Missing/stale evidence is `unknown`, never `ok`; remediation text
  is static guidance. A new `fail` episode wakes sentinel once, plus one separate daily directive.
  Sentinel may restart an identified failed project service once within its budget, or refresh the
  local model table; everything else is escalated. Usage refresh is at most every 15 minutes with
  a fenced writer; model refresh is daily under a host-local lock. `redis-version` (redis-watch,
  `cmd/agentbus/redislatest.go`) compares the broker's INFO version with the latest Redis release
  **of its own major** (the major it runs, read from INFO; no claim about the compose tag): the dispatch
  service's `RedisRefresh` chore reads GitHub's recent `redis/redis` releases (5 s, drafts and
  prereleases skipped) once a day under a host-local lock into `~/.cache/agentbus/redis-latest.json`
  (major -> newest release, 0600; a failed fetch moves only `checked_at`, silently); warn when behind
  in its major; a newer major is never a warning, only named in the detail (changing major is the
  person's decision); unknown when the broker is unread, no release of its major is found or no check
  succeeded within 48 h. The person updates the broker; sentinel never restarts or upgrades it. `github`
  (busmon-github-check, `health/github.go` pure, `cmd/agentbus/githubcheck.go` the runner; 15 min, 10 s) runs only
  when a remote is on github.com (origin first): `ssh -o BatchMode=yes -o ConnectTimeout=5 -T git@github.com` and
  `git -c credential.helper= ls-remote https://github.com/<o>/<r>` with `GIT_TERMINAL_PROMPT=0`, in parallel, every command
  under `LC_ALL=C` (the answers are read by git's English messages); any answer from GitHub is reached (auth unknown),
  matched before the network errors since it may quote a repository named like one (TLS by its own phrases), only a network error failed, an unrecognised answer or a missing binary not
  judged. Both reached ok, one warn (for you), neither unknown then warn after `GitHubNeitherWarnAfter` (1 h, tracked
  in the check's memory: a restart starts it over); never fail, so it never wakes sentinel. The stall guard (spec
  `docs/superpowers/specs/2026-11-01-stall-guard-design.md`; judgement pure in `health/stall.go`, readers in
  `cmd/agentbus/stallchecks.go`; `dispatch.stall_after`/`clear_stall_after`/`quiet_after`, 15 min, 0 = off):
  `team-stall` fails (waking sentinel, which sends master one waking directive with the next ready task) when every
  non-dormant agent (`bus.DormantAfter`) is idle that long, each pane seen not busy by a fresh observation (none =
  unknown), nothing is open (request, accepted task, ask, gate, pause, swap, obligation, valid hand-off, mission) and
  the plan has an agent task ready whose owner is not held; person tasks only = warn `waiting-person` (for you); it
  reads `quiet-agent`'s record (warn suppresses it, unknown is unknown, `QuietOff` when quiet_after = 0). The plan's
  verdicts are read forward (`plan.ReadWithVerdicts`). `quiet-agent` warns (master) on a waking directive or
  escalation from master, the dispatcher or a person, delivered with its payload (an expired request never counts),
  and nothing written since by the agent on status, report or cmd (same millisecond = written), not working, held or
  seen busy; silent since before the retained cmd history = not judged; collected per agent. `clear-stall` warns on
  a valid hand-off older than its threshold, naming the clear pass's last reason (`dispatch.ClearDecisions`; reason
  `pane-focused` is for you). The health loop's `StallNotify` hook (lease token, after each stored `team-stall`)
  tells the person once per fail episode (`FencedNotifyOnceForPerson`, mark `stall` = the record's since, kept across an
  unknown pass and deleted once the check reads ok or warn): at once when master cannot act (`dispatch.CannotAct`, the escalation rule), else at twice
  `stall_after`. busmon shows the health pill, F5
  details and retained transitions in its R recap. Each check has a place per status (`Definition.Place`: `Person`
  statuses with their `Command` are for you, any other warn or fail is in hand by its `Handler`, ok is fine, unknown
  not measured; `PlaceOf` adds the evaluator's fixed `Outcome.Reason` code, stored as `Record.Reason` only when the check
  declares it in `Reasons` (code → the person's command, `CommandOf`): services' spent budget (per kind against
  `ServiceBudgetStarts`, read before inactive since a spent kind exits; its command restarts only that kind) and
  `waiting-git` are for you, an inactive service or a recent error in hand, reviewer-codex and foureyes); busmon pages spec `docs/superpowers/specs/2026-10-31-busmon-pages-mockups.md`): the pill counts only
  what is for you (`health ▲ 2 to do (F5)`, `▲2` below the wide tier), else `?N`, else a muted `●` for in hand, else
  `✓`; F5 is the one list pattern (`listrow.go`: one line per check by place, the fine ones folded, `a` lists them,
  Enter opens a row in place, `y` copies a For-you row's command; the selection follows its check across polls). Runaway is warning-only. `cmd-delivery` judges only the consumer groups of agents registered in
  `{p}:agents` (A9, `deliveryEvidence`: a dead group's lag tripped `fail` for nobody; `agents forget`
  destroys a forgotten agent's group). Scanner, canary, key
  inventory and project-defined checks belong to later releases (password rotation shipped as
  `./setup.sh rotate-redis-password`, the person's act); health is not a security boundary.
- **History archive** (spec `docs/superpowers/specs/2026-11-01-history-design.md`, package `history/`): the
  dispatch service's chore (`dispatch/history.go`, its own goroutine beside the reconciliation, 1 min, a 5 s
  pass, 2000 entries) appends `history.Streams` to `<state>/history/YYYY-MM-DD.jsonl` (UTC day of the id;
  `{"stream","id","f"}`), only while `HoldsDispatch` and under an flock on `history/.lock`; files only, no
  Redis write, so nothing new is fenced. On start (and after a failed write or a deleted cursor) a torn last
  line is truncated and each stream's position is max(`cursor.json`, the files' highest id); no cursor and no
  day file writes the `archive restarted` gap line (`of=*`), a stream trimmed past its position one `gap` line.
  Every field goes through `health.Redact` (whole field on any hit; `SafeText` shares `health.Sensitive`).
  `history.keep_days` (90, 0 = off) and `history.max_mb` (256), pruned daily, never today's file. Bus reads:
  `Bus.StreamAfter`/`StreamOldest`.
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
  *open* challenges; query `agentbus verdicts` instead. And never close a stale gate with a verdict
  of your own (it counts as an approve): `agentbus gate close` leaves a trace that is not a verdict.
  busmon's For you lists a gate only when it concerns the person (`personGateItems`: the person
  opened it, or it holds the person, read as `personGates` when the person is no agent); a review
  between agents never counts in the `for you` badge.
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
  credential. Log backstops (kimi-quota, `quota/kimi.go`, `quota/agy.go`): Kimi Code (`moonshot`)
  from `~/.kimi-code/sessions/*/*/logs/kimi-code.log` only (8 newest, 7 days, 256 KiB tail): a
  `5-hour`/`weekly` usage-limit hit is that window at 100 %, voided by any later `llm response`
  (`AvailableAt`); Kimi states no reset, so it is the honest upper bound newest hit (after the last answer)
  + the window's length, and a `billing cycle` hit (no length) stays unknown. agy (`google`) from
  `~/.gemini/antigravity-cli/log/cli-<date>_<time>.log` (glog, year from the name): `RESOURCE_EXHAUSTED
  (code 429) … Resets in <d>` holds until that stated reset; agy logs no proof of an answer, so
  nothing voids it earlier, and the quota may be per model: `kind = "off"` is the escape. The dispatch service (`serviceQuota` → `dispatch.QuotaConfig`) collects every minute,
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
  `agentbus swaps` lists the ledger (a reload's entries tagged `[reload]`). Lifecycle-3a: a reload
  (`agentbus reload`, part C) holds the same intent with `kind=reload` (`SwapIntent.Kind`,
  `SwapKindReload`), so every swap rule of the dispatcher applies unchanged; its `begin` carries the
  kind and is refused when the role has no `{p}:agents` snapshot (`ErrNoSnapshot`, in the intent's
  script: a deleted project is never revived, and tab mode, which has no services marker, still
  reloads), it ends with `reloaded` (`FinishReload`, never the fallback state), an interrupted one is
  closed as a failed reload (`closeInterrupted`), and busmon flags it `reloading` (`SwapIntents`).
  Lifecycle-3b, `agentbus reload <role> [--fresh] [--dry-run] [--reason] [--as <person>]`
  (`cmd/agentbus/reload.go`): who may (sentinel any role but itself on an observed update, master
  last and only with no other pilot driver; master only sentinel; a person anyone by an explicit
  `--as`, which also passes the identity guard), `AGENT_BUS_RELOAD` (caller's env, else the
  project's env file; `up --reload on|propose|off`; `propose` and `off` leave only a person's `--as`), a preflight that changes nothing (verified live
  identity, not its own pane, not on a fallback, `reloadOpenWork` = swap's `openWork` plus an
  `accepted` board task and, only with `--fresh`, its own open ask (a resume keeps the conversation and
  the ask is in Redis), declared idle, no wake typing, a proved Claude Code session unless `--fresh`,
  and `planRelaunch` before the kill: inside herdr, the workspace open, one template tab, or for a pop
  (`tier = "pop"`) agent-spawn and the caller in the project's workspace; a pop comes back through
  `agent-spawn <role> <p> [--resume <id>]` with the bind required (`spawnPop`), as swap does), the intent and a second readiness read, SIGTERM to the re-checked pid (never
  SIGKILL), `relaunchRole` (up's lock and relaunch, the role's tab only, resuming `IdentSession`; after the
  exit it re-reads the tab, bounded 20 s, until `judgeTab` — up's own rule — reads a relaunch, since herdr's
  view lags the exit, and a tab the relaunch skips is an error, not a minute of waiting for proof),
  the proof (`replaced`, plus after an update episode a `client_fp` hashing to the installed
  version), `reloaded`; every failure after the intent is a `failed` ledger entry with its
  recovery. Tool rules: sentinel `agentbus reload *`, master `agentbus reload sentinel`, and a shared
  deny on `agentbus reload *--as*` / `agentbus * reload *--as*` for every role (busmon-reload-deny),
  so an agent cannot skip the caller table or the `off`/`propose` opt-outs by naming a person.
  Defence in depth, not a boundary: the client matches command text (another spelling, a wrapper or
  a script runs it), and a person's own shell has no such rule.
  A fallback record keeps `held_episode`, and the release of that
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
  open ask is held once with a hint (Enter sends, Tab opens its panel prefilled). busmon: the reader's ask page (`tea_reader.go`, `ModeShellAsk`, `!`/`w`/F4; `w` with no ask open
  opens the compose window to master, `ActAskOrWrite`, busmon-keys; Enter on a For you ask or in the agent's page; `askpanel.go`'s bodies), counts and fade follow open
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
  runs a client; sentinel reloads the outdated agents with `agentbus reload` (lifecycle-3b, below).
  v0.15: an observation carries `since` (first reading of
  that state by the same pid, carried forward) and a failed inspection keeps the previous reading
  (`Observation.Fresh`, 120 s); a pane seen `blocked` (its client's own menu or permission prompt)
  reads `waiting` with the flag `prompt` (`promptWaiting`), one bell per `since`, unless the agent
  declares blocked/waiting or has an open ask. The dispatcher also rings the desktop once per such episode
  (`dispatch/promptnotify.go`, decision `docs/memory/decisions/2026-10-27-prompt-notify.md`): after
  `AGENT_BUS_PROMPT_GRACE` s (default 30, `0` = off) of the same pid and `since`, with the same
  suppressions plus a pause or a swap, through the context alert's `notify-send` runner (missing =
  a logged note); its only write is the fenced mark `prompt:<agent>` (= `pid:since`) in `{p}:quota`,
  deleted when the pane is no longer blocked or the agent leaves the observation. No bus entry, nothing typed.
  The same runner rings once per episode for an agent deferred for `identity mismatch` while
  messages wait for it (A8, `dispatch/identnotify.go`: mark `ident:<agent>` = the registered
  `pid:pid_start`, deleted once its decision is no longer a mismatch), since that agent's escalation
  goes to master, which helps nobody when master is the one lost. An escalation caused by the pane's state (rules
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
