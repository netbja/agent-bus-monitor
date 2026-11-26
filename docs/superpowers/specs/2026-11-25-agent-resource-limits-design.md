# Agent resource limits (agent-resource-limits)

Status: spec r2, for review (subject `agent-resource-limits-spec`). Not built. Base: origin/dev
`3d2dfa79`. Author: architect, from master's directive (task `agent-resource-limits-spec`, cmd
`1795634829231-0`), on the person's request after the 2026-11-25 20:16 OOM. No new dependency.
The questions for the person are last (§11).

r2 answers both rejects of r1:
- **Cap C now stays inside the holder** (reviewer-codex, verdict `dn4jk53b38lk`, (1); foureyes,
  verdict `dn4jk534du41`, (1)). In r1, `agentbus run --cap` used a `systemd-run --user --scope`.
  That moved a test out of the holder unit, so cap A, cap B, per-agent accounting and the
  killed-agent notice missed the biggest consumer. Now cap C is a child cgroup of the agent's
  terminal cgroup, or a process group with `RLIMIT_AS` while there is no cap B. `systemd-run` is
  used only in a shell that no holder runs (§6).
- **The OOM victim is not promised** (reviewer-codex, (2)). The kernel picks the victim by its
  OOM score. That is usually the largest process, but not always. The spec now says only what the
  kernel's data can show: the cgroup in which a kill happened. Within a cap-C cgroup, that is the
  command itself. `agentbus run` raises its command's `oom_score_adj`, so the command is the
  preferred victim at cap A and cap B too (§2).
- **§1:** the CPU figure is the whole holder unit's total, not the test's (foureyes, (2)).
- **§3.1:** the example is fixed (foureyes, (3)).
- **§4:** a terminal enters its cgroup at clone (`SysProcAttr.UseCgroupFD`), not after start
  (foureyes's suggestion).

## 1. What happened, measured

From this host's user journal and `systemctl --user show`, read on 2026-11-25 after the incident:

- `20:16:07 agentbus-holder.agent-bus-monitor.v0.33.0.b223191132.service: The kernel OOM killer
  killed some processes in this unit.`
- `20:16:12 … Failed with result 'oom-kill'.` Then the totals of the whole unit, every agent
  included: `Consumed 1d 1h 53min CPU time over 21h 46min wall clock time, 56.4G memory peak,
  31.9G memory swap peak.`
- Both holder units today: `OOMPolicy=stop`, `MemoryMax=infinity`, `Delegate=no`. The host has
  62 GB of RAM and 39 GB of swap. The user manager delegates `cpu io memory pids`.

**The cause is one unit line.** The kernel OOM-killed process(es) in the unit. Then systemd
applied the unit's `OOMPolicy=stop`, its default for every service. That stopped the whole holder
unit, and `KillMode=control-group` took every agent terminal with it. So the holder was not killed
for lack of memory. It was stopped because something inside it was.

Two more facts make it worse. Nothing capped the unit, so it used 56 GB of RAM and 32 GB of swap
before the kill, and the host thrashed. And nothing showed per-agent memory, so nobody saw it
grow. No command an agent ran had a time cap. The journal does not say how long the runaway
itself ran.

The v0.33.0 and v0.34.0 holders running side by side is the upgrade's designed drain
(`holder_draining` warns about it). But the warning never ends, and it does not say how long the
drain has lasted.

## 2. The model: three nested caps

```
holder unit (systemd service)              cap A: whole holder; OOMPolicy=continue
 ├─ holder                  (leaf: the holder process)
 ├─ term-<id>               cap B: one agent's terminal, everything under it
 │    ├─ client             (leaf: the pane's shell and client)
 │    └─ cmd-<n>            cap C: one `agentbus run --cap` command
 └─ term-<id>               cap B
```

Each cap counts everything below it, so a command is charged to C, B and A. When a cap is hit,
the kernel OOM-kills a process **inside that cgroup**. The kernel chooses it by OOM score
(`oom_badness`: mostly resident memory and swap, plus `oom_score_adj`). That usually means the
largest process, but it is not guaranteed. Nothing outside the cgroup is touched.

- `agentbus run` sets its own `oom_score_adj` to `+500` before it starts the command, which
  inherits the value. Raising your own value needs no privilege. Go cannot run code between fork
  and exec, so the value is set on `agentbus run` itself, not on the child. So when cap A or B is hit, the capped command is the
  preferred victim, ahead of a client or the holder.
- What the kernel tells us is the `oom_kill` count in each cgroup's `memory.events`, not the
  victim's pid. So attribution names the cgroup, which identifies the terminal (B) or the command
  (C). It never names a process as "the one killed" unless that process is the only command in
  a `cmd-<n>`.

The caps:

- **Cap A, the holder unit.** It is enforced, and no agent needs to cooperate. It gets
  `OOMPolicy=continue`, `MemoryMax` and `MemorySwapMax`. This alone would have kept every pane
  alive on 2026-11-25.
- **Cap B, one agent's terminal.** It is enforced. It is a delegated child cgroup with its own
  `memory.max`, so one agent's runaway cannot take the others' share of A.
- **Cap C, one command.** It is advisory: the agent must use `agentbus run --cap`. It makes the
  kill earlier, exact in attribution, and explained to the agent. The skills require it for
  every test.

A cap that relies on the agent alone fails silently when it is skipped (lesson: advisory versus
enforced). So A and B are the protection. C adds precision.

## 3. Cap A: the holder unit

`systemdHolderUnit` writes four more lines in `[Service]`:

```
OOMPolicy=continue
MemoryHigh=<limits.holder_memory_high>    # default 60%  (the person's answer to Q1)
MemoryMax=<limits.holder_memory_max>      # default 70%  (systemd: % of physical RAM)
MemorySwapMax=<limits.holder_swap_max>    # default 4G
```

- **`continue`:** after an OOM kill inside the unit, systemd logs it and the unit keeps running.
  The killed process's parent (say, a client's shell tool) sees `signal: killed`.
- **`MemorySwapMax` matters as much as `MemoryMax`.** Without it, a runaway fills swap first and
  the host thrashes before the kill (32 GB on 2026-11-25).
- **`MemoryHigh` at 60 %, the person's choice (Q1).** r2 proposed none, because it throttles
  every pane of the holder, not only the runaway. The person chose a soft throttle below the hard
  cap: past it, the kernel reclaims and slows the whole holder before anything is killed. busmon's
  warning when the holder crosses it is §3.1's notice (step 3). `off` writes no line.
- **No CPU cap on the unit:** CPU starvation kills nothing. C's time cap and §5 cover it.
- **An upgrade, not a restart.** Units are never restarted to upgrade (herdr-exit spec 4.4a). The
  next build's `agentbus up` writes a new unit with these lines, and the old build drains as it
  does today.
- **The limits apply live.** `agentbus up` rewrites the current build's unit at each run when its
  text changed, then `daemon-reload`. A reload applies `MemoryHigh`, `MemoryMax` and
  `MemorySwapMax` to the running holder at once, with no restart (checked on a throwaway unit:
  `memory.max` changed, same PID). So a limit the person lowers takes effect at their next `up`,
  and one below the holder's current use makes the kernel reclaim or kill at once. The keys' help
  says so.
- **launchd** has no equivalent. macOS: §6 and §11 Q3.
- **The supervised holder** (no service manager) has no cgroup. It gets nothing from A or B, only
  C's fallback and §5. Its `holder` health detail says so.

### 3.1 The holder's own high-water notice

dispatch already asks the holder for each terminal's processes on every pass (`Reply.Procs`). On
Linux, the holder adds to each reply four reads of its own unit cgroup: `memory.current`,
`memory.max`, `memory.swap.current`, and the `oom_kill` count of `memory.events`. Those are small
file reads, with no new process. A holder without a cgroup reports none.

A new health check, `holder_memory`:

| Status | When |
|---|---|
| ok | below `limits.warn_at` (default 0.8) of the unit's `MemoryMax` |
| warn | at or above it. Detail: `holder build <b>: 35.6G of 43.4G cap (82 %), most: coder 21.0G` (per-terminal figures from §4 when cap B is on, else §5's RSS sums) |
| warn | the unit's `oom_kill` count rose since the last pass. Detail: the terminal(s) whose own `oom_kill` rose (§4), or `terminal unknown` without cap B |
| unknown | no cgroup (launchd, supervised) |

As a health check, it shows in busmon, sentinel reads it, and the dispatcher's existing
warn-to-master notice tells master **once per episode**. There is no new notice mechanism.

### 3.2 A stale second holder

`holder_draining` already warns while an older build holds terminals. Two changes:

- Its detail names the age of the drain: `build v0.33.0: 3 terminal(s), draining for 21h`.
- Past `limits.holder_drain_stale_after` (default 6h), its status becomes `fail`. The detail names
  the agents still in the old build and the command that ends the drain: `agentbus holder drain
  <project>`, which relaunches them onto the current build one at a time (`agentbus reload <agent>`
  for one). Never `service restart holder --kill-agents`: it restarts the current build's holder and
  kills its agents, leaving the old build as it was (build step 3b, foureyes). The
  `fail` reaches master once, through the same notice path. busmon's header shows `2 holders`
  while two builds answer.

The old build is never stopped automatically. Its terminals are agents' live sessions, and ending
them is master's or the person's act.

## 4. Cap B: one agent's terminal

**Replaced by §12 (2026-11-27, the person's process model):** cap B is the agent's own scope or
unit, not a child of the holder unit. The attribution, "the agent is told" and live-read rules
below still apply, read from that cgroup.

The holder unit gets `Delegate=memory pids cpu`. At start, on Linux, the holder:

1. reads its own cgroup from `/proc/self/cgroup`;
2. moves itself into a leaf child `holder`. cgroup v2 forbids processes in a cgroup whose
   children use controllers;
3. writes `+memory +pids +cpu` to the unit cgroup's `cgroup.subtree_control`.

**At each terminal launch**, the holder:

- creates `term-<terminal id>` with `memory.max` = `limits.agent_memory_max` (default 30%, read
  against `MemTotal`) and `memory.swap.max` = `limits.agent_swap_max` (default 2G);
- enables `+memory +pids +cpu` in that cgroup's `subtree_control`;
- creates the leaf `term-<id>/client`;
- starts the pane's process directly in that leaf: `SysProcAttr{UseCgroupFD: true, CgroupFD:
  <fd of client>}` (clone3, Go 1.20+, Linux 5.7+). Moving the process after start would race
  with its first forks.

`memory.oom.group` stays 0 everywhere, so a kill takes one process, not the whole cgroup. When
the terminal ends, the holder removes its empty cgroups. The `term-<id>` and `cmd-<n>` cgroups
belong to the person's user, inside the delegated subtree. So `agentbus run` (§6), running inside
the pane, can create `cmd-<n>` itself.

- **It composes with sandboxed-launch.** The pane's process is born in its cgroup, so whatever
  wraps the client inherits it.
- **Attribution.** On each terminal-processes request, the holder reads `memory.current`,
  `memory.peak` and the `oom_kill` count of `term-<id>` and returns them with that terminal's
  `Procs`.
- **The agent is told.** When `term-<id>`'s `oom_kill` count rises and no `cmd-<n>` under it
  explains the rise (an `agentbus run` reports its own, §6), dispatch sends the agent one entry:
  `a process in your terminal was killed at the terminal's memory cap (<cap>). The kernel does
  not say which. Processes gone since the last pass: <names>. Run tests with agentbus run --cap.`
  The kernel gives no victim pid, so the entry names nothing more than what is gone.
- **Live-read.** The holder re-reads the two `limits.agent_*` keys at most once a minute, on a
  terminal-processes request, and rewrites `memory.max` where the value changed. Lowering a cap
  below a pane's current use makes the kernel reclaim or kill at once. That is the person's act,
  and the key's help says so.
- **Fallback.** If any step above fails (no delegation, cgroup v1, a kernel without clone3
  cgroup support), the holder logs it once and runs without cap B. `holder_memory`'s detail says
  `no per-agent caps: <reason>`. A launch is never refused for it.

Rejected: wrapping each client in `systemd-run --user --scope`. A scope leaves the holder unit's
cgroup, so `KillMode=control-group`, cap A and the unit's accounting no longer cover the agent.
It also adds a D-Bus round trip to every launch.

## 5. Visibility per agent

- `ProcInfo` gains `rss_kb`, read from `/proc/<pid>/statm`: one more small read per process that
  the holder already walks.
- dispatch already pulls every terminal's process tree on each pass (`hostruntime.go`, the
  background judgement). From it, dispatch publishes, per agent, beside `BgProcs` in the agent's
  observation fields:
  - **memory:** `term-<id>`'s `memory.current` when cap B is on, which includes every
    `cmd-<n>`; else the RSS sum of the terminal's process tree;
  - **CPU %:** the tree's CPU growth over the pass window;
  - **the largest process:** its RSS and its pid (`top_bytes`, `top_pid`), never its name. The
    observation publishes no process name or argument, the rule `bg_procs` set
    (background-work-visible step 2, `TestObservePublishesTheProcessSource`); master's decision
    on step 3c.
- **busmon** shows `mem` and `cpu` columns in the agents table, as in `4.1G 180%`. A cell turns
  amber at `limits.warn_at` of the agent's cap B, or, without cap B, of cap A divided by the
  number of terminals. The agent detail gives the largest process's size, and its name only when
  busmon reads it itself on the same host (`top_pid`'s `/proc/<pid>/comm`); otherwise the size
  alone.
- **master is told once per episode** when an agent crosses `warn_at`, on the context alert's
  pattern (`contextalert.go`): an episode mark, then one notice naming the agent, its largest
  process's size, its use and its cap. Sentinel is not woken for a warn. It reads `holder_memory` at its
  daily review, as it reads any check.

## 6. Cap C: one command, and how agents run tests

`agentbus run --cap [--mem <size>] [--time <dur>] [--cpu <pct>] -- <command> [args…]`

The command always runs in its own process group, with `oom_score_adj` +500. On `--time`, the
whole group is killed. What enforces `--mem` depends on where `agentbus run` runs:

| Where `agentbus run` runs | Memory and CPU |
|---|---|
| Inside a terminal with cap B (its own cgroup is a `term-<id>/client` the holder made) | It creates `term-<id>/cmd-<pid>` with `memory.max` = `<mem>`, `memory.swap.max` = 0 and `cpu.max` from `<cpu>`, and starts the command in it with `UseCgroupFD`. The command stays under B and A: charged to the agent, ended with the unit, counted in §5. |
| Inside a holder without cap B (step 2 before step 4; the supervised holder; macOS) | `RLIMIT_AS` = `<mem>` on the command. That limits virtual memory, a cruder measure than RSS; it is enforced on Linux and not on macOS. The process stays in the holder's cgroup, so A still covers it. No CPU cap. |
| In a shell that no holder runs (the person's own terminal), Linux with a user manager | `systemd-run --user --scope --quiet --collect -p MemoryMax=<mem> -p MemorySwapMax=0 -p RuntimeMaxSec=<time> [-p CPUQuota=<cpu>]`. There is no holder to escape from. |

`agentbus run` tells a holder from its absence by its own cgroup path: under a holder unit's
`term-<id>/client`, under a holder unit, or neither. It never calls `systemd-run` inside a holder.

- **Defaults.** They come from `limits.command_memory` (8G), `limits.command_time` (30m) and
  `limits.command_cpu` (empty: none). The flags may only narrow them, because an agent cannot
  raise its own cap. A larger value is refused, naming the key.
- **Exit.** The command's own status, or 137 with one stderr line, chosen by what is known:
  - `agentbus run: killed at memory cap 8G` when `cmd-<pid>`'s `oom_kill` count rose. That is
    exact: the cgroup holds only this command.
  - `… at time cap 30m` when the deadline fired.
  - `… killed by signal 9 (memory cap not proven)` otherwise, including under `RLIMIT_AS`, where
    an allocation fails instead of being killed.

  It removes `cmd-<pid>` on exit.
- **`-race`** maps large virtual ranges, so it fails under the `RLIMIT_AS` fallback. Until cap B
  is on, a race run needs `--mem off`, which is allowed only when `limits.command_memory` is
  `off`: the person's choice.

**macOS** has no enforced cap A or B (launchd has none). It gets §5's visibility and notice, and
C's time cap. Its memory cap is §11 Q3.

**How agents run tests.** These are documentation changes, plus one test that enforces them:

- `skills/agent-bus` (all implementers and reviewers), `skills/agent-bus-master` (the release
  check), the reviewers' mutation recipes and `docs/testing-redis.md`: every `go test` line becomes
  `agentbus run --cap -- go test … -timeout <d>`. The explicit `-timeout` matters because Go's
  default of 10m per package binary does not apply once a script sets `-timeout 0`. A mutant run
  uses `--mem 4G --time 5m`.
- A suite test, `TestSkillGoTestLinesAreCapped`, reads `skills/**/SKILL.md` and
  `docs/testing-redis.md`. It fails on any `go test` line in a code block that lacks `-timeout`
  or is not preceded by `agentbus run --cap`.
- `tests/run.sh` (the shell suite) runs its Go helpers the same way.

## 7. Steps, cheapest first

| Step | What | Size | Protects |
|---|---|---|---|
| **1** | Cap A: three unit lines + the two `limits.holder_*` keys + unit-text test | ~40 lines | every pane against a repeat of 2026-11-25, from the next build's `up` |
| 2 | `agentbus run --cap` (process group, time cap, `RLIMIT_AS`, `oom_score_adj`; the no-holder `systemd-run` row) + skills + `TestSkillGoTestLinesAreCapped` | small | tests killed earlier, with the reason, and preferred as the victim |
| 3 | §5 visibility (`rss_kb`, per-agent mem/cpu, busmon columns, master notice) + §3.1 `holder_memory` + §3.2 drain age | medium | seeing it coming |
| 4 | Cap B: delegated terminal cgroups (`UseCgroupFD`), attribution, the agent told; `agentbus run` gains its `cmd-<pid>` row | medium, Linux only | one agent cannot starve the others; exact command attribution |

**The cheapest first step is step 1.** It is one PR with three lines in the unit text, two keys,
and a test of the unit text, and it needs no agent to cooperate.

**Until step 1 ships, the person can protect the running holder now.** That is on the person's
yes, never an agent's act: a drop-in for the current build's unit, then `daemon-reload`, which
does not restart the unit.

```
systemctl --user edit agentbus-holder.agent-bus-monitor.<key>.service
  [Service]
  OOMPolicy=continue
  MemoryMax=70%
  MemorySwapMax=4G
systemctl --user daemon-reload
```

`MemoryMax` and `MemorySwapMax` apply at reload. Before anyone tells the person to do this, the
builder verifies on a throwaway unit that `OOMPolicy` applies at reload too, without a restart.
A cap without `OOMPolicy=continue` would only make the stop come sooner, so no cap is applied
without it.

## 8. Settings (`config/keys.go`)

Every key is `PersonOnly`, because an agent cannot loosen its own cap, and `MachineOnly`, because
RAM differs per host. Sizes use systemd's syntax: `8G`, or `70%` of physical RAM.

| Key | Default | Read by | When |
|---|---|---|---|
| `limits.holder_memory_max` | `70%` | `agentbus up` (unit text) | when a unit is written; live at the reload |
| `limits.holder_memory_high` | `60%` | `agentbus up` (unit text) | same |
| `limits.holder_swap_max` | `4G` | `agentbus up` (unit text; the slice's from §12.3) | same |
| `limits.holder_self_max` | `2G` | `agentbus up` (the holder unit alone, §12.3) | same |
| `limits.agent_memory_max` | `30%` | holder | each launch; live, at most once a minute |
| `limits.agent_swap_max` | `2G` | holder | same |
| `limits.warn_at` | `0.8` | dispatch, health, busmon | each pass |
| `limits.holder_drain_stale_after` | `6h` | health | each pass |
| `limits.command_memory` | `8G` | `agentbus run --cap` | each run |
| `limits.command_time` | `30m` | `agentbus run --cap` | each run |
| `limits.command_cpu` | empty (none) | `agentbus run --cap` | each run |

`off` on a cap key writes no limit at that layer. `OOMPolicy=continue` is always written: it has
no downside to switch off.

## 9. Testing

- **Step 1.** A golden test of `systemdHolderUnit`'s text, for the default keys, for `off`, and
  for both a `%` and a size. Prior art: the unit-text tests in `service/holder_test.go`.
- **Step 2.** `agentbus run --cap`, against real children:
  - one that allocates past a small `RLIMIT_AS` exits non-zero with the "not proven" line;
  - one that sleeps past `--time` exits 137 with the time-cap line, and its whole group is gone
    (a grandchild included);
  - the child's `/proc/<pid>/oom_score_adj` reads 500;
  - the cgroup-path classifier is table-tested on sample `/proc/self/cgroup` lines (in a
    `term-<id>/client`, under a holder unit, outside any).

  The no-holder row is tested with a fake `systemd-run` on `PATH` that records its argv. No test
  touches the person's user manager. The §6 suite test is a plain file read.
- **Step 3.** `holder_memory` and `holder_draining` are pure `health` evaluators with table tests,
  like the existing holder checks. busmon's columns get golden screens. The once-per-episode
  notice reuses the context alert's test pattern.
- **Step 4.** The cgroup code takes a root directory, so its tests run against a temp tree of
  plain files: the write order, the leaf move, `cmd-<pid>` creation and removal, the
  `memory.events` parse, and "an `oom_kill` rise in a `cmd-<n>` is not reported again for its
  terminal". The real delegation is checked once by hand on this host and recorded in the PR:
  `agentbus run --cap --mem 256M` allocating past its cap prints the memory-cap line, while its
  neighbour pane and the holder stay alive; a pane allocating past a 512M cap B gets its notice.
- No test runs a real memory runaway in the suite.

## 10. Out of scope

- Disk and file-descriptor caps; network.
- Killing or restarting a stale holder automatically (§3.2: that is master's or the person's act).
- A CPU cap per terminal. B shows CPU only. `cpu` is delegated so that C can cap it, and B could
  later use the same controller if the person asks.
- systemd-oomd's policy. Fedora's oomd acts on pressure at the slice level, outside this spec.

## 11. Questions for the person

**Q1. How big is the holder's cap (cap A)?**
- **(a) `MemoryMax=70%`, `MemorySwapMax=4G` (recommended).** On this host, that leaves 43 GB for
  all agents and 19 GB for the desktop and the broker. A runaway is killed after 4 GB of swap, not
  32.
- (b) `85%`, swap `8G`: more room for agents, less for everything else.
- (c) No memory cap, only `OOMPolicy=continue`: the panes survive, but the host still thrashes
  before the kill.

**The person's answer (2026-11-25, through master):** (a), plus `MemoryHigh` near 60 % as a soft
throttle, busmon warning when the holder crosses it; the values configurable; the broker and the
desktop stay outside the cap. Built in step 1 (§3).

**Q2. Does each agent get its own cap (cap B, step 4)?**
- **(a) Yes: `30%` of RAM and `2G` of swap per agent (recommended).** About 18 GB here. A normal
  `go test ./...` uses a few GB.
- (b) Yes, at a fixed `16G` on every host.
- (c) No, cap A only. One agent's runaway can then take every other agent's share.

**Q3. On macOS (no cgroups), what stops a runaway?**
- **(a) The notice and `agentbus run`'s time cap only (recommended).** It is simple, and macOS
  hosts are rare here.
- (b) A watchdog: the holder kills the largest process of a terminal over the agent cap, polled
  every pass. It kills late, and it makes the holder a process killer.
- (c) Nothing beyond the notice.

**Q4. The default caps of `agentbus run --cap` (tests, mutants)?**
- **(a) `8G`, `30m`, no CPU cap (recommended).** The full suite fits. A mutant script narrows to
  `4G` and `5m`.
- (b) `4G`, `15m`, `200%` CPU: tighter. The full suite may need the person to raise the keys.
- (c) Per role, in `roles.toml`: more knobs, and one more file to keep in step.

## 12. The process model: one systemd unit per agent (amendment, 2026-11-27)

Status: amendment r1, for review (subject `resource-limits-units`), ahead of step 4. It
replaces §4's design for cap B. Steps 1–3 are built and unchanged, except cap A's place (§12.3).
Source: the person's typed answer to ask `1795734341064-0` (code-intel Q7, 2026-11-27), relayed
here at the person's request. Base: origin/dev `93727f7b`.

### 12.1 What the person decided

- **Target layout:** `agentbus.slice` (cap A) → one unit per agent, `agentbus-agent@….service`
  (cap B = the unit's `MemoryMax` and `MemorySwapMax`, `OOMPolicy=continue`) → `Delegate=yes`,
  so agent-bus code creates the sub-cgroups (an `agentbus run --cap` command, a code-intel
  language server) and writes `memory.max`, `memory.high` and `memory.swap.max` itself. There is
  no `systemd-run` per spawn.
- **Default mode: user units** (`systemctl --user`) plus `loginctl enable-linger`. That needs no
  root, starts at boot, and keeps the person's account, CLI logins and terminals.
- **Later: system units** for a dedicated server, from the same templates and the same delegated
  code path, with hardening (`ProtectSystem`, `PrivateTmp`, `NoNewPrivileges`, optionally a user
  per agent). Credentials and pane handling are open for that mode (§12.7).
- **One interface for the cgroup root** (user unit, system unit, transient scope fallback, none on
  macOS), shared by code-intel, `agentbus run --cap` and cap B. **Until the units exist, the
  `systemd-run --user --scope` fallback.**
- `roles doctor` reports the mode (`system`, `user+linger`, `scope`, `soft`) and whether the
  memory controller is delegated.
- Installing or changing units always needs the person's typed yes (sudo for system mode).

### 12.2 Why §4 changes

§4 put each terminal in a child cgroup of the holder unit (`UseCgroupFD`). The person's model
gives each agent a cgroup systemd owns, outside the holder's lifetime and accounting. Two §4
objections to a scope no longer hold once cap A is on the slice: the scope or unit sits in
`agentbus.slice`, so cap A still covers it, and the holder ends its agents itself (§12.4)
instead of relying on `KillMode=control-group`. The D-Bus round trip per launch is accepted in
scope mode. In unit mode it is one `systemctl --user start`, the cost of a unit per agent.

### 12.3 Cap A moves to the slice

`agentbus.slice` is written by `agentbus up` (unit text, typed yes):
- `MemoryMax`, `MemoryHigh` and `MemorySwapMax` come from today's `limits.holder_*` keys
  (70 %, 60 %, 4G), unchanged in name and default.
- The holder unit gets `Slice=agentbus.slice`, so the holder and every agent share cap A, as
  they do today inside the holder unit.
- The holder unit keeps `OOMPolicy=continue`. It gets its own `MemoryMax=limits.holder_self_max`
  (new key, default `2G`): the holder alone should never need more.
- The broker and the desktop stay outside the slice, as the person answered for Q1.

### 12.4 The cgroup root: one interface, four modes

`cgroot` (a new package, Go, no dependency) answers one question for any agent-bus process: *where
is this agent's delegated cgroup, and may I make children in it?*

```go
type Mode string // "system", "user", "scope", "soft"
type Root interface {
    Mode() Mode
    Delegated() bool                                // memory controller usable in children
    Child(name string, mem, high, swap string) (fd int, path string, err error) // a new leaf
    Usage() (current, max uint64, oomKills uint64, err error)
}
func Self() (Root, error)       // this process's agent root, from /proc/self/cgroup
```

`Self` reads `/proc/self/cgroup`, walks up to the nearest `agentbus-agent@*.service` or
`agentbus-agent-*.scope` cgroup, and checks that its `cgroup.controllers` holds `memory` and
that the person's user can write `cgroup.subtree_control`. With none, or on macOS, the mode is
`soft`: `Child` fails, and callers use their soft path (`RLIMIT_AS` for `agentbus run`, §6;
code-intel's gated soft bound). Every child is a leaf. The agent's own processes sit in the
leaf `client` (cgroup v2 forbids processes in a cgroup whose children use controllers).
`agentbus run --cap` creates `cmd-<pid>`, and code-intel creates `ci-<n>`. Each is started
with `UseCgroupFD`, so the process is born in its cgroup.

### 12.5 The two launch paths

**Scope mode (first: no unit to install).** The holder starts a pane's command as today, as its
own child on the pane's pty (`holder/server.go:446-467`). The only change is the argv:

```
systemd-run --user --scope --quiet --slice=agentbus.slice --unit=agentbus-agent-<p>-<role>-<term>
  -p Delegate=yes -p MemoryMax=<agent_memory_max> -p MemorySwapMax=<agent_swap_max>
  -p OOMPolicy=continue -- agentbus agent-enter -- <client argv>
```

4b's live check confirms that this host's systemd accepts `OOMPolicy` and `Delegate` on a
transient scope. A property it refuses is dropped and named in the doctor line, never silently.

- `systemd-run --scope` execs the command in the caller's process, so the pid is still the
  holder's child. Its pty, `waitLoop`, reaper and group kill are unchanged.
- `agentbus agent-enter` moves itself into the scope's `client` leaf, enables
  `+memory +pids +cpu` in the scope's `subtree_control`, and execs the client.
- If `systemd-run` is missing or fails, the pane starts as today and the mode is `soft`. A launch
  is never refused for it (§4's fallback rule).

**Unit mode (target).** `agentbus-agent@.service` is a template, written by `agentbus up` on the
person's yes:

```
[Unit]
Description=agent-bus agent %i
[Service]
Slice=agentbus.slice
ExecStart=<agentbus full path> agent-shim %i
Delegate=yes
OOMPolicy=continue
KillMode=control-group
Restart=no
```

- **The instance names one terminal: `<project>.<role>.<term>`**, not only the role. Two terminals
  of one role exist at once during a relaunch: the new pane is proven before the old one closes
  (decision 2026-10-27). Two projects share role names too. §12.7 Q1 asks the person.
- **The pty is the holder's; the process is the unit's.**
  1. The holder opens the pane's pty.
  2. It runs `systemctl --user start agentbus-agent@<i>.service`, then
     `systemctl --user set-property --runtime` for `MemoryMax` and `MemorySwapMax` from the
     `limits.agent_*` keys.
  3. The unit's `agentbus agent-shim <i>` connects to the holder's socket. It receives the pty's
     slave end over `SCM_RIGHTS`, with the launch's argv, env and directory, which `cfg.Launch`
     still builds.
  4. The shim moves itself into `client`, does `setsid` and `TIOCSCTTY` on the slave, makes it
     stdin, stdout and stderr, and execs the client. The pid stays the unit's main pid.
  5. The holder holds a `pidfd` of it, sent by the shim, for exit and kill, in place of
     `cmd.Wait`.
- **Ending an agent** is `systemctl --user stop` of its unit: control-group kill, every
  descendant included. The holder's group kill and reaper (`holder/groupkill_linux.go`,
  `reap_linux.go`) apply only to scope mode and soft mode.
- **A holder restart** closes the ptys' master ends, so the clients get `SIGHUP` as today. Their
  units then end on their own, since the client exits. Keeping an agent alive across a holder
  restart (passing the masters on) is not in this amendment.
- **Linger:** `agentbus up` checks `loginctl show-user <user> -p Linger`. When it is off, it
  proposes `loginctl enable-linger` in the same typed-yes step, so user units start at boot and
  outlive the person's logout.

### 12.6 What reads the mode

- **The holder:** at each launch, the mode decides the path (§12.5). Per terminal, the holder
  reads `memory.current`, `memory.peak` and `oom_kill` from the unit's or scope's cgroup, in
  place of `term-<id>`'s (§4's attribution and "the agent is told" are unchanged).
- **`agentbus run --cap`** (§6): `cgroot.Self()`. In `system`, `user` or `scope` mode,
  `Child("cmd-<pid>", …)`. In `soft` mode, `RLIMIT_AS` as built.
- **code-intel** (`2026-11-26-code-intel-design.md` §6): `Child("ci-<n>", budget, 90 % of budget, 0)`,
  or its gated soft bound in `soft` mode.
- **`agentbus roles doctor`:** one line per host, `cgroup mode: user+linger, memory delegated`
  (or `scope`, `soft: <reason>`, `user, linger off`). With `--publish`, busmon's Health page
  shows it.

### 12.7 Questions for the person (ask `1795735397184-0`, in French)

1. **Unit instance per terminal** (`<project>.<role>.<term>`), not per role: a relaunch has two
   terminals of one role for a moment, and two projects share role names. Recommended: per
   terminal.
2. **The pty in unit mode:** the shim receives the pty from the holder over its socket
   (`SCM_RIGHTS`), recommended. The alternative is a `TTYPath=` drop-in per launch, which needs a
   `daemon-reload` at every start.
3. **Cap A on the slice, the holder's own cap 2G?** Recommended: yes. The slice keeps today's
   70 %, 60 % and 4G for the holder and every agent together.
4. **Linger proposed at install?** Recommended: yes, in the same typed-yes step as the units.
5. **Order:** scope mode first (cap B on Linux soon, no install), then unit mode. Recommended:
   yes. The alternative is unit mode directly.
6. **System mode** (dedicated server, hardening, credentials, panes): kept as a later spec,
   recommended. The templates and `cgroot` are written so it adds a mode, not a design.

### 12.8 Steps (replacing step 4 of §7), in series

- **4a `cgroot` and doctor:** the interface, `Self` with all four modes, the doctor line. Tests:
  a fake cgroup tree under a temp dir (`/proc/self/cgroup` read through a seam): each mode
  found, a non-delegated tree is `soft`, a nested `client` leaf is used. No live systemd.
- **4b scope mode:** the holder's argv through `systemd-run --scope`, `agentbus agent-enter`, the
  slice written by `up` with cap A moved and `limits.holder_self_max`, the holder's attribution
  from the scope's cgroup. Tests: the argv with and without `systemd-run`, unit text goldens, a
  fake `systemd-run` that fails gives `soft`, and a live check on this host in the PR.
- **4c `agentbus run --cap` and code-intel** take `cgroot.Child`.
- **4d unit mode:** the template, `agent-shim`, the fd passing, `pidfd`, stop by
  `systemctl --user stop`, linger in `up`. Tests: the shim's fd and argv exchange over a
  socketpair, unit text goldens, a fake `systemctl`, and a live check.
- **Later:** system mode, its own spec.

Each step is its own PR and review. Every unit and slice write is on the person's typed yes.
Service tests fake `systemctl` and `loginctl` (CLAUDE.md).
