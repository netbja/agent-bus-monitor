# Architecture notes

The architecture sections of [CLAUDE.md](../CLAUDE.md), moved here unchanged on 2026-11-06 (claude-md-split).
The rules that bite every agent stay in CLAUDE.md; the operator notes are in
[operator-notes.md](operator-notes.md).

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

**`relaunch/`** is the relaunch core (self-repair watcher spec, part 7, step 1): the template's
`Tab` and `Template`, the one life rule (`SnapLife`, a pid gone only by `PidListedNoMore`), the
pure `Decide`, `JudgeTab` over herdr's panes, `Open` (a new pane split from the tab's, the
template command run in it, the old pane closed once the new agent is proven) and `Role`, which
relaunches one role: the settle loop, then `Open`, then its verdict. It never types into an
existing pane. Two entries call it. The CLI entry (`reload`, `up --resume`, in `cmd/agentbus`)
keeps the `HERDR_ENV=1` guard and `up`'s lock. The service entry (`ServiceRole`, with
`ServiceHerdr`) is for the dispatch service, which runs in no pane: it reaches herdr through the
session its env file names, refuses when none is named, and takes the same lock
(`ProjectLock`, `~/.cache/agentbus/up-<p>.lock`) without waiting. Nothing calls the service
entry yet: the watcher's R1 will (step 7). Step 2 adds the role's **intent** for a self-repair
(`bus/selfrepair.go`): `BeginSelfRepair`, `SelfRepairHeld` and `EndSelfRepair` take, check and end
`{p}:swap:<role>` with kind `self-repair` and a nonce, each in a script that also checks the
dispatch lease token, so one relauncher holds a role at a time. `up` reads each agent role's intent
right before relaunching it (`Options.Held`) and skips a held one, saying by what.

**`termhost/`** is the seam to whatever hosts the agents' terminals (herdr-exit spec, 4.2, step
1): the `Host` interface (`Start`, `List`, `Close`, `Screen`, `Activity`, `Processes`, `Submit`,
`WaitScreen`, `Ping`, `Describe`) and its errors, and `Line`, the only thing a host types: built by
`WakeLine(project, agent, count, nonce)` (names `ValidName`, count at least 1 and shown as `999+`
past 999, nonce `^[0-9a-f]{6,16}$`) or `ClearLine()`, with no exported field
(`TestLineIsAClosedSet`). `termhost/fake` is an in-memory host for tests and `termhost/contract`
the suite every adapter passes (`Run(t, harness)`; the fake today, the holder later). Nothing
runs on a `Host` yet: only `Line` is used, by `dispatch.Runtime.Prompt(ctx, pane, line)`, which
types the same wake line as before and takes no text.

**`plan/`** (v0.22) is the batch's plan as the person reads it: `docs/plans/<name>.json` (strict,
`Parse`), published at a commit by `agentbus plan load` into `{p}:plan` (the file kept opaque:
cjson would rewrite `[]`) plus master's facts in `{p}:plan:facts` (`bus/plan.go`: PR, done, note,
Redis `TIME`; one script each, a task not in the plan refused); neither key is in `projectKinds`,
and a load is refused for a project with no such key. `Derive` (`derive.go`) is pure: a board
request `done` is *delivered*, so **in review**; done needs a recorded `plan done` (shown as a
dated claim) or `merge:false` + delivered + approved; an approved merge task waits for the person
only with its PR linked (`merge PR N`; plan-view), else stays with the team; a person task whose ask is
answered, nothing open, is `Answered` ("answered, master to record": not For you, master's ready work in
`team-stall`); the review is `bus.RollUp` over the task's
subject (default its board task) plus `pr:<n>`; asks match only by `AskDef.Task`; a never-read
input reads `?`. busmon reads the plan every `requestPoll` tick through the optional
`planReader` (the ledger incrementally, `VerdictsAfter` from the last seen id), derives in
`pollOnce`, and shows the header item, the `P` overlay, a recap section and the lower panel's fourth tab, Plan (A11: `planTabItems`, key 4, the
narrow tier's sixth view; Enter opens a task or expands done). Spec:
`docs/superpowers/specs/2026-10-22-plan-view-design.md`.

**`agentstate/`** judges an agent's state from its screen, per client (herdr-exit spec 4.3, 4.9;
step 2a): `Signature` (Ready, Busy, Modal, Dialog, the input region under the last Prompt line)
with `Match` and `InDialog`, and one embedded profile per client, `clients/<client>.toml`, holding
today's patterns as data; its fixtures are `testdata/screens`. A profile is refused when it names
another client, holds an unknown key, has no prompt marker or a pattern that does not compile.
`TestProfilesAreTheOldTable` pins the move: the profiles compile to the table `dispatch` held.
`dispatch.Signatures` and `Supported` read these profiles; nothing else changed. A new client
is a profile and its fixtures, not code.

**`filepick/`** (compose-at-file) lists a project's files for busmon's `@` in the compose window:
a walk in Go that runs **no program** (git's config can make git run one: `core.fsmonitor`), reads
the `.gitignore` files and `.git/info/exclude` as text, skips `.git`, `node_modules`, `.venv` and
`vendor`, lists a directory holding a `.git` entry (a worktree) as one entry walked on demand
(`SubFor`), never follows a directory link, and offers a file link only when its resolved target is
a regular file inside the root that passes every filter. Bounded (depth 12, 50 000 entries, 2 s),
it says why a list is incomplete. Nothing that looks like a secret (`internal/secretpath`, the rule
health's `tracked-secrets` check shares) or that roles.toml's shared `Read` denies
(`peer.SharedReadDenies`) is offered; `Refused` says why for the popup. `Rank` orders matches (name
prefix, segment prefix, substring, subsequence). `ListDir` serves `@~/` (`ParseHome` refuses `~user`,
`.`/`..` and empty segments first): one directory of the person's home at a time, never a tree,
hidden entries at its top (and `~/Library` on macOS) never offered, the directory resolved and
judged on every component, a file link only for a regular target inside home; `HomeRefused` says
why. Specs: `docs/superpowers/specs/2026-11-08-compose-at-file-design.md` and its home addendum.

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
agent→`AgentSnapshot`; state + `pane` + `session`), `{p}:resume` (Redis hash, agent→`{session, profile,
at}`, never a command; how to resume its last proved session, written by the SessionStart hook's recorder, cleared only by
`forget`), `{p}:events` (Redis stream, `MAXLEN ~ 10000`; the terminal timeline: wake, blocked, attach, relaunch,
holder…; history, trimmed by `busmon --reset` with the activity streams, never in ACTIVITY nor an
activity date), `{p}:usage` (Redis hash, agent→`UsageSnapshot`;
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
(Codex's shared app-server). With the pid goes `boot_id` (first-run-trust): the boot id of the
host it runs on, so a reader on another host (busmon through a tunnel) knows the pid is not one
of its own. cmd `type` gains
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
  `a`, the title `tokensTitle`'s summary): the account windows as cards (usage-cards, `account_cards.go`: per provider a head line, its name upper case in its
  colour and at the right its busiest window or its hold; one row per window, a full-width thin bar with its pace
  mark, the figure and `· resets HH:MM` (weekday beyond a day; an aged reading `· read Xm ago`); a pace sentence,
  `paceSentence`: limit reached, the limit before the reset at this pace, else on track, an aged reading its age and no
  forecast; the card's name the header's, `providerLabel`; a rule between cards; no
  24-hour spark), every project side by side (`ProjectNames`,
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

- **`agentbus`** — fire-and-forget CLI: `status`/`report`/`reports`/`notify`/`cmd`/`thread`/`history`/`challenge`/`reply`/
  `verdict`/`verdicts`/`pilot`/`gate`/`agents`/`bind`/`usage`/`budget`/`board`/`plan`/`shutdown`/`refresh`/`request`/`dispatch`/`missions`/`mission`/`mem`/`roles`/`up`/`start`/`down`/`service`/`subscribe`/`watch`/`listen`/`config`/`version`. Parses args manually;
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
  each fire, **not** a long-lived loop (which would never wake a terminal session). `agents [<name>...] [--json]`
  lists every peer's current state, or only the ones named (`selectAgents`; a name with no snapshot is said, exit 1).
  `health <check>...` prints only those checks, each its status then `reason:`/`detail:` lines (`healthChecks`). `watch` is the legacy alias of `subscribe` (same handler).
  `listen` tails all four streams via `Bus.Tail` for debugging. `report` prints its entry id on
  stdout (lifecycle-2), the id `handoff <self> --report <id>` declares (`bus/handoff.go`:
  `{p}:handoff`, agent → report, session, pid, pid_start, `at`, written in one
  script only for the agent's own report from its verified process in its proved session, with the agent's
  `idle` status entry in the same script and `at` that entry's millisecond (chain-reliability: the declaration is
  the idle, so `DecideClear`'s "idle since its hand-off", `Snap.TS >= Record.At`, holds without another status; the
  snapshot follows through `putSnapshot`, a failure said, not ignored); not in
  `projectKinds`, ignored by `HasKeysBesidesDispatch`; any report and any non-idle status of the
  agent delete it in the same MULTI (`addVoidingHandoff`), so voiding never waits for a pass;
  `Handoff.VoidFor` — another process or session, or working since — is the rule the dispatcher's
  fenced `handoffPass` deletes by and busmon's `handoff` flag shows by; `handoff` alone lists them, read-only for the identity guard, each with its report id and its exact time (RFC3339 to
  the millisecond, local) beside the age; the
  context alert's directive now asks for both). `report` now retains the full
  text (newlines/tabs kept, ≤8000, `AGENT_BUS_REPORT_FULL_MAX`) in a `full` field **only when it
  differs from the one-line ≤500 preview**; `reports [--json]` lists recent reports (marking those
  with more via ` (+N)`; each row shows the headline and each notice's read state, report-one-row: a person's `read HH:MM`/`unread`
  from `{p}:seen`, `?` when unreadable, an agent's `delivered`/`pending` from its group's cursor; `--json` adds `notices`
  `[{target, id, person, seen_at}]`; `--unread [--to <t>]` only the reports a person has not opened, exit 64 when none, refused
  when `{p}:seen` cannot be read) and `reports <id>` prints one report's full retained text.
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
  receiver lease is held). It is what a woken agent runs; `--brief` (agent-brief, `briefLine`) prints one line per
  entry instead, `id from [type ref task=… expires=… attempt=…]: body` (each only when present, the deadline in
  local RFC3339, attempt above 1), the whole body on it (`\\` and every line breaker escaped), `(person)`,
  `(duplicate?)` and `(stale)` marked, same exit codes, so an agent reads the bus without `jq`. `cmd --no-wake` writes `wake=no`
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
  page has something for it; message-reply-key, `reply_key.go`: `r` (reply-key-r; `i`/`I` kept) on a message, report or note page opens the compose
  window to its sender prefilled `re: <first line>` (cut at `replyFirstMax`, 72 runes; a report notice quotes its
  report, the retained one's text else the notice's subject, never the envelope: `replySource`), offered (`OnPage`, after close in
  the footer) only when the sender is a registered agent (`replyTarget`: never the dispatcher, the person or a non-agent,
  each said when `r` or `i` is pressed), a draft opened as it was, never overwritten; `r` is shared with About's replay in the
  page table and `lookupOn` takes the binding the top page offers, the last match when it offers none;
  `TestNoKeyCollisionPerMode` fails on a key two actions share in a mode unless both are `OnPage` and no page offers both), `keys.go` (one key table per mode drives the handler, the footer
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
  never wraps by one cell. `forYou` ranks asks, the stall guard's rows for the person (`stallItems`, rank `rankHealth`: `team-stall` warn `waiting-person` and `clear-stall` warn `pane-focused`, while current; Enter opens F5), waits, the person's gates and unread lines, and `decisions` is
  its badge and reminder subset; a plan task waiting for the person is an item of its own (`rankPlan`, `planItems`:
  counted in the title and badge, Enter opens its page with the Action row, also from the reminder; it stays a standing
  decision in the badge and the reminder until master records it done, even one with no `after`); with nothing for the person it says so, then
  the next decision as a selectable row (`forYouEmptyRows`, foryou-no-progress: no In progress rows, the Agents panel shows them; key
  `empty:plan:<task>` opens the plan task; no store), and the
  recap's For you section shows only when the For you panel is not on screen (`recapForScreen`), in the list format
  (recap-foryou: `recapItemLine`, one line per item, the sender in its provider's colour, its text, the age at the
  right edge, an unread one marked `●`; a report notice reads as its report's own preview, `reportText`, never the
  envelope nor the id; what was sent to the person newest first (the unread rows with no item, asks, waits and blocked
  agents keep their own order around them), identical items (sender and text) merged with `(N)` and the newest's report,
  an unread entry that is such an item marked on it, never a second row; items older than a day folded behind
  `+ N older` by `recapFold` until `o`; Enter reads every entry the item stands for (`recapRow.IDs`, a merged item's
  members, `readRecapRow`: the notices, never only the report shown) and opens the report's full text, `Y` copies it; a
  report not at hand keeps its preview, Enter and `Y` say why (`reportGone`: no longer retained, or could not be read when
  the report stream's read failed, `ReportUnread`) and copy nothing, never the envelope); its keys are `ModeShell` (the panel table with the shell's tabs)
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
  paste, ended by a plain move, a click or a first Esc; PageUp/PageDown a page of the window's rows; compose-undo: `^Z`
  undoes and `^Y` (or ctrl+shift+z) redoes, per field, `textArea.edit` keeping the state before each edit that changed
  the text (text, cursor, selection), a paste or an image path one step, a run of typed characters or of deletions one
  step (a move, a click, a line break or a selection ends it), at most `undoMax` (100) steps, a new history at every
  `set` (open, send, completion); no setting); `^V` pastes the clipboard's image (usage-cards, `clipimage.go`: wl-paste, xclip or pngpaste,
  chosen by the session (WAYLAND_DISPLAY, DISPLAY, macOS), 5 s timeout, read through its 20 MiB bound, PNG only, saved 0600 as a new file under `os.UserCacheDir()/agentbus/attachments` (0700), its path
  inserted at the cursor with a space on each side as its neighbours need, said when there is no tool or no image; our own
  files older than 30 days pruned at each paste; never uploaded)
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
  of a header gauge (the fill keeps its level roles: `RoleGauge`'s deep green (usage-green: `#39C69E` dark, `#0B7D60` light, ANSI green), amber from 80 %, red from 95 %, usage-cards; the thin track is the fill faded; when a gauge's two windows do not fit, every provider keeps only its busiest, `quotaSegOf`), the model lines of an agent's page, and the U page's
  account windows and per-agent provider column. Icons (busmon-icons, spec
  `docs/superpowers/specs/2026-11-02-busmon-icons-design.md`): `display.icons` = `off` (default, today's screen byte for
  byte, `TestIconsOffIsToday`) | `nerd` (a lettered box per provider) | `agent-font` (the maker's logo, by client then
  provider), env `AGENT_BUS_BUSMON_ICONS` (an unknown value skipped, never a broken screen), chosen on the C page with
  arrows and applied at once, its row drawing each mode's sample; `theme.go`'s `IconGlyphs` (one Private Use rune, one
  cell) through `agentIcon`/`providerIcon`, always as `iconPair` (the icon and a blank cell busmon writes, two blank
  cells for none): the Agents rows and the U page's account and agent rows at 80 cells and wider (the message or the
  trailing room pays), the header gauges in the wide tier only and only while no text is lost (`admitHeader` run with
  and without the pairs); never the For you, Activity or Requests `from` column (the person's answer). `TestEveryQuotaProviderHasItsColour`
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
  (`Bus.HumanClose`). The Work tab (`3`, requests-plan-redesign; `work_model.go` the one producer,
  `work_view.go` the rows) lists who owes what: YOU, TEAM, NEXT, IDEAS; its "between agents" line
  opens the Requests list, which answers who owes whom an answer (spec
  `docs/superpowers/specs/2026-10-28-requests-view-design.md`; `requests_view.go` pure, `requests_rows.go`
  rows): a thread is the unit, a row a group (section, sender, target, kind); sections owed by you, you wait on,
  tracked requests, reviews still gated, directives between agents, notices, drawn under three headings with
  their counts since home-inbox-drawer §5 (`Owed by you`, `You wait on`, `Between agents` for the next three and
  the notices line; the title's directive count reads `directives`) (report notices by
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
  Request priority (request-priority §6, `priority.go`): `+`/`-` on a single tracked request's row (Requests, `OnPage`
  through `pageOffers`, which reads the row key only, `priorityTarget`) or on its page write the person's level through
  `busWriter.SetRequestPriority` in a command, said by a flash (the next poll reads the board; busmon never edits its
  copy); a group, a row that is no request and a done one say why; each row carries a mark column (`listRow.HasMark`:
  `↑` attention high, `↓` muted low, blank normal; a group its highest member's); the tracked section sorts by
  `threadRank` (a requested request's level, anything else normal), groups by unread, best rank, then oldest.
  Activity auto-scroll (v0.12.3): selecting, clicking or opening a line keeps it live; only `f`
  (`listState.Pause`) and a wheel-up leave it, and (A3) moving the selection down onto the newest
  line or wheeling back to the bottom resumes it (`f`/End still do).
  Attention marks (spec `docs/superpowers/specs/2026-10-21-busmon-ux-design.md`): an idle agent whose
  status message names the person (`forYouHint`, `foryou.go`; never for the default `pilot`) gets
  the flag `for you`: a hint, it never relabels, rings or counts. Activity entries for the person
  (an ask, a cmd to the person's name, a notify with `for=person`, an agent's newest report while it
  waits for the person) carry a mark column before the time (`unread.go`: ● unread, ○ superseded by a
  newer one from the same sender, an answer or a closed ask; a report notice, `reportSubject`'s exact prefix, is never
  superseded, and its Activity and For you rows read as its report's headline, never the envelope: report-notice-unread; report-one-row, spec
  `docs/superpowers/specs/2026-11-05-report-one-row-design.md`: a notice whose `reportSubject` id is its `ref` folds into its report's
  Activity line (`activityFeed.add`: `reportLine`/`noticeLine` by seq, pruned at the `feedCap` trim; a report takes a waiting notice's
  line over in place, so the selection follows; no report at hand, the notice stays its own line), the line's `→` its targets, its mark
  its notices to the person (`feedLine.personNotices`), opening it reads every member (`markLineRead`); For you one row per report
  (`unreadKey`, `attentionItem.IDs`) and `classifyWaits` adds no second row for a report noticed to the person; opening a notice to
  the person also writes the read receipt `{p}:seen` (`bus/seen.go`: notice id → `<ms>|<person>`, Redis `TIME`, first opening wins,
  512 fields, only while the cmd stream exists; runtime; the person's decision, the one read fact on the bus: busmon never reads it
  back); opening the line reads it, saved in the
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
and logs `clear-deferred` once per change of reason, so a hand-off never waits silently); on by default since autoclear-default
(the person's decision), `AGENT_BUS_AUTO_CLEAR=0` or `dispatch.auto_clear = false` turns it off (any env value but `0`/`1` is skipped with a warning naming the variable). The fake runtime enforces `dispatchTypes` in every
dispatch test: a prompt that is not a wake line, or a `/clear` without its reservation, panics. The wake
line is fixed — `[agentbus#<nonce>] <project>: <n> message(s) waiting for <agent>. Run: agentbus
--project <project> subscribe --drain <agent>` — and the agent then drains through Redis. The
dispatcher touches only unarmed agents with a real obligation, in a pane that is ready, unfocused
and pid-verified (focus-notice, `dispatch/focusnotify.go`: a wake deferred for `pane focused` whose oldest message waits
`dispatch.focus_notice`, 30 s, `0` = off, page C Attention, rings the desktop once per oldest message through the prompt
ring's runner, fenced mark `focus:<agent>` in `{p}:quota` deleted once the focus defer ends; nothing typed). Don't add a second pane writer, and don't make the dispatcher type message
content. Since v0.12 it runs, with the conductor, as a **user service** (systemd `--user`,
launchd), started by `agentbus up`; herdr tabs remain only as the explicit fallback
(`docs/memory/decisions/2026-10-14-project-services.md`).

There can also be a parallel notification path that is **not** on this bus: a Claude Code `Stop`
hook POSTing to a notification webhook of the person's own. It never touches Redis — see README
"Example deployment".
