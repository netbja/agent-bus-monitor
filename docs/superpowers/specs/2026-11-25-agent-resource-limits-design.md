# Agent resource limits (agent-resource-limits)

Status: spec r1, for review (subject `agent-resource-limits-spec`). Not built. Base: origin/dev
`3d2dfa79`. Author: architect, from master's directive (task `agent-resource-limits-spec`, cmd
`1795634829231-0`), on the person's request after the 2026-11-25 20:16 OOM. No new dependency.
The questions for the person are last (§11).

## 1. What happened, measured

From this host's user journal and `systemctl --user show`, read on 2026-11-25 after the incident:

- `20:16:07 agentbus-holder.agent-bus-monitor.v0.33.0.b223191132.service: The kernel OOM killer
  killed some processes in this unit.`
- `20:16:12 … Failed with result 'oom-kill'.` Then: `Consumed 1d 1h 53min CPU time over 21h 46min
  wall clock time, 56.4G memory peak, 31.9G memory swap peak.`
- Both holder units today: `OOMPolicy=stop`, `MemoryMax=infinity`, `Delegate=no`. The host has
  62 GB of RAM and 39 GB of swap. The user manager delegates `cpu io memory pids`.

**The cause is one unit line.** The kernel killed one process, the runaway mutant. That was the
right process. Then systemd applied the unit's `OOMPolicy=stop`, its default for every service.
That policy stopped the whole holder unit, and `KillMode=control-group` took every agent terminal
with it. So the holder did not die from lack of memory. It was stopped because one of its
children was killed.

Three more facts make it worse. Nothing capped the unit, so the runaway used all the RAM and
32 GB of swap first, and the host thrashed for minutes. Nothing showed per-agent memory, so
nobody saw it grow. The test that ran away had no time cap in practice: 26 h of CPU.

The v0.33.0 and v0.34.0 holders running side by side is the upgrade's designed drain
(`holder_draining` warns about it). But the warning never ends, and it does not say how long the
drain has lasted.

## 2. The model: three nested caps

```
holder unit (systemd service)        cap A: whole holder; OOMPolicy=continue
 ├─ holder process (leaf "holder")
 ├─ terminal of agent coder           cap B: one agent's terminal
 │    └─ agentbus run --cap -- go test   cap C: one command
 └─ terminal of agent foureyes        cap B
```

When a cap is hit, the kernel kills **the largest process inside that cap**, which is the
runaway. Nothing else is killed: no other pane, no client, no holder. Each layer is useful on its
own, and each is a step (§7).

- **Cap A, the holder unit.** It is enforced and needs no agent to cooperate. It gets
  `OOMPolicy=continue`, `MemoryMax`, and `MemorySwapMax`. This alone would have saved every
  pane on 2026-11-25.
- **Cap B, one agent's terminal.** It is enforced. Each terminal runs in its own child cgroup of
  the holder unit (`Delegate=`), with its own `memory.max`. One agent's runaway cannot take the
  others' share of cap A.
- **Cap C, one command.** It is advisory: the agent must use it. `agentbus run --cap -- <cmd>`
  gives a command a memory cap and a wall-time cap, and a CPU cap where possible. The skills make
  it the way every test runs.

A cap that relies on the agent alone fails silently when it is skipped (lesson: advisory versus
enforced). So A and B are the protection, and C makes the kill earlier and its message clearer.

## 3. Cap A: the holder unit

`systemdHolderUnit` writes three more lines in `[Service]`:

```
OOMPolicy=continue
MemoryMax=<limits.holder_memory_max>      # default 70%  (systemd: % of physical RAM)
MemorySwapMax=<limits.holder_swap_max>    # default 4G
```

- `continue` means that after an OOM kill inside the unit, systemd logs it and the unit keeps
  running. The killed process's parent (a client's shell tool) sees `signal: killed`, as with any
  kill.
- `MemorySwapMax` matters as much as `MemoryMax`. Without it, the runaway first fills the swap,
  and the whole host thrashes before the kill (32 GB on 2026-11-25).
- No `MemoryHigh`: it throttles every pane of the holder, not only the runaway. The early warning
  is the notice in §5, not throttling.
- No CPU cap on the unit: CPU starvation kills nothing. The time cap of C and the visibility of §5
  cover it.
- **An upgrade, not a restart.** Units are never restarted to upgrade (herdr-exit spec 4.4a). The
  next build's `agentbus up` writes a new unit with these lines, and the old build drains as it
  does today. No running holder is touched.
- **launchd** has no equivalent: no memory cap, no OOM policy. macOS takes the stance of §6.
- **The supervised holder** (no service manager, `agentbus supervise`) has no cgroup. It gets
  nothing from A or B, only C and §5. Its `holder` health detail says so.

### 3.1 The holder's own high-water notice

On every terminal-processes request (`Reply.Procs`, which dispatch already makes on each pass),
the holder also reads, from its own cgroup on Linux: `memory.current`, `memory.max`,
`memory.swap.current`, and the `oom_kill` count of `memory.events`. These are four small file
reads, with no new process. The holder adds them to the reply. A holder without a cgroup reports
none.

A new health check, `holder_memory`:

| Status | When |
|---|---|
| ok | below `limits.warn_at` (default 0.8) of the unit's `MemoryMax` |
| warn | at or above it; detail: `holder build <b>: 51.2G of 43.4G cap (82 %), largest: coder 38.0G` |
| warn | the `oom_kill` count rose since the last pass; detail: what the holder's per-terminal attribution (§4) names |
| unknown | no cgroup (launchd, supervised) |

It is a health check, so busmon shows it, sentinel reads it, and the dispatcher's existing
warn-to-master notice tells master **once per episode**. There is no new notice mechanism.

### 3.2 A stale second holder

`holder_draining` already warns while an older build holds terminals. Two changes:

- Its detail names the age of the drain: `build v0.33.0: 3 terminal(s), draining for 21h`.
- Past `limits.holder_drain_stale_after` (default 6h), its status becomes `fail`, and the detail
  names the agents still in the old build and the command that ends the drain (`agentbus relaunch
  <agent>` for each, or the person's `service restart holder --kill-agents` for that build). A
  `fail` reaches master once through the same notice path. busmon's header shows `2 holders` while
  two builds answer.

The old build is never stopped automatically. Its terminals are agents' live sessions. Ending
them is master's or the person's act.

## 4. Cap B: one agent's terminal

The holder unit gets `Delegate=memory pids`. At start, on Linux, the holder:

1. reads its own cgroup from `/proc/self/cgroup`;
2. moves itself into a leaf child `holder` (cgroup v2 forbids processes in a cgroup whose
   children use controllers);
3. writes `+memory +pids` to the unit cgroup's `cgroup.subtree_control`.

At each terminal launch, before the client's `exec`, the holder creates `term-<terminal id>`,
writes `memory.max` = `limits.agent_memory_max` (default 30%, read against `MemTotal`) and
`memory.swap.max` = `limits.agent_swap_max` (default 2G), and moves the pane's process into it.
`memory.oom.group` stays 0: only the largest process in the pane dies, not the client with it.
When the terminal ends, the holder removes the empty cgroup.

- **It composes with sandboxed-launch.** The cgroup is entered before `srt` or the client runs,
  so whatever wraps the client inherits it.
- **Attribution.** For each terminal, the holder reads `memory.current`, `memory.peak` and the
  `oom_kill` count of `memory.events`, and reports them with the terminal's `Procs`. A rise of
  `oom_kill` in `term-<id>` names the agent whose command was killed.
- **The killed agent is told.** On that rise, dispatch sends the agent one entry: `a process in
  your terminal was killed at its memory cap (<cap>): <command name, from the last Procs>; rerun
  it with agentbus run --cap and a smaller scope.` A bare `signal: killed` does not explain itself.
- **Live-read.** The holder re-reads the two `limits.agent_*` keys at most once a minute, on a
  terminal-processes request, and rewrites `memory.max` on panes whose value changed. Lowering a
  cap below a pane's current use makes the kernel reclaim or kill at once. That is the person's
  act, and the key's help says so.
- **Fallback.** If any of steps 1–3 fails (no delegation, cgroup v1, the supervised holder), the
  holder logs it once and runs without cap B. `holder_memory`'s detail says `no per-agent caps:
  <reason>`. A launch is never refused for it.

Rejected: wrapping each client in `systemd-run --user --scope`. A scope leaves the holder unit's
cgroup, so `KillMode=control-group`, the unit's cap A, and its accounting no longer cover the
agents. It also adds a D-Bus round trip to every launch.

## 5. Visibility per agent

- `ProcInfo` gains `rss_kb`, read from `/proc/<pid>/statm`: one more small read per process that
  the holder already walks.
- dispatch already pulls every terminal's process tree on each pass (`hostruntime.go`, the
  background judgement). From it, dispatch publishes per agent: memory (the terminal's cgroup
  `memory.current` when cap B is on, else the RSS sum of the tree), CPU % (the growth of the
  tree's CPU over the pass window), and the largest process's name and RSS. They go into the
  agent's observation fields, beside `BgProcs`.
- **busmon** shows `mem` and `cpu` columns in the agents table: `4.1G 180%`. They turn amber at
  `limits.warn_at` of the agent's cap B, or, without cap B, of cap A divided by the number of
  terminals. The agent detail names the largest process.
- **master is told once per episode** when one agent crosses `warn_at`. This uses the context
  alert's pattern (`contextalert.go`): an episode mark, one notice to master naming the agent, its
  largest process, its use and its cap. Sentinel is not woken for a warn. It reads
  `holder_memory` at its daily review, as for any check.

## 6. Cap C: one command, and how agents run tests

`agentbus run --cap [--mem <size>] [--time <dur>] [--cpu <pct>] -- <command> [args…]`

| Where | What it does |
|---|---|
| Linux with a reachable user manager | `systemd-run --user --scope --quiet --collect -p MemoryMax=<mem> -p MemorySwapMax=0 -p RuntimeMaxSec=<time> [-p CPUQuota=<cpu>] -- <command>` |
| Linux without one, macOS | its own process group; `RLIMIT_AS` = `<mem>` (Linux only: macOS does not enforce it); the whole group is killed at `<time>`; the CPU cap is ignored, and that is said once on stderr |

- The defaults come from `limits.command_memory` (8G), `limits.command_time` (30m) and
  `limits.command_cpu` (empty: none). The flags may only narrow them (agents cannot raise their
  own cap). A larger value is refused with the key's name.
- Exit status: the command's own, or 137 with one stderr line, `agentbus run: killed at memory cap
  8G` or `… at time cap 30m`. The agent reads why it failed.
- A scope leaves the pane's cgroup B for its own, tighter cap C. That is intended: C is always
  below B.

**macOS.** It has no enforced cap A or B (launchd has none). It gets §5's visibility and notice,
and cap C's time cap. The memory cap on macOS is §11 Q3.

**How agents run tests.** These are documentation changes, plus one test that enforces them:

- `skills/agent-bus` (all implementers and reviewers), `skills/agent-bus-master` (the release
  check), the reviewers' mutation recipes and `docs/testing-redis.md`: every `go test` line becomes
  `agentbus run --cap -- go test … -timeout <d>`. `-timeout` is explicit, because Go's default
  of 10m per package binary does not apply when a script sets `-timeout 0`. A mutant run uses
  `--mem 4G --time 5m`.
- A suite test, `TestSkillGoTestLinesAreCapped`, reads `skills/**/SKILL.md` and
  `docs/testing-redis.md`, and fails on a `go test` line in a code block that has no `-timeout`
  or no `agentbus run --cap` before it. Skills drift, and this keeps them honest.
- `tests/run.sh` (the shell suite) runs its Go helpers the same way.

## 7. Steps, cheapest first

| Step | What | Size | Protects |
|---|---|---|---|
| **1** | Cap A: three unit lines + the two `limits.holder_*` keys + unit-text test | ~40 lines | every pane, from 2026-11-25's failure, at the next build's `up` |
| 2 | `agentbus run --cap` + skills + `TestSkillGoTestLinesAreCapped` | small | earlier, explained kills of tests |
| 3 | §5 visibility (`rss_kb`, per-agent mem/cpu, busmon columns, master notice) + §3.1 `holder_memory` + §3.2 drain age | medium | seeing it coming |
| 4 | Cap B: delegated per-terminal cgroups, attribution, the killed agent told | medium, Linux only | one agent cannot starve the others |

**The cheapest first step is step 1.** It is one PR with three lines in the unit text, two keys,
and a test of the unit text. It needs no agent's cooperation.

**Until step 1 ships, the person can protect the running holder now** (on the person's yes, never
by an agent): a drop-in for the current build's unit, then `daemon-reload`, which does not restart
the unit.

```
systemctl --user edit agentbus-holder.agent-bus-monitor.<key>.service
  [Service]
  OOMPolicy=continue
  MemoryMax=70%
  MemorySwapMax=4G
systemctl --user daemon-reload
```

`MemoryMax` and `MemorySwapMax` take effect at reload. That `OOMPolicy` does too, without a
restart, is for the builder to verify on a throwaway unit before telling the person. Without it,
a cap only makes the stop happen sooner. So no cap is applied without `OOMPolicy=continue`.

## 8. Settings (`config/keys.go`)

All are `PersonOnly` (an agent cannot loosen its own cap) and `MachineOnly` (RAM differs per
host). Sizes take systemd's syntax: `8G`, or `70%` of physical RAM.

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

`off` on a cap key writes no line (no cap at that layer). `OOMPolicy=continue` is always written:
it has no downside to switch off.

## 9. Testing

- **Step 1.** A golden test of `systemdHolderUnit`'s text for the default keys, for `off`, and for
  a `%` and a size. Prior art: the existing unit-text tests in `service/holder_test.go`.
- **Step 2.** `agentbus run --cap` with a fake `systemd-run` on `PATH` asserts the argv. The real
  fallback is exercised on a child that allocates past a small `RLIMIT_AS`, and on one that sleeps
  past `--time`: exit 137 and the stderr line. The installer-test pattern stubs the manager; no
  test touches the person's user manager. The suite test of §6 is a plain file read.
- **Step 3.** `holder_memory` and `holder_draining` are pure `health` evaluators with table tests,
  as the existing holder checks are. busmon's columns get golden screens. The once-per-episode
  notice reuses the context alert's test pattern.
- **Step 4.** The cgroup code takes a root directory, so a test runs it against a temp tree of
  plain files (the write order, the leaf move, removal, and the `oom_kill` parse). The real
  delegation is checked once by hand on this host and recorded in the PR: a pane that allocates
  past a 256M cap is killed, and its neighbour and the holder are alive.
- No test runs a real memory runaway in the suite.

## 10. Out of scope

- Disk and file-descriptor caps; network.
- Killing or restarting a stale holder automatically (§3.2: master's or the person's act).
- A CPU cap per agent (visibility only. The person can ask for one later as `cpu.max` in cap B).
- systemd-oomd policy: Fedora's oomd acts on pressure at the slice level, outside this spec.

## 11. Questions for the person

**Q1. How big is the holder's cap (cap A)?**
- **(a) `MemoryMax=70%`, `MemorySwapMax=4G` (recommended).** On this host: 43 GB for all agents,
  with 19 GB left for the desktop and the broker. The runaway dies after 4 GB of swap, not 32.
- (b) `85%`, swap `8G`: more room for agents, and less for everything else.
- (c) No memory cap, only `OOMPolicy=continue`: the panes survive, but the host still thrashes
  before the kill.

**Q2. Does each agent get its own cap (cap B, step 4)?**
- **(a) Yes: `30%` of RAM and `2G` of swap per agent (recommended).** About 18 GB here. A normal
  `go test ./...` uses a few GB.
- (b) Yes, at a fixed `16G` on every host.
- (c) No: cap A only. One agent's runaway can then use every other agent's share.

**Q3. On macOS (no cgroups), what stops a runaway?**
- **(a) The notice and `agentbus run`'s time cap only (recommended).** Simple; macOS hosts are
  rare here.
- (b) The holder kills the largest process of a terminal over the agent cap, polled every pass:
  a watchdog. It kills late, and it is the holder killing processes.
- (c) Nothing beyond the notice.

**Q4. The default caps of `agentbus run --cap` (tests, mutants)?**
- **(a) `8G`, `30m`, no CPU cap (recommended).** The full suite fits; a mutant script narrows to
  `4G` and `5m`.
- (b) `4G`, `15m`, `200%` CPU: tighter. The full suite may need `--mem` and `--time` raised by the
  person.
- (c) Per role, in `roles.toml`: more knobs, and one more file to keep in step.
