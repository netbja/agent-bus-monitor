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

`systemdHolderUnit` writes three more lines in `[Service]`:

```
OOMPolicy=continue
MemoryMax=<limits.holder_memory_max>      # default 70%  (systemd: % of physical RAM)
MemorySwapMax=<limits.holder_swap_max>    # default 4G
```

- **`continue`:** after an OOM kill inside the unit, systemd logs it and the unit keeps running.
  The killed process's parent (say, a client's shell tool) sees `signal: killed`.
- **`MemorySwapMax` matters as much as `MemoryMax`.** Without it, a runaway fills swap first and
  the host thrashes before the kill (32 GB on 2026-11-25).
- **No `MemoryHigh`:** it throttles every pane of the holder, not only the runaway. The early
  warning is §3.1's notice.
- **No CPU cap on the unit:** CPU starvation kills nothing. C's time cap and §5 cover it.
- **An upgrade, not a restart.** Units are never restarted to upgrade (herdr-exit spec 4.4a). The
  next build's `agentbus up` writes a new unit with these lines, and the old build drains as it
  does today.
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
  the agents still in the old build and the command that ends the drain: `agentbus relaunch
  <agent>` for each, or the person's `service restart holder --kill-agents` for that build. The
  `fail` reaches master once, through the same notice path. busmon's header shows `2 holders`
  while two builds answer.

The old build is never stopped automatically. Its terminals are agents' live sessions, and ending
them is master's or the person's act.

## 4. Cap B: one agent's terminal

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
  - **the largest process:** its name and RSS.
- **busmon** shows `mem` and `cpu` columns in the agents table, as in `4.1G 180%`. A cell turns
  amber at `limits.warn_at` of the agent's cap B, or, without cap B, of cap A divided by the
  number of terminals. The agent detail names the largest process.
- **master is told once per episode** when an agent crosses `warn_at`, on the context alert's
  pattern (`contextalert.go`): an episode mark, then one notice naming the agent, its largest
  process, its use and its cap. Sentinel is not woken for a warn. It reads `holder_memory` at its
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
| `limits.holder_memory_max` | `70%` | `agentbus up` (unit text) | when a unit is written |
| `limits.holder_swap_max` | `4G` | `agentbus up` (unit text) | when a unit is written |
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
