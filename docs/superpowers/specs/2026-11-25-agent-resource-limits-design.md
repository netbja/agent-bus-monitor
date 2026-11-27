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

**Replaced by §12 (2026-11-27, the person's process model):** cap B is the terminal's own
scope (step 1) or its cgroup under the delegated holder's `holder/agents/` (step 2). The attribution, "the agent is told" and live-read rules
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
| `limits.holder_self_max` | `2G` | the holder, `holder/self` only (§12.3, step 4d) | at holder start |
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

## 12. The process model: the holder's delegated cgroup tree (amendment, 2026-11-27)

Status: amendment r5, for review (subject `resource-limits-units`), ahead of step 4. r5,
reviewer-codex: the start sweep covers every build's scopes of the project. It stops another
build's only when that build's holder is not active, so a crash before an upgrade leaves
nothing, and a drain keeps the old holder's agents (§12.5). r4: r4, foureyes:
no pane ever lands in `holder/self` (a failed cap B gives an uncapped `holder/agents` leaf), and
the holder stops its scopes at terminal end, at holder stop, and in a start sweep of its own
build's stale scopes (§12.5, §12.6).
- **r3, the person's answers to ask `1795735397184-0`** (all typed, 2026-11-27) change r2's
  target. There is no unit per agent. The holder's own service is delegated, and it builds the
  per-terminal cgroups itself. Transient scopes come first and remain the fallback.
- **r3 also answers reviewer-codex's r1/r2 points:**
  - the start-before-limits race goes away with the per-agent units;
  - scope limits are set before the command runs (§12.5);
  - the scope wrapper's pid contract is stated and tested (§12.5).
- foureyes' r2 nit is in §12.3.
- r2 (foureyes) is kept: no cap of the holder's own in a cgroup where agents still run.

It replaces §4's design for cap B. Steps 1–3 are built and unchanged. Source: the person's typed
answers to asks `1795734341064-0` (code-intel Q7) and `1795735397184-0` (Q1–Q6). Base:
origin/dev `93727f7b`.

### 12.1 What the person decided

1. **Cap A on `agentbus.slice`** (`MemoryMax` 70 %, `MemoryHigh` 60 %, `MemorySwapMax` 4G,
   today's `limits.holder_*` keys) over the holder and every agent.
2. **No per-agent service units.** The holder's own unit (today one per project and build,
   `service/holder.go`) gets `Delegate=yes` and builds its cgroup tree itself:

   ```
   agentbus.slice                                  cap A
    └─ agentbus-holder.<project>.<key>.service     Delegate=yes
        ├─ holder/self                              the holder process, limits.holder_self_max (2G)
        └─ holder/agents/<project>.<role>.<term>    cap B (limits.agent_memory_max, agent_swap_max)
            ├─ client                               the agent's client and its children
            ├─ cmd-<pid>                            agentbus run --cap (§6)
            └─ ci-<n>                               a code-intel language server
   ```

   It is one service in systemd. Logs carry `AGENT=` and `PROJECT=` journal fields. Each agent
   is restarted or inspected through agentbus and busmon, not systemctl.
3. **The spawn:** the holder owns the pty and starts the client directly. It uses `exec.Cmd`
   with `SysProcAttr{Setsid, Setctty, Ctty, UseCgroupFD: true, CgroupFD: <client leaf fd>}`
   (`clone3` with `CLONE_INTO_CGROUP`). The process is born inside cap B, with no shim and no fd
   passing. A drop-in plus a reload per launch is never used. A shim with `SCM_RIGHTS` stays only
   as a fallback idea for a host without delegation, and is not built here.
4. **If the holder process is OOM-killed**, systemd restarts it, and agents are reattached or
   reported in busmon (§12.6).
5. **Linger:** `agentbus up` (which installs the holder unit) shows
   `loginctl enable-linger $USER` as its own line in the same typed-yes summary, saying what it
   means: start at boot, survive logout.
   - `--no-linger` skips it.
   - If polkit refuses, it prints the exact command and continues.
   - `roles doctor` says `linger off: holder stops at logout` when linger is off.
6. **Order:**
   - step 1: transient scopes, one per terminal (cap B), with language servers nested in them,
     behind the cgroup-root interface;
   - step 2: the delegated holder service, swapping the implementation behind the same
     interface.

   Scopes remain the fallback where delegation or the service is unavailable.
7. **System mode is a later spec.** The constraint for this one: the interface, the unit text and
   the holder layout assume no user mode, and nothing outside the user implementation
   hard-codes `--user`, `$XDG_RUNTIME_DIR` or a uid path. Its open questions (credentials,
   per-agent users, pane access) are listed in §12.8.

### 12.2 Why §4 changes, and what stays

§4 already put each terminal in a child cgroup of a delegated holder unit through `UseCgroupFD`.
The person keeps that idea for step 2 and adds three things: the slice for cap A, the holder's own
leaf `holder/self` with its own cap, and the agents grouped under `holder/agents/`. §4's
attribution, "the agent is told" and live-read rules stay. They read the terminal's cgroup,
wherever the mode puts it.

### 12.3 Cap A on the slice; the holder's own cap only in its own leaf

- `agentbus up` writes `agentbus.slice` with cap A, from the `limits.holder_*` keys, on the
  person's typed yes. The holder unit gets `Slice=agentbus.slice`, keeps `OOMPolicy=continue`,
  and gets **no `MemoryMax` of its own** (r2, foureyes: a cap on the holder's whole unit would
  also hit the agents in it).
- **`limits.holder_self_max`** (new, `2G`, PersonOnly, MachineOnly) is the `memory.max` of
  `holder/self` only, written by the holder at start in step 2. No agent is ever in that leaf,
  so the person's 2G holder cap can no longer hit an agent. In step 1 there is no
  `holder/self`, and the key is read but unused, as the doctor line says.
- The slice applies to a holder at its next start. Holder units are per build
  (`service/holder.go`), so `up` writes the new build's unit and never rewrites a running one.
  **The same build key must not be re-upped across this change** (foureyes r2): `up` refuses to
  rewrite a running holder's unit text when the new text adds `Slice=` or `Delegate=`, and says
  to drain or update first.
- `holder-memory` health (step 3a, `holder/unitmem.go`) reads `agentbus.slice` once it exists,
  and the holder unit before that.

### 12.4 The cgroup root: one interface, its implementations

`cgroot` (a new package, Go, no dependency) answers, for any agent-bus process: *where is this
agent's cgroup, and may I make children in it?*

```go
type Mode string // "holder" (step 2), "scope" (step 1), "soft"; "system" later
type Root interface {
    Mode() Mode
    Delegated() bool                     // memory controller usable in children
    Kill() KillMethod                    // cgroup.kill, freeze-and-kill, or none
    Child(name string, mem, high, swap string) (fd int, path string, err error)
    Usage() (current, max, oomKills uint64, err error)
}
func Self() (Root, error)                // this process's agent cgroup, from /proc/self/cgroup
func ForTerminal(t TermKey) (Root, error) // the holder's view of one terminal's cgroup
```

- **The implementations** sit behind one seam. Only the user-mode implementation knows
  `--user`, `$XDG_RUNTIME_DIR` or the uid. System mode adds one, and changes no caller (§12.1
  item 7).
- **`Self`** finds the agent's cgroup from `/proc/self/cgroup`: the nearest
  `holder/agents/<…>` or `agentbus-<project>-<role>-<term>.scope`. It checks that
  `cgroup.controllers` holds `memory` and that `cgroup.subtree_control` is writable.
  - With none of these, or on macOS, the mode is `soft`: `Child` fails, and callers take their
    soft path (`RLIMIT_AS` for `agentbus run`, §6; code-intel's gated soft bound).
  - A root with neither `cgroup.kill` nor `cgroup.freeze` is soft too, for callers that need a
    whole-cgroup kill (code-intel r8).
- **No processes in an inner cgroup** (cgroup v2's no-internal-process rule). The agent's
  processes live in its `client` leaf. `cmd-<pid>` and `ci-<n>` are its siblings.

### 12.5 Step 1: one transient scope per terminal

The holder starts a pane's command as today, as its own child on the pane's pty
(`holder/server.go:446-467`). Only the argv changes:

```
systemd-run --user --scope --quiet --slice=agentbus.slice --unit=agentbus-<p>-<key>-<role>-<term>
  -p Delegate=yes -p MemoryMax=<agent_memory_max> -p MemorySwapMax=<agent_swap_max>
  -p OOMPolicy=continue -- agentbus agent-enter -- <client argv>
```

- **The limits come before the command.** `systemd-run --scope` creates the scope with its
  properties for its own pid, and only then execs the command. No allocation happens outside cap
  B (r3, reviewer-codex).
- **The pid contract** (r3, reviewer-codex). The holder starts `systemd-run`, a process with pid
  P in a new session and group, the pty its controlling terminal (`pty.StartWithSize`: `Setsid`,
  `Setctty`). Then each step execs, keeping the pid:
  1. `systemd-run` execs `agentbus agent-enter`, still pid P (foureyes probed it on this host,
     2026-11-27: `systemd-run --scope` keeps the caller's pid).
  2. `agent-enter` moves P into the scope's `client` leaf, enables `+memory +pids +cpu` in the
     scope's `subtree_control` (now legal, since P has left the scope's root), and execs the
     client, still pid P.

  So `TermInfo.pid`, `waitLoop`'s `cmd.Wait`, the session, the process group and the
  controlling terminal are all the client's, as today. The group kill and the reaper are
  unchanged. A wrapper that forks instead of exec'ing would break this, so the test pins it:
  `/proc/P/comm` is the client's after start, and `cmd.Wait` returns the client's exit.
- **No `systemd-run`, or it fails before the exec:** the pane starts as today, without it. The
  mode is `soft` for that terminal, and a launch is never refused (§4's fallback rule).
- **The scope sits in `agentbus.slice`**, so cap A still covers the agent.
- **Who stops a scope** (r4, foureyes). Today the holder unit's `KillMode=control-group`
  (`service/holder.go`) ends every agent process when the holder stops. A scope is outside the
  holder unit, so its background processes would survive: an `agentbus run --cap` child, or
  anything off the pty's foreground group. So the holder stops scopes itself, with
  `systemctl --user stop <scope>`, which kills the whole scope:
  - **at a terminal's end** (its client exited, or the holder closed it): that terminal's scope;
  - **at holder stop:** every scope of its project and build, in the holder's own shutdown on
    `SIGTERM`, before it exits. The unit has no `ExecStop` today (`service/holder.go`), and
    none is added. A holder killed outright skips this step, and the start sweep catches what
    it left;
  - **at holder start:** a sweep of every `agentbus-<p>-*.scope` of its project, any build
    (r5, reviewer-codex: a holder of build A killed outright, then build B started, would
    leave A's scopes). For each scope, the sweep reads the build key from the name, then:
    - **its own build:** it stops the scope when no live terminal holds it;
    - **another build:** it stops the scope only when that build's holder unit is not active
      (`HolderActive(project, key)`, `service/holder.go`). A live old holder during a drain
      keeps its scopes. An error from the manager ("could not say") counts as active: when in
      doubt, nothing is stopped, and the doctor line names the scope as `stale?`.
  - The scope name holds the build key for that reason. `agentbus-<p>-<key>-<role>-<term>`
    stays under systemd's unit-name length, with the key cut as the holder unit's name cuts it.
- 4b's live check confirms that this host's systemd accepts `OOMPolicy`, `Delegate` and
  `MemorySwapMax` on a transient scope. foureyes probed it on 2026-11-27: it does. A property a
  host refuses is dropped and named in the doctor line, never silently.

### 12.6 Step 2: the delegated holder service

- The unit text adds `Delegate=yes` and `Slice=agentbus.slice`, on the person's yes, with
  linger (§12.1 item 5).
- **At start**, the holder:
  1. reads its cgroup;
  2. creates `holder/self` and moves itself into it;
  3. writes `memory.max` = `limits.holder_self_max` there;
  4. enables `+memory +pids +cpu` down to `holder/agents/`.
- **At each launch**, it creates `holder/agents/<project>.<role>.<term>` with cap B and its
  `client` leaf, and starts the client with `UseCgroupFD`/`CgroupFD` on `client` (§12.1 item 3),
  with `Setsid`, `Setctty` and `Ctty` as `pty` sets today. No `systemd-run`.
- **When the terminal ends**, the holder removes the empty cgroups.
- **Journal fields:** the holder's lines about one terminal go to journald through the native
  protocol (its Unix socket, no library) with `AGENT=<role>` and `PROJECT=<p>`, and to stderr
  when the socket is absent.
- **If the holder process dies** (OOM-killed in `holder/self` included), systemd restarts it.
  Said plainly: **the agents do not survive it today.** The ptys' master ends die with the
  holder, the clients get `SIGHUP`, and the unit's control-group kill on stop ends what is left.
  "Reattached" is not possible while the masters live in the holder. So they are **reported**:
  each one shows as lost in busmon (cleaning-up-agents §3), and a parked or resumable session is
  woken as that spec says. Keeping the masters alive across a holder restart is §12.8 Q-later.
- **No pane ever starts in `holder/self`** (r4, foureyes). In a delegated holder, every spawn
  passes `CgroupFD`. A child forked without it would be born in `holder/self`, so none is.
  - If cap B's cgroup cannot be created or limited, the pane starts in an **uncapped**
    `holder/agents/<project>.<role>.<term>/client` leaf (no `memory.max`): inside cap A, never
    in `holder/self`. The doctor line and busmon say `no cap B: <reason>` for that terminal.
  - A test pins it: a spawn whose cgroup creation fails lands in the uncapped leaf, and
    `holder/self/cgroup.procs` holds only the holder's pid.
- **Scopes are the fallback only for a holder that is not delegated** (an older install, the
  person's `--no-delegate`, a refused `Delegate`). Such a holder has no `holder/self`: it runs
  as today, and step 1's scopes apply.

### 12.7 What reads the mode

- **The holder:** the mode picks the launch path. Per terminal it reads `memory.current`,
  `memory.peak` and `oom_kill` from the terminal's cgroup (scope or `holder/agents/<…>`).
- **`agentbus run --cap`** (§6): `cgroot.Self()`. In `holder` or `scope` mode,
  `Child("cmd-<pid>", …)`. In `soft` mode, `RLIMIT_AS`, as built.
- **code-intel** (its §6): `Child("ci-<n>", budget, 90 % of budget, 0)`, or its gated soft
  bound.
- **`agentbus roles doctor`:** one line per host, for example:
  - `cgroup mode: holder, memory delegated, kill: cgroup.kill, linger on`;
  - `scope`;
  - `soft: <reason>`;
  - `linger off: holder stops at logout`.

  With `--publish`, busmon's Health page shows it.

### 12.8 Open, for later specs

- **System mode:** credentials (the bus password, the forge token, the clients' logins), one
  user per agent, and how the person attaches to panes owned by another user.
- **Agents surviving a holder restart:** the pty masters would have to outlive the holder, by
  passing them to systemd's fd store (`FileDescriptorStoreMax`) or to a second process. That is
  its own design, with its own risks.

### 12.9 Steps (replacing step 4 of §7), in series

- **4a `cgroot` and doctor:** the interface, `soft` and `scope` detection, the kill method, the
  doctor line, linger in the doctor line.
  - Tests use a fake cgroup tree under a temp dir (`/proc/self/cgroup` read through a seam):
    each mode is found, a non-delegated tree is `soft`, a tree without `cgroup.kill` but with
    `cgroup.freeze` reports freeze-and-kill, and the `client` leaf is used. No live systemd.
- **4b scope mode:** the argv, `agentbus agent-enter`, the slice written by `up`, the holder
  unit's own caps dropped, `up`'s refusal to rewrite a running unit across `Slice=`,
  `holder-memory` reading the slice, and attribution from the scope.
  - Tests:
    - the argv with and without `systemd-run`;
    - the pid contract through a fake `systemd-run` that execs (`/proc/P/comm`, `cmd.Wait`);
    - scope stops at terminal end and at holder stop, with a fake `systemctl` recording the
      calls;
    - the start sweep:
      - stops this build's stale scopes;
      - after A was killed outright, B's start sweep stops A's scopes (A inactive);
      - leaves a live A's scopes alone while B runs (a drain);
      - stops nothing when the manager cannot say;
    - a fake that fails gives `soft`;
    - unit text goldens with no `MemoryMax` on the holder unit;
    - a live check on this host in the PR.
- **4c `agentbus run --cap` and code-intel** take `cgroot.Child`.
- **4d the delegated holder:** `Delegate=yes`, `holder/self` with `limits.holder_self_max`,
  `holder/agents/<…>` with `CgroupFD`, journal fields, linger in `up`'s summary (with
  `--no-linger` and the polkit fallback), and cleanup of empty cgroups.
  - Tests: a fake cgroup tree, a fake `systemctl` and `loginctl`, unit text goldens, a holder
    killed in a test leaving every agent reported lost, a failed cap-B creation landing in the
    uncapped `holder/agents` leaf, never `holder/self`, and a live check.

Each step is its own PR and review. Every unit and slice write is on the person's typed yes.
Service tests fake `systemctl` and `loginctl` (CLAUDE.md).
