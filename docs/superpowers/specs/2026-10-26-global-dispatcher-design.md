# One host dispatcher instead of one per project — design

Status: **decided: A** (Bernard, 2026-10-26, ask answer `1793035843860-0`). Author: architect.
Task `global-dispatch-spec`, from master. No code. Decision note:
[2026-10-26-host-dispatcher](../../memory/decisions/2026-10-26-host-dispatcher.md).

Revises part of [project services](../../memory/decisions/2026-10-14-project-services.md)
("one unit per project and kind"). Everything else in that decision stands, along with the
[event-driven dispatch decision](../../memory/decisions/2026-10-10-event-driven-dispatch.md):
one automated pane writer, a wake line only, every dispatcher write fenced.

## Problem Statement

Each project that `agentbus up` starts gets two user services, `agentbus-<p>-dispatch` and
`agentbus-<p>-missions`. On this host today that is four processes for two projects
(`agent-bus-monitor` and a second project), plus an env file for a third with no unit
running. Each project's dispatcher repeats work that belongs to the host, not to the project:

- **Quota collection.** Each dispatcher runs every quota reader once a minute: the Claude cache
  file, `codex app-server account/rateLimits/read`, the Grok log, external commands. N projects
  means N identical readings of the same account windows and N `codex app-server` spawns a
  minute, all producing the same numbers.
- **Host-wide health chores.** The model table refresh already needs a host-local lock because
  every dispatcher tries to run it.
- **One unit per project to install, migrate, budget, log and remove.** Each new project adds a
  unit, a budget file, a crash file and a log. `setup.sh update` installs a new binary, but every
  running dispatcher keeps the old one until someone runs `agentbus up <p>` or `service restart`
  for that project.

The person wants one dispatcher per host that serves every project started on that host. The
protections the per-project units give must stay: a broken project must not stall the others, a
deleted project must stay deleted, a lost lease writes nothing, and the conductor's repository
code stays contained.

## Solution

**Decided: option A below.** One `agentbus-dispatch` user service per host, the **host
dispatcher**. It runs one **project runner** per **enrolled project**: today's `dispatch.Run`,
unchanged, with its own `{p}:dispatch` lease and token, its own Redis client, its own herdr session,
its own log file and its own restart budget. The project runners share one thing, the quota
collector: one reading a minute, handed to every runner's quota policy.

**The conductor stays per project**, one `agentbus-<p>-missions` unit each, as today. It runs
repository code (test commands from agents' commits) in that project's `PATH` and checkout. It
blocks for up to 20 minutes per test. And it must never be stopped halfway through an integration
because another project's configuration changed. Putting it in a shared process would turn one
project's runaway test into every project's outage.

What the person sees: `agentbus up <p>` says `dispatch: served by the host dispatcher (pid N, K
projects)`, `agentbus down <p>` stops that project's runner and leaves the others alone, and busmon
shows the same `dispatch up/down`, the same services overlay and the same F3 log as today.

## Vocabulary

| Term | Meaning |
|---|---|
| **host dispatcher** | The one `agentbus-dispatch` user service per host, a single process. It owns no project state. |
| **project runner** | Inside the host dispatcher, the supervisor of one project: it holds that project's lease and runs `dispatch.Run` for it, restarting it with backoff. |
| **enrolled project** | A project with an env file on this host (`~/.config/agentbus/services/<p>.env`, written by `up`) that is not disabled locally. Enrolment is host-local. |
| **served project** | An enrolled project whose `{p}:services` marker exists and lists `dispatch`, and whose runner holds `{p}:dispatch`. |
| **quota collector** | The host dispatcher's single reader of every provider's quota, once a minute. It holds readings, never policy. |

## Options

### A. One process, one goroutine supervisor per project (decided)

Described in full under Implementation Decisions. In short: the host dispatcher scans enrolled
projects every 5 s and starts or stops a project runner for each. Every runner is today's loop,
behind the same leases and fenced scripts. Isolation between projects is cooperative: separate
Redis clients and herdr sessions, a deadline on every call, `recover` on every goroutine, and a
watchdog that gives up the lease of a runner that stops completing passes.

- **Gains.** One process and one unit per host. One quota reading instead of N. A binary update
  reaches every project with one restart. The leases, tokens, fenced scripts and `Decide` are
  unchanged, and so is everything Redis shows.
- **Risks.**
  - *A panic in any goroutine kills every project's dispatch.* Mitigation: every goroutine the
    runner starts goes through one `safeGo` helper that recovers, logs and counts a failure for
    that project only. A test must fail when a goroutine in the `dispatch` package is started any
    other way.
  - *A goroutine stuck in a call that ignores its context cannot be killed.* Mitigation: every
    Redis and herdr call already carries a deadline (`herdrCallTimeout` 3 s, the 5 s observe
    budget), and this must hold everywhere. The watchdog stops renewing the lease of a runner
    that has not finished a pass for 2 min, then starts a new runner with a new token. The stuck
    goroutine is leaked but harmless, because every write it might make is fenced and fails with
    `ErrNotHolder`. The one unfenced act is typing into a pane. A stuck prompt that comes back
    late can type at most one extra wake line, which an agent answers by draining. This residual
    is accepted.
  - *Memory and CPU are shared.* A project with a runaway agent list can slow the others.
    Mitigation: per-pass budgets (the observe pass already has one), and the watchdog's stuck
    signal shows up in health.
  - *Hidden environment reads.* The per-project settings live in each project's env file and are
    no longer loaded into the process environment. `dispatch/` itself reads none. The reads are in
    the code that builds a dispatcher (`cmd/agentbus`: service run, quota, health, dispatch
    supervision) and in `bus.Connect` and the stream caps. Each one is classified under "Per-project
    configuration", and a scoped static test guards the classification.

### B. One supervisor process that forks a worker process per project

The host dispatcher starts `agentbus --project <p> service run dispatch` as child processes,
restarts them, and counts their failures.

- **Gains.** Real process isolation: a panic, a leak or a deadlock kills one child.
- **Risks.** This is the supervisor of our own that the services decision ruled out: we would
  re-implement restart, logs, start limits and stop-on-logout, and do it worse than
  systemd/launchd. It keeps N processes, so it saves nothing but the units. Quota collection is
  still per child unless an IPC channel is added. **Rejected.**

### C. Keep per-project processes, use a template unit

`agentbus-dispatch@.service`, instantiated per project. launchd has no templates, so macOS keeps
one plist per project.

- **Gains.** The smallest change, and process isolation is kept.
- **Risks.** It only removes unit files on Linux. N processes and N quota readings remain, and a
  binary update still needs N restarts. **Closed:** the goal is fewer processes and one quota
  reading, not just fewer files (Bernard chose A, answer `1793035843860-0`).

### The conductor: stays per project (all options)

A host conductor was considered and rejected:

- it runs arbitrary repository code (`go test`, scripts) with that project's `PATH`, `GOFLAGS`
  and working directory, taken from that project's env file. One process cannot hold N
  environments for child processes without re-implementing what the unit gives for free;
- an integration takes minutes. Restarting the process to pick up project B's new configuration
  would kill project A's integration. Today `up` deliberately never restarts a busy conductor;
- test code can exhaust memory or file descriptors. Per project, the OOM killer takes one
  conductor. In a shared process it takes every project's missions;
- it already holds a local `flock` per project and its own lease. Nothing about it is duplicated
  across projects, so merging gains nothing.

## User Stories

1. As the person, I want one dispatcher process per host, so that adding a project does not add a
   process, a unit and a log to supervise.
2. As the person, I want `agentbus up <p>` to enrol `p` with the host dispatcher and start it if it
   is not running, so that starting a project stays one command.
3. As the person, I want `agentbus up <p>` to tell me when `p` is actually served (its lease
   appeared), so that I am not told "started" for a runner that never took the lease.
4. As the person, I want `agentbus down <p>` to stop dispatching for `p` only, so that stopping
   one project never stops the others.
5. As the person, I want one `setup.sh update` plus one restart of the host dispatcher to move every
   project to the new binary, so that no project keeps running last week's dispatcher.
6. As the person, I want busmon's `dispatch up/down`, services overlay and F3 log to keep working
   unchanged for each project, so that I read a project the same way whatever runs it.
7. As the person, I want busmon to say that a project's dispatcher is the host dispatcher, with its
   pid and how many projects it serves, so that I know that restarting it affects all of them.
8. As the person, I want `agentbus service status` to list every enrolled project with its runner
   state, so that I can see from one command which projects are served, waiting, stopped or given
   up.
9. As an agent, I want my wake line, my escalations and my quota holds to behave exactly as they do
   today, so that nothing in my protocol changes.
10. As master, I want escalations from a project's runner to go to that project's escalation
    target (`AGENT_BUS_ESCALATE_TO` in its env file), so that one project's setting never leaks
    into another.
11. As the person, I want a project whose herdr session does not answer to read `waiting-herdr`
    while the other projects keep being woken, so that one closed session never stops the rest.
12. As the person, I want a project whose broker is unreachable to back off on its own while the
    others keep running, so that one broker outage stays one project's problem.
13. As the person, I want a project that fails 5 times in 10 minutes to give up alone and say so in
    `{p}:services`, so that a fault that does not heal is not retried forever and does not take the
    host dispatcher down.
14. As the person, I want `agentbus up <p>` to give a given-up project a fresh budget, so that I can
    retry after fixing the cause, exactly as today.
15. As the person, I want a panic in one project's runner to cost that project one failure, not
    every project's dispatch, so that a bug triggered by one project's data stays contained.
16. As the person, I want a runner that stops completing passes to lose its lease and be replaced,
    so that a hung call does not leave a project unwatched while showing `ready`.
17. As the person, I want a deleted project (`busmon --delete`) to stay deleted, so that no runner
    ever recreates `{p}:dispatch`, `{p}:services` or any other key of it.
18. As the person, I want a project's runner to stop within one scan of its marker vanishing, so
    that a deleted project costs nothing.
19. As the person, I want one quota reading a minute for the whole host, so that N projects do not
    spawn `codex app-server` N times a minute for the same numbers.
20. As the person, I want quota warnings, holds and the coordinator's quota pause to stay per
    project, so that one project's thresholds (`AGENT_BUS_QUOTA_*` in its env file) apply to it
    alone.
21. As the person, I want `agentbus pause` on one project to pause only that project's runner, so
    that pausing stays a per-project act.
22. As the person, I want the conductor to stay one unit per project, so that a test command from
    one repository can never kill another project's missions or its dispatcher.
23. As the person, I want an existing per-project dispatch unit to hand over to the host dispatcher
    without a gap longer than a few seconds and without two dispatchers acting at once, so that
    migrating is safe on a live project.
24. As the person, I want a project I have not migrated yet to keep running its old unit, so that
    the migration happens one `up` at a time or all at once, as I choose.
25. As the person, I want `agentbus service migrate` to move every enrolled project at once, so
    that I do not have to `up` each one.
26. As the person, I want the `--tabs` fallback (no service manager) to keep running one
    `agentbus dispatch` tab per project, so that a host without systemd/launchd still works.
27. As the person, I want two hosts sharing one broker to keep working as today, the lease deciding
    who serves a project enrolled on both, so that a second machine never double-wakes.
28. As the person, I want the host dispatcher never to start at login, so that the "never at login"
    rule of the services decision still holds.
29. As the person, I want the host dispatcher to exit cleanly with nothing to serve after its last
    project is removed, so that it does not idle forever.
30. As sentinel, I want project health to keep reporting the dispatcher per project, including a
    runner that is stuck or has given up, so that a fault on the host is still escalated per
    project.
31. As a reviewer, I want the per-project settings read from each env file into an explicit
    configuration, never from the process environment, so that one project's settings provably
    cannot reach another's runner.

## Implementation Decisions

### Shape of the host dispatcher (deep module)

One new module, the host dispatcher, with a small interface:

- `Serve(ctx, Sources) error` runs until ctx ends or nothing is left to serve.
- `Sources` is everything it depends on, injected: the enrolled-projects lister (reads the
  services directory), a bus opener per project (env → `*bus.Bus` with its own client), a runtime
  factory per herdr session, the quota collector, the clock, the log opener per project, and the
  per-project budget store.

Behind that interface: the 5 s scan, starting and stopping runners, the per-project supervisor
(today's `superviseDispatch` logic: re-acquire with backoff, give up once the project is gone),
the watchdog, `safeGo`, per-project state publishing and the shared quota readings. `dispatch.Run`
and `Decide` do not change shape. `dispatch.Run` gains only an injected quota source in place of
its own reader.

The process stays a thin shell: `agentbus service run-host dispatch` loads the host env file,
builds `Sources` and calls `Serve`. The per-project `service run dispatch` stays for the migration
period and for `--tabs`-less manual use (see Migration).

### Per-project configuration

Each project's env file (`services/<p>.env`, 0600, unchanged format) is parsed into a
`ProjectConfig`. It is never loaded into the process environment:

- broker: `REDIS_URL` or `REDIS_HOST`/`PORT`/`PASSWORD`. Each runner gets its own Redis client
  through a `bus.Connect` variant that takes these values explicitly. This also means two projects
  on two brokers work, and one exhausted pool stalls one project only;
- herdr: `HERDR_SESSION`, `HERDR_SOCKET_PATH`, `HERDR_BIN`/`HERDR_BIN_PATH`. The herdr runtime takes
  them as fields and sets the child's environment per call. Today it inherits the process
  environment, which would hand every project the first project's session;
- `AGENT_BUS_ESCALATE_TO`, and the `AGENT_BUS_QUOTA_*` policy knobs (warn, hold, max age, long
  hold, off);
- the project directory, `AGENTBUS_PROJECT_DIR`, a new key that `up` writes (today it is only the
  dispatch unit's `WorkingDirectory` and the missions unit's `--repo`). One process has one
  working directory, so no check may use `os.Getwd()` to mean "the project".

`ProjectConfig` exposes one lookup, `Getenv(key)`, and its slice form `Environ()`. The rule depends
on the key's scope in the inventory:

- a **project-scoped** key comes from the project file, or else its built-in default (the default
  the reader already applies when the variable is unset, e.g. `localhost`/`6380` for the broker,
  `master` for escalation). It **never falls back to the host environment**: otherwise
  `host.env` would silently become every project's default, which is the leak the contract forbids;
- a **host-scoped** key comes from the process environment (loaded from `host.env`), and a
  project file that sets it is ignored with a warning (see the rows below);
- any other key reads empty.

Every builder that today takes `getenv func(string) string` (`serviceQuota`,
`runawayThresholdsFromEnv`, `bus.LegacyPasswordInUse`, `herdrBin`, `repoDir`) receives `Getenv`.
Every builder that takes an environment slice (`manifestPath`, `skillsMatch`, `realDoctorDeps`)
receives `Environ()`. None receives `os.Getenv` or `os.Environ()`.

**Inventory of environment reads on the dispatch path** (base `f803a72`), with how each is served:

| Where | Reads | Scope under the host dispatcher |
|---|---|---|
| `bus.Connect` (`bus/bus.go`) | `REDIS_URL`, `REDIS_HOST`/`PORT`/`PASSWORD` | **project**: a `bus.ConnectWith(lookup)` variant; `Connect` stays for the CLI |
| service run (`servicecmd.go`) | `REDIS_URL` (validation), `HERDR_BIN`, the `AGENT_BUS_QUOTA_*` knobs via `serviceQuota(os.Getenv, …)` | **project** |
| service run (`servicerun.go`) | `AGENT_BUS_ESCALATE_TO` | **project** |
| quota knobs (`quotacmd.go` `serviceQuota`) | `AGENT_BUS_QUOTA`, `_WARN`, `_HOLD`, `_MAX_AGE`, `_LONG_HOLD` | **project** (policy). The readers' configuration is host-wide (below) |
| health (`healthchecks.go`) `redis-password` | `REDIS_HOST`, `REDIS_PASSWORD_FILE`, `LegacyPasswordInUse(os.Getenv)` | **project** (per broker) |
| health `installed-versions` | `herdrBin(os.Getenv)`, `repoDir(os.Getenv)`, `manifestPath(os.Environ())`, `skillsMatch(os.Environ())` | **project** for herdr bin and manifest (`ROLES_TOML`, see the manifest note below). **Host** for the agentbus repo (`AGENTBUS_REPO`) |
| health `tracked-secrets` | `os.Getwd()` | **project**: `AGENTBUS_PROJECT_DIR` |
| health `runaway` | `runawayThresholdsFromEnv(os.Getenv)` | **project** |
| health `agent-launch` (`healthchecks.go`, `manifestPath(os.Environ())`) | `ROLES_TOML`, else `AGENTBUS_REPO`/the binary's directory (`roles.go` `manifestPath`) | **project** `ROLES_TOML`, via `Environ()`; its built-in default is today's host resolution |
| health `model-drift` (`healthchecks.go`, `realDoctorDeps(os.Environ())`) | the manifest path as above | **project**, via `Environ()` |
| `realDoctorDeps` (`doctor.go`) | `os.Getenv("CLAUDE_CONFIG_DIR")` (transcript root) | **host**, like the quota readers. It is read inside `realDoctorDeps`, so the function moves into the static test's scope and takes the value from its caller |
| health builder (`realHealthConfig`), `dispatchcmd.go` | `service.Detect`/`ManagerBin`/`PathsFrom(os.Getenv)`: `AGENTBUS_SERVICE_MANAGER`, `XDG_*`, `HOME` | **host** |
| stream caps (`bus/bus.go`) | `AGENT_BUS_REPORT_MAX`, `AGENT_BUS_VERDICT_MAX`, `AGENT_BUS_REPORT_FULL_MAX` | **host**: read from the process. A project env file that sets one gets a warning in its log: per-project caps are not supported under the host dispatcher |
| quota readers (`quota/config.go`, `quota/claude.go`) | `XDG_CONFIG_HOME`, `XDG_CACHE_HOME`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME` | **host**: the shared collector. A project env file that sets one is **ignored with a warning** in that project's log (the collector reads one set of sources for the host), never refused: refusing would disable dispatch for a setting that changes nothing in that project's policy |
| health `herdr-probe` | the runtime | **project**: the project's own session-bound runtime |

**The roles manifest.** `manifestPath` reads `ROLES_TOML`, else `<AGENTBUS_REPO or the binary's
directory>/roles.toml`. No `services/<p>.env` carries `ROLES_TOML` today (they hold `HERDR_*`,
`LANG`, `PATH`), so the dispatcher's manifest is resolved host-wide today. Under the host
dispatcher, `ROLES_TOML` is project-scoped: `up` adds it to the keys it records (`CollectEnv`) when
its own environment sets it. When it is unset, the built-in default is exactly today's resolution
(`AGENTBUS_REPO`, host-scoped, else the binary's directory), so a project without it behaves as it
does now. The two-project manifest test therefore has one project with `ROLES_TOML` set and one
without, resolving to the host default.

**`PATH`, `LANG` and other process keys in project files.** Today's env files carry `PATH` and
`LANG`, and the missions unit still loads them from the same file: the conductor needs the
project's `PATH` to run its tests. They stay in the project file for that reason. The host
dispatcher treats them as host-only: it never reads them from a project file, without a warning,
because every existing file has them and they are still meaningful to the conductor. The warning
is kept for host-scoped keys that only the dispatcher would have read (stream caps, quota reader
locations).

The host env file (`services/host.env`, same rules) holds what belongs to the process: `PATH`,
`LANG`, `XDG_*`, `HOME`-derived paths, the stream caps, the quota readers' locations, and the
default broker for `service status`. An invalid project env file disables that project with the
state `failed` and reason `invalid configuration`, and is never retried until the file changes. An
invalid host env file is the process's own invalid configuration: exit 0.

**Logs are per project, never the process's stderr.** `dispatch.Config.Log` defaults to
`os.Stderr` when nil (`filled`), which in one process would merge every project's decision log. In
host mode, the runner always sets `Log` (and the health loop's writer) to that project's log
through its own `lastError` wrapper. A nil `Log` reaching a runner is a construction error, not a
default. The process's stderr carries only host-level lines (scan, collector, process start and
stop), each prefixed `host:`.

### Discovery, enrolment and deletion

- **Enrolled** = `services/<p>.env` exists, `p` passes `ValidName`, and there is no
  `services/<p>.disabled`. The scan reads that directory every 5 s: cheap, local, and it works for
  any broker. No `SCAN` of the keyspace.
- **Served** additionally needs the `{p}:services` marker to exist with `dispatch` in its kinds.
  The runner checks this on every scan (`HEXISTS`, one per project).
- **Lease acquisition is guarded by the marker, atomically.** A new fenced script acquires
  `{p}:dispatch` only while `{p}:services` holds the `installed` field. This closes a race that
  exists today: `superviseDispatch` checks `HasKeysBesidesDispatch` and then calls `SET NX` as two
  separate steps. A runner can then never recreate a deleted project's lease, whatever the timing.
  The renew script is unchanged. Renewal of an existing key does not resurrect anything, because
  `busmon --delete` deletes the lease too.
- **Marker gone** → the runner stops (cancel, `ReleaseDispatch` CAS, which is a no-op if already
  deleted) and publishes nothing, because `PutServiceState` is already `EXISTS`-guarded. The runner
  stays stopped until the marker reappears, which only `up` writes.
- `busmon --delete <p>` and `service remove` delete `services/<p>.env` (and the missions unit) before
  the keys, as they delete units today. The host dispatcher then has nothing left of `p` locally.

### Restart budget and exit codes

Two levels, each with today's rule (0 final, 1 restarted, 5 failures in 10 minutes give up):

- **The host dispatcher process.** Exit 0: stopped by the manager, invalid host configuration,
  nothing enrolled for 10 minutes, or its own budget spent. Exit 1: everything else, which in
  practice means a panic that escaped `safeGo` or a failure to start (the services directory is
  unreadable). Per-project faults never make the process exit. A broker that is down for every
  project is per-project backoff, not an exit. The systemd unit keeps `StartLimitBurst=5`
  /`StartLimitIntervalSec=600`, and the process counts its own starts for launchd, as today.
- **A project runner.** A failure is: `dispatch.Run` returns an error other than lease lost or
  stopped, a recovered panic, or the watchdog's stuck verdict. Each failure is counted in that
  project's existing budget file (`BudgetFile(p, dispatch)`). Backoff runs 1 s doubling to 30 s
  (today's `dispatchRetryMin/Max`). When the budget is spent, the runner publishes `gave-up` with
  the same detail as today ("run agentbus up p to try again") and stays stopped. `up` resets the
  budget file, and the scan sees the reset and restarts the runner. Lease lost while the project
  still exists is not a failure: re-acquire with backoff, as today.
- **Standby.** Another holder of `{p}:dispatch` (a legacy unit, a tab, another host) → the runner
  publishes `standby`, polls every 5 s (today 15 s; shortened so a hand-over is quick) and takes
  over once the lease is free.

### Fault isolation (option A's contract)

Every rule below needs a test that would fail if it were broken:

1. **One goroutine helper.** Every goroutine the runner and `dispatch.Run` start goes through
   `safeGo(project, …)`, which recovers, logs to that project's log and crash file, and reports a
   failure to the runner.
2. **A deadline on every external call.** Redis commands use the client's read and write timeouts
   (blocking reads have their explicit `BLOCK`). Herdr calls use `herdrCallTimeout`. The observe
   pass keeps its 5 s budget. The quota collector keeps a per-reader timeout and runs outside every
   runner.
3. **Watchdog, over both loops.** Each runner records two progress marks: the last completed
   reconcile pass, and the last completed health pass (or skip). Health runs in its own goroutine,
   and a hung check would otherwise leave the project `ready` with its health frozen. Either mark
   older than its limit while the lease is held is a stuck verdict: 2 min (24 ticks) for reconcile,
   and the health tick plus twice its `PassBudget` for health. On a stuck verdict the runner stops
   renewing, cancels the run, counts a failure, publishes `failed` / `stuck: <loop> made no
   progress in <d>`, and starts a fresh runner (new token) after backoff. The old goroutines are
   abandoned. Fencing guarantees they write nothing; the residual is at most one extra wake line.
   The renewal goroutine needs no mark, because a renewal that stops lets the lease lapse, which
   the run already treats as lease lost. The cmd tail needs none either: it only shortens waits.
4. **Separate clients and sessions.** No Redis client, herdr runtime, log writer or budget is
   shared between projects.
5. **No process environment for a project setting.** Project settings flow through
   `ProjectConfig.Getenv`, following the inventory above, checked by a scoped test (see Testing).
6. **No shared writer.** Each project's decision log, health log and crash output go to its own
   files (see "Logs are per project").

### Quotas and pause

- **Collection is host-wide.** The quota collector runs the readers once a minute, exactly as one
  dispatcher does today, and keeps the latest `[]quota.Reading`. Each runner's quota pass asks the
  collector for the latest readings instead of reading itself. Readings are account-scope by
  construction, and today every dispatcher on the host already reads the same sources, so sharing
  them changes no result.
- **Policy stays per project, unchanged.** `{p}:budget` publishing, warn once, holds (`{p}:quota`),
  the coordinator's quota pause and unpause, long-hold messages and resume directives all remain
  inside that project's runner, fenced by its token, with that project's knobs.
  `AGENT_BUS_QUOTA=off` in a project's env file turns its policy off; collection runs while at
  least one served project wants it.
- **Pause stays per project.** `{p}:pause` is read at the top of each project's pass, as today.
  Nothing host-wide pauses.

### Health

- Per-project health loops stay inside each runner (`{p}:health*`, fenced per project). The
  `services` check reads the dispatch record that the runner publishes, and treats `stuck` and
  `gave-up` as `fail`.
- Every check's inputs come from that project's `ProjectConfig`, per the inventory. The checks
  affected:
  - `redis-password`: the project's broker host, password file and legacy-password test;
  - `installed-versions`: the project's herdr binary and roles manifest; the agentbus repository
    is host-wide;
  - `tracked-secrets`: `AGENTBUS_PROJECT_DIR`, not the process's working directory;
  - `runaway`: the project's thresholds;
  - `herdr-probe`: the project's session-bound runtime;
  - `service-env`: the project's env file mode, as today.

  A project whose `AGENTBUS_PROJECT_DIR` is missing (an env file written before this change)
  reads `tracked-secrets` as `unknown` with "project directory not recorded; run agentbus up
  <p>", never as the host dispatcher's working directory.
- Host-wide chores run once in the host dispatcher. Today that is the model table refresh, which
  keeps its host-local lock because a legacy unit may still run it during migration. Usage
  refresh stays per project, because it publishes per project.

### What `{p}:services` records

`ServiceRecord` for kind `dispatch` gains additive fields:

- `mode`: `host` or `project`;
- `served`: the number of projects the host dispatcher serves.

`pid` is the host dispatcher's pid, and `log` stays the project's own log path. The runner writes
the project's log (`LogFile(p, dispatch)`) and crash file, so busmon's F3 reads the right file
unchanged. The states are today's (`starting`, `ready`, `waiting-herdr`, `standby`, `failed`,
`gave-up`, `stopped`). The record is still written only through the `EXISTS`-guarded script.

The `installed` marker keeps `kinds` and `host`. `kinds` still lists `dispatch` for a project served
by the host dispatcher: the marker says what the project wants, not which process provides it.

### `agentbus up` / `down` / `service`

- **`up <p>`** writes `services/<p>.env` and the marker as today, removes `services/<p>.disabled`,
  and resets `p`'s dispatch budget. It installs `agentbus-dispatch` (systemd) or
  `dev.agentbus.dispatch` (launchd) if missing, and starts it if inactive. It then waits up to 20 s
  for `{p}:dispatch` to be held, and prints `dispatch: served by the host dispatcher (pid N, K
  projects)`, or `dispatch: enrolled, not served yet (state: <state>, see agentbus service logs
  dispatch)`. Missions is unchanged. An `up` never restarts the host dispatcher for one project's
  new env file: the scan reloads that project's runner when the env file's mtime changes, which
  restarts that runner only.
- **`down <p>`** writes `services/<p>.disabled`; the runner releases its lease within one scan and
  publishes `stopped`. `down` also stops `p`'s missions unit as today. It never stops the host
  dispatcher. Its message names what stayed running: `the host dispatcher still serves K other
  projects`.
- **`service start|stop|restart dispatch`** under `--project p` act on `p`'s runner through the
  `.disabled` file (restart = touch the env file, which restarts the runner). `service
  start|stop|restart --host dispatch` acts on the process, and prints the projects a stop or
  restart affects. `service status` lists every enrolled project: runner state, lease held, budget
  left, and missions unit state.
- **The workspace hook** (`plugin workspace-event`) starts the host dispatcher if it is stopped and
  the project is enrolled and present. It never installs anything, as today.
- **`--tabs`** and no-manager mode: unchanged, one `agentbus dispatch` tab per project. The host
  dispatcher is never started as a hidden process.

### busmon

- `dispatch up/down` in the header: unchanged (lease presence).
- Services overlay (F3): the dispatch row shows `host dispatcher · pid N · serves K projects` when
  `mode=host`. The log pane reads `log` as today. A one-line note says that restarting it affects
  every project.
- `busmon --delete <p>`: removes `services/<p>.env` and `<p>.disabled` along with the missions unit,
  then the keys. It never stops the host dispatcher.

### Migration from per-project units

The hand-over is lease-safe because both sides already respect `{p}:dispatch`:

1. The new binary installs the host dispatcher unit (via `up` or `service migrate`) and starts it.
   For a project whose legacy `agentbus-<p>-dispatch` still holds the lease, the runner goes to
   `standby`.
2. `up`/`migrate` then stops and removes the legacy dispatch unit. On SIGTERM it releases the lease
   (CAS), and the runner takes over at its next 5 s standby poll. If the old process is killed
   instead, the lease lapses in at most 15 s. Two dispatchers never hold the lease at once, so
   nothing is double-woken.
3. A project not migrated keeps its legacy unit, which behaves exactly as today. **No step-aside
   rule:** a legacy `service run dispatch` never exits because a host dispatcher exists. Being
   *installed* says nothing about being *running*: the host dispatcher may be stopped, may have
   given up, or may have disabled `p`. A legacy unit that stepped aside on that evidence would
   leave `p` unserved (fail-open). Contention is already safe: both sides respect `{p}:dispatch`,
   the loser waits in `standby`, and either one takes over when the other goes away. The only way
   a legacy unit stops is `up`/`migrate` removing it (step 2), after the host dispatcher's runner
   is in `standby` for `p`. If the host dispatcher is not active, `migrate` refuses that project
   and leaves its legacy unit in place.
4. `agentbus service migrate` performs 1–2 for every enrolled project and prints a table (project,
   before, after). `setup.sh update` prints a hint to run it and never migrates on its own: setup
   owns only what it wrote, and the units are `up`'s.
5. Rollback: `agentbus up <p> --project-units` reinstalls the per-project dispatch unit and writes
   `<p>.disabled` for the host dispatcher, with the same lease hand-over in reverse.

The missions units are untouched by the migration.

## Testing Decisions

A good test here exercises the host dispatcher through `Serve` with injected `Sources`. It never
reaches into a runner's internals, and it observes only Redis state (leases, wake records,
`{p}:services`) and fake runtime calls. Redis cases use the disposable broker
(`AGENTBUS_TEST_REDIS_URL`) and skip without it; nothing touches the production broker, the user's
systemd or herdr.

Seam: one, `Serve(ctx, Sources)`. Fakes: a projects lister over a temp services directory, a
runtime factory whose runtimes can be told to block forever or panic, a quota collector that counts
reads, a manual clock where possible.

Cases (each is an acceptance criterion):

- **Isolation.** Project A's runtime blocks forever inside `Inspect`. Project B's owed agent still
  gets its wake record and prompt within two ticks. After the watchdog period, A's lease is
  released, its state reads `failed`/`stuck` and one failure is counted.
- **Panic.** A's runtime panics. The process survives, B is unaffected, A's budget counts one
  failure, and A restarts after backoff.
- **Budget.** A fails 5 times in 10 minutes and publishes `gave-up`. Resetting its budget file (what
  `up` does) restarts it at the next scan.
- **Deletion.** Delete every `{p}:*` key while the runner holds the lease. The runner stops, and
  after several scans no `{p}:*` key exists (no lease, no services record, no health key). The
  guarded acquire returns "not installed" for a project without a marker.
- **Disable/enable.** `<p>.disabled` stops one runner, its lease is released and `stopped` is
  published; removing the file restarts it. Other runners keep their tokens unchanged.
- **Env reload.** Touching `<p>.env` with a new `AGENT_BUS_ESCALATE_TO` restarts only `p`'s runner,
  and its next escalation goes to the new target.
- **Per-project herdr session.** Two projects with two sessions: every fake runtime call carries
  its own project's session and socket, never the other's.
- **Quota sharing.** Three served projects cause one collector read per minute, not three. A
  project with a lower hold threshold holds while the others do not.
- **Standby hand-over.** A token placed in `{p}:dispatch` by a fake legacy holder puts the runner in
  `standby`. Releasing it lets the runner acquire within one standby poll, and at no instant do two
  tokens act.
- **No process environment for project settings.** A static AST test, in the style of
  `TestEveryDispatchedCommandIsInTheTable`. Its scope is the files that build a runner's
  dispatcher, not `dispatch/` (which has no reads to find): the host dispatcher's own files, plus
  the builder functions `serviceQuota`, `realHealthConfig`/`realHealthChecks`, `realDoctorDeps` and
  the runner's connect path. In that scope, any `os.Getenv`, `os.LookupEnv`, `os.Environ` or
  `os.Getwd` call, and any `os.Getenv` passed as a value, fails unless it is listed in an allowlist
  that names the host-scoped keys of the inventory. A behaviour test backs it: two projects whose
  env files differ in broker host, herdr bin, escalation target, quota hold, runaway threshold,
  roles manifest and project directory. Each project's health checks (`agent-launch` and
  `model-drift` included) and decisions see only its own values, while the process environment
  holds a third, distinct value for each key. A third project whose file leaves those keys unset
  sees the built-in defaults, never the process environment's values.
- **Goroutines.** A second static test fails on a bare `go` statement in `dispatch/` or the host
  dispatcher's files that does not go through `safeGo`.
- **Per-project logs.** Two runners each log a decision. Each line lands in its own project's log
  file, and none in the process's stderr. A runner built with a nil `Log` fails construction.
- **Health watchdog.** A health check that blocks past its context with the reconcile loop healthy
  yields the stuck verdict `health`, one failure counted, a fresh runner, and other projects
  unaffected.
- **Legacy contention, fail-closed.** With the host dispatcher unit installed but not running, a
  legacy `service run dispatch` acquires the lease and serves. With the host dispatcher running,
  the legacy unit holds the lease and the runner waits in `standby`; nobody exits. `migrate` with
  the host dispatcher inactive refuses and leaves the legacy unit.
- **Exit codes.** `service run-host`: invalid host env → 0; manager stop → 0; nothing enrolled for
  the idle window → 0; unreadable services directory → 1. Prior art:
  `cmd/agentbus/servicerun_test.go`.
- **up/down/migrate.** With fake `systemctl`/`launchctl`, in the style of
  `service/manager_test.go` and `tests/installer_test.sh`: `up` installs the host unit once for two
  projects; `down a` leaves the host unit active; `migrate` removes legacy dispatch units and never
  touches missions units.

Prior art: `dispatch/loop_test.go` (crash hooks, fenced writes), `cmd/agentbus/servicerun_test.go`
(exit rules, standby), `service/manager_test.go` (fake managers), `bus` fenced-script tests
(`ErrNotHolder`).

## Out of Scope

- A host-wide conductor (rejected above).
- Running the host dispatcher as a system (root) service or across users.
- Discovering projects from the broker (`SCAN`); enrolment stays local and explicit.
- Any change to `Decide`, the wake line, the cmd protocol, `subscribe`, or the lease timings
  (`PX 15000`, renew 5 s).
- Starting at login (still never).
- Cross-host coordination beyond what the per-project lease already gives.
- Template units (option C, closed by the decision).

## Further Notes

- **Decided, 2026-10-26:** the goal is fewer processes and one quota reading, so A, not C
  (Bernard's answer `1793035843860-0` to master's ask). No question remains open in this spec.
- **Residual risks accepted under A:** a goroutine stuck in a call that ignores its context leaks
  until restart. A late-returning stuck prompt can type one extra wake line. Memory pressure from
  one project is felt by all. The watchdog and health make each of these visible; none can corrupt
  state, because every write is fenced per project.
- **Decision note:** [2026-10-26-host-dispatcher](../../memory/decisions/2026-10-26-host-dispatcher.md)
  revises `2026-10-14-project-services` for dispatch only; the conductor stays one unit per project.
- **Order of work:** (1) explicit configuration (`ProjectConfig`, herdr runtime with
  session fields, `bus.Connect` with explicit values) plus the no-env static test. This is a
  behaviour-preserving refactor that helps even without the rest. (2) `safeGo` plus the static
  test. (3) The guarded acquire script. (4) The quota collector as an injected source. (5) `Serve`
  with runners, watchdog and scan. (6) `service run-host`, unit, `up`/`down`/`service`/busmon.
  (7) `migrate` and the legacy step-aside rule.
