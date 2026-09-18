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
agentbus cmd [--ref T] claude2 run the suite       # directive; prints the entry id (thread root)
agentbus thread 1782588072942-0                    # show the :cmd thread (ref or id == arg), chronological
agentbus report claude1 "bug fixed"                # curated report (note kind)
agentbus report claude1 --auto "soak 24h done"     # auto = Stop-hook safety net
agentbus reports                                   # list recent reports ((+N) = full text retained)
agentbus reports --json                            # same list, machine-readable JSON
agentbus reports <id>                              # print one report's full retained text
agentbus subscribe claude1                         # block for next cmd then exit (re-arm to stay subscribed)
agentbus subscribe claude1 3600                    # same, with a 1h idle window before the heartbeat object
agentbus listen                                    # debug tail (all four streams)
agentbus pilot claim --ttl 120s                    # claim pilot lease (self = AGENT_BUS_AGENT)
agentbus gate claude2                              # list open 4-eyes challenges; exit 1 if gated
agentbus verdict --pr 25 myagent approve "LGTM"    # write verdict to {p}:verdicts; --ref resolves matching gate (best-effort)
agentbus verdicts --pr 25                          # roll-up 4-eyes state: APPROVED/REJECTED/PENDING (exit 0/3/2); no-arg lists recent
agentbus usage                                     # print every agent's budget; usage <a> '<json>' writes one
agentbus version                                   # print the bus protocol version (no project/broker needed)

busmon --list                                      # what projects are on this bus? (no --project needed)
busmon --project myproject                         # live dashboard (last 25 lines, then live)
busmon --project myproject --limit 100             # backfill the last 100 lines on launch
busmon --project myproject --limit 0               # replay all retained history (pre-limit behavior)
busmon --project myproject --reset                 # purge the project's streams first (asks to confirm)
busmon --project myproject --reset --yes           # purge without the confirmation prompt
busmon --delete oldproject                         # retire a project: DEL every one of its keys (asks to confirm)
busmon --delete oldproject --yes                   # ...without the confirmation prompt
```

The `subscribe` output carries `"v"` (= `bus.ProtocolVersion`, currently 1). Changes within a
version are additive only (consumers must ignore unknown fields); `v` bumps only on a breaking subscribe-contract change (a field
removed/renamed/repurposed or a semantics change). A consumer that sees an unknown higher `v` should
fail loud, not best-effort. Stream entries and `agents`/`usage`/`verdicts` output are not versioned.

On launch, busmon backfills only the **last `--limit` ACTIVITY lines** (default `25`, merged across
all four streams) before live-tailing — set a persistent default with `AGENT_BUS_BUSMON_LIMIT`, or
`--limit 0` to replay everything. `--reset` clears the project's history (`XTRIM` of the four
streams — it keeps consumer groups and the armed/pilot/gate leases, so cmd delivery is unaffected)
after a `[y/N]` confirmation; a piped/non-TTY stdin counts as "no".

`busmon --list` is the way back in when you have forgotten a project's name — it is the only busmon
invocation that does **not** need `--project`, since that is the question it answers:

```
$ busmon --list
PROJECT             AGENTS  MASTER  LAST ACTIVITY
ai-tradex-solana         6  master  2m ago
agent-bus-monitor        2  -       3h ago
demo                     0  -       23d ago
```

Newest activity first, so the project you just left is the top row. It prints to stdout and exits
(nothing else does — pipe it), reports an empty broker on stderr, and never writes: combined with
`--reset` the list still wins and nothing is purged. `AGENTS` counts every agent the project has
ever seen — the `{p}:agents` hash is never pruned — so a retired peer still counts; `LAST ACTIVITY`
is what carries recency. A project purged with `--reset` keeps its date (the hash outlives the
`XTRIM`); `never` means nothing on it was ever dated.

`busmon --delete <project>` is the other half: it retires a project by `DEL`-ing every `{p}:*` key.

```
$ busmon --delete demo-2574
Delete ALL 2 keys for project 'demo-2574' (0 agents, last activity 23d ago)?
This is a DEL, not --reset: streams, consumer groups, agents/usage/budget/verdicts, pilot and gate leases all go. [y/N] y
Deleted 2 keys for project 'demo-2574'.
```

**`--reset` and `--delete` are deliberate opposites.** `--reset` clears the history of a project you
are keeping — an `XTRIM`, so consumer groups and the armed/pilot/gate leases survive and cmd
delivery is unaffected. `--delete` removes a project you are not keeping, and takes all of that with
it. Same `[y/N]` gate and same `--yes` bypass as `--reset`; the prompt states the key count, agent
count and last activity, because a slug alone is a poor thing to judge on. A project with no keys is
a no-op success, so a repeated cleanup script does not fail on its second run.

## busmon panes

```
 demo  ·  ⬢ MASTER hermes  ·  5 requests waiting · oldest 3h  ·  anthropic 25%/44%  ·  ⇄ bus ok
┌─ AGENTS ─────────────────────────────────┬─ BOARD  [1/5 done] ─────────────────┐
│ architect: done · no update 5h           │ task-23  sentinel  working  4m      │
│ coder: working · no update 18m 👂 🔒1 ⧉  │ task-21  coder     accepted 21m     │
│ foureyes: idle · no update 10m 👂 ⧉      │ task-20  foureyes  requested 43m    │
│ ⬢ hermes: working (driving the slice) ⧉  │ task-22  coder     blocked  1h      │
├─ ACTIVITY  [live] ───────────────────────┴─────────────────────────────────────┤
│ ── Fri 2026-09-18 ──                                                           │
│ 03:00:14 [coder] working | refactoring the stream parser                       │
│ 03:06:14 [report:note->coder] résumé du refactor: 3 étapes, 2 faites… (+404)    │
│ 03:14:14 [challenge foureyes->coder pr-42] explique pourquoi le parser ignore…  │
├─ INPUT  [Tab feed · F1 legend · F2 requests] ──────────────────────────────────┤
│ > _                                                                            │
└────────────────────────────────────────────────────────────────────────────────┘
```

- **STATUS** — top bar: the project name, the pilot-lease driver as
  `⬢ MASTER <driver>` (or `autonomous (no master)` when no lease is held), what is
  waiting on someone (`5 requests waiting · oldest 3h`), the account budget per
  provider, and `⇄ bus ok` — the **monitor's own** link to Redis. That last one is
  separate on purpose: a broker busmon cannot reach looks exactly like a team that
  has gone quiet, and the two call for opposite reactions. When it breaks it reads
  `⇄ monitor cannot reach the bus 12s (dial tcp …)` and the feed retries from its
  own cursors rather than dying silently.
- **AGENTS** — one chip per agent, keeping three facts apart that are easy to
  confuse:
  - **the state the agent declared** — `coder: working` — colour-coded, and never
    overwritten by silence. An agent that said `working` three hours ago and has
    said nothing since still reads `working`: that is the last thing it actually
    told you.
  - **how old that declaration is** — `· no update 18m`. Nothing is shown while
    the agent spoke within `idleAfter`. This replaces the old `offline` label,
    which turned silence into a claim about the agent; the bus has no heartbeat,
    so silence is silence. A `{p}:report` proves the process is alive but does
    **not** refresh the declared state, and an agent that has published a report
    but never a status reads `no state declared`.
  - **the subscription** — `👂`, a live `subscribe` lease. A lease, not a state.

  Then the badges: `⌛N` = N cmd entries unread by its consumer group (orange when
  nobody is listening — the "stopped re-arming" tell); `🔒N` = open 4-eyes
  challenges; `⧉` = attached to a herdr pane; `[120k ctx]` = that agent's own
  context fill (from `{p}:usage`); `⬢` = holds the pilot lease. Chips wrap to fit
  the terminal width, and `+N` means that many did not fit. The roster comes from
  the `{p}:agents` hash, so an agent that last spoke before the `--limit` backfill
  window still gets a chip. Press `?` for all of this on screen.
- **BOARD** — the shared task board (`{p}:board`), one line per task:
  `task owner state branch age`, state color-coded, newest activity first (the
  same order as `agentbus board`). Refreshed by the same 1s ticker as the rest.
  The title counts `N/M done` and turns green `✓ all done` when every task is
  done — the visual cue that an `agentbus shutdown` is pertinent. The pane is
  hidden while the board is empty, leaving the full row width to AGENTS.
- **ACTIVITY** — scrolling, color-coded feed of status, notifications, commands,
  and reports. Lines carry times only, so a dim `── Mon 2026-08-04 ──` separator
  is inserted at each day boundary (and above the very first line) to keep a
  multi-day history readable. It live-tails by default; **Tab** moves focus here. While focused,
  **↑↓** / **j k** select a line (highlighted), **g**/**Home** jumps to the oldest
  and **G**/**End** to the newest, **Enter** opens the selected message in full,
  and **y** copies the line to the clipboard (OSC52, so it works over the SSH
  tunnel). **/** filters the feed; mouse wheel / PgUp/PgDn still scroll. The title
  shows `[live]`, the browse indicator `[↑ pause · N below]`, the active filter, or
  the selection position. **Esc** walks back out one step at a time — selection,
  then filter, then focus — so nothing is lost by accident.
- **INPUT** — type a message, Enter publishes on `{p}:notify`; an `@<agent> <text>` line sends a
  directed cmd to that agent (type `@` for autocomplete). Esc/Ctrl-C quits.
  The field inherits the terminal's own fg/bg colors (no forced white-on-blue), so
  it stays legible in any theme.

### Overlays: summary on screen, detail on demand

Three views sit over the layout, one keypress away, so the panes stay compact.

**Enter — the message in full.** The feed shows one clipped line; this shows the
whole entry: author, addressee, absolute date and age, entry id, thread, and the
body with its newlines intact. `y` copies the text exactly as published, `Y` the
whole thread. For a `cmd` it also lists the thread transcript — the directive, its
replies and its verdict, in order — so an exchange interrupted halfway can be read
back without hopping between terminals.

It also states **how much of the text is really there**, which the feed cannot:

| line | meaning |
|---|---|
| `complete — this is the whole message as published` | the entry carries `text_complete: yes` |
| `truncated at publish — the rest was never stored` | `text_complete: no`; re-reading will not bring it back, the author has to resend |
| `one-line preview, no further text retained` | no `full` field was stored |
| `completeness not marked on this entry` | published before the marker existed, so completeness is **unknown** |
| `not retained — this id reads back empty (cause unknown)` | the entry is gone; an empty read cannot tell trimming from deletion |

A trailing `…` is deliberately **not** read as proof of truncation: the sanitiser
writes one when it cuts, but so do authors.

**r / F2 — what is waiting on someone.** Tracked requests from the board, ordered
by what needs attention (blocked, then unclaimed, then in progress, then done),
each with its target, age, what would move it, and the evidence: the deadline
**only when `expires_at` is set**, and the delivery disposition in words.
`output_written` renders as *written to subscriber output (not proof it was read)* —
never as received or accepted; `queued` means no output was **recorded**, not that
none was ever written; a `missing` body never implies the work is done. Cmd threads
with no answer in the retained history are listed separately and labelled as
carrying no acceptance signal at all, because the protocol records none for them.

**? / F1 — the legend.** Every badge, colour and counter, in words.

### Liveness model (why no dedicated heartbeat)

Agents are one-shot CLI invocations, not daemons — nothing is alive between
invocations to emit a periodic heartbeat. Liveness is derived **passively** from
the Redis stream-entry timestamp of each agent's last `status` or `report` entry:
every such publish *is* the heartbeat. A dedicated heartbeat stream would buy nothing
the existing traffic doesn't, until agents become long-running.

What busmon will **not** do with that signal is guess. Silence is reported as
silence (`no update 18m`), never as `offline`; a report moves liveness but not the
declared state; an armed lease is shown as a lease; and no agent's presence is ever
used to infer that its task has progressed. Task state comes from the board, and
acceptance only from an explicit request transition.

### Reproducing the screens

`scripts/busmon-demo.sh` builds an isolated bus for demos and renders — its own
Redis container on port 6390, its own throwaway `demo` project, never the real
broker on 6380:

```bash
scripts/busmon-demo.sh up                          # start + seed the demo bus
scripts/busmon-demo.sh run                         # busmon against it
scripts/busmon-demo.sh capture out.txt ./busmon F2 # one screen, via tmux, to a file
scripts/busmon-demo.sh down                        # remove the container
```

The seeded data is deliberately awkward — an agent that declared `working` and went
quiet, an agent known only to the agents hash, a long multi-line Unicode report, a
pre-retention report, an answered thread, an unanswered directive, and a tracked
request in each state. `docs/captures/` holds the before/after renders produced this
way.

## Bootstrap a team

`scripts/bootstrap` brings a whole team up from one keypress, and the master can **pop** extras on
demand — both go through one shared launch recipe. It's shell tooling *around* the bus
(`scripts/`, `roles/`, `roles.toml`), not part of the bus protocol.

### One-time setup

```bash
# from this repo:
go build -o busmon ./cmd/busmon              # the busmon tab runs $REPO/busmon
go install ./...                             # put agentbus + busmon on your PATH (the agents call them)
scripts/link-role-skills.sh                  # symlink each role's skills into ~/.claude/skills
```

Keep the **herdr-plus** checkout at `~/Tools/herdr-plugins/herdr-plus` (or set `HERDR_PLUS_PATH`) —
`bootstrap` handles the rest: it **builds** `bin/herdr-plus` if it's missing, then **links** the
checkout into the project's herdr session (herdr registers plugins **per session**). The build step
matters because `herdr plugin link` registers the plugin but does **not** compile it, and every
entry point spawns `bin/herdr-plus`; without it, opening the Projects picker fails with *"Unable to
spawn … bin/herdr-plus … does not exist"*. To do it by hand: `make -C
~/Tools/herdr-plugins/herdr-plus build`. Also needs the Matt Pocock skills present locally (the role skills resolve from
`~/Tools/herdr-plugins/skills/skills/engineering/`). If you use `--cron`, make sure `agentbus` is
reachable on the cron job's `PATH` — the trigger prepends `~/.local/bin`, so building the binaries
there is the simplest fix.

### Start a new project

herdr registers plugins **per session**, and you give each project its own herdr session — so the
Projects picker only exists in a session once herdr-plus is linked there. `bootstrap` does that link
for you, but herdr only accepts it into a **running** session, so create the session first and
provision from inside it:

```bash
# 1. create + attach the project's herdr session (must be running for the link to take):
herdr --session myproject

# 2. from inside it, provision — links herdr-plus into this session AND writes the template:
/path/to/agent-bus-monitor/scripts/bootstrap new myproject /path/to/your/project

# 3. open the Projects picker and pick myproject:
herdr plugin action invoke cloudmanic.herdr-plus.projects
```

`bootstrap` **launches nothing** — it provisions (broker up, skills linked, **herdr-plus linked into
the session**, template written). The `new` line's 2nd argument is where the team works — every
agent's cwd, and where `--index` builds the index; omit it to use `$PWD`. It is **not** the tooling
repo (broker, `agent-launch`, `busmon` stay here). The linked session is `$HERDR_SESSION` (so
running from inside it just works), else the project name — pass `--session <name>` if your herdr
session is named differently. Picking `myproject` opens the workspace: one tab per boot role —
`master`, `coder`, `foureyes`, `sentinel` — plus a `busmon` tab. Each tab boots its Claude agent,
which arms on the bus (`agentbus subscribe`) and appears in busmon.

From there you drive the team as a human:
- **Watch** everything in the `busmon` tab (AGENTS + ACTIVITY).
- **Talk** to an agent from busmon's INPUT: `@master start on the plan at docs/…` (`@` autocompletes
  agent names), or just type in the master's own tab.
- **Grow** the team on demand — from the master: `scripts/agent-spawn architect myproject` (design
  work), `scripts/agent-spawn deploy myproject` (ship it + watch the target), or a second `coder`
  for surge.

Add `--index` (warm codegraph / code-index for the repo) or `--cron` (a daily sentinel review) to
the `bootstrap new` line when you want them.

### Resume a project

```bash
scripts/bootstrap myproject          # no verb -> recall: ensures the broker is up, reuses the template
```

Re-open it the same way through the **Projects** picker. Mind the brique-1 scope: **recall restores
the _workspace_ (tabs / layout) with _fresh_ agent sessions** — not the previous conversations. To
pick up a specific agent's earlier conversation, use Claude Code's own resume — the sessions are
named `<project>:<role>`, so they're easy to spot:

```bash
claude --resume                      # then choose e.g. myproject:coder from the list
```

(Automatic scripted recall — every agent's conversation restored in one go — is deferred to the S4
follow-on; see the spec.)

### Roles & pieces

**Roles** live in `roles.toml` (hand-editable — adding one needs no code change):

| Role       | Model                                | Perms             | Tier | Job                                             |
|------------|--------------------------------------|-------------------|------|-------------------------------------------------|
| `master`   | `claude-sonnet-5`                    | acceptEdits       | boot | pilot: coordinates the team, gates each task    |
| `coder`    | `claude-opus-4-8`                    | bypassPermissions | boot | implementer (TDD)                               |
| `foureyes` | `claude-opus-4-8`                    | bypassPermissions | boot | independent 4-eyes reviewer                     |
| `sentinel` | `claude-haiku-4-5`                   | bypassPermissions | boot | daily review + notify-only master-context nudge |
| `architect`| `claude-fable-5` → `claude-opus-4-8` | bypassPermissions | pop  | design / specs, popped on demand                |
| `deploy`   | `claude-sonnet-5`                    | bypassPermissions | pop  | ships the project to its target, then watches it|

- **`scripts/agent-launch <role> <project>`** — the shared leaf: resolves the role from `roles.toml`
  and `exec`s `claude` with the right model, permission mode, skills, and session name
  (`<project>:<role>`, so `claude --resume` shows who is who). Used by *both* the boot tabs and the pop.
- **`scripts/agent-spawn <role> <project>`** — the master's pop: opens a new herdr tab running
  `agent-launch` (pop == boot). Requires `HERDR_ENV=1`; also documented in the master skill's
  "Spawn a peer" section.
- **`scripts/link-role-skills.sh`** — symlinks each role's skills into `~/.claude/skills` from this
  repo (`agent-bus*`) and the Matt Pocock collection (refuses to overwrite a non-symlink target).
- **`scripts/daily-review-trigger.sh`** — the `--cron` job pokes the sentinel's pane (resolved live
  via `agentbus pane sentinel`) once a day to write a project review; the sentinel also nudges the
  master to reset its context when it saturates — **notify-only**, it never clears the master's pane.

Design/rationale live in `docs/superpowers/specs/2026-07-16-master-bootstrap-design.md`. Tests are a
dependency-free bash suite: `bash tests/run.sh` (see **One-time setup** above for prerequisites).

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
| `{p}:report`        | `agent kind(note\|auto) message`; optional `full` (kept only when it differs from the ≤500 preview) | AGENTS + ACTIVITY + hermes      |
| `{p}:notify`        | `from message`                                   | ACTIVITY                        |
| `{p}:cmd`           | `from target type ref command`                   | ACTIVITY + agents               |
| `{p}:verdicts`      | `subject author reviewer decision message ref`   | audit ledger                    |

Additional keys: `{p}:pilot` (string, pilot lease), `{p}:gate:{agent}` (hash, 4-eyes challenges),
`{p}:armed:{agent}` (string with TTL, the subscribe presence lease behind the `👂` badge),
`{p}:board` (hash, task→owner/state/branch — the shared ownership registry, `agentbus board`).
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
