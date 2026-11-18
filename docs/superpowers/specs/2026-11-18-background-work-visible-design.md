# An agent's background work, visible in busmon

Status: spec, r2, for review (subject `background-work-visible-spec`). r2: foureyes (it starts from
the declared record busmon already shows; one background mark per agent; the observed facts get
their own per-agent key; SubagentStop refreshes the parent's tasks). Not built. Base: origin/dev
`5ec6e134`. Author: coder, task `background-work-visible-spec`, from master's directive of
2026-11-18 relaying the person's item 24: today an agent reads `idle` in busmon while it runs a long
background job or sub-agents. The person's decisions are §6. The build is §7's steps, one PR each
from origin/dev.

## 1. What exists today

- **A declared source, shown already** (progress-line §4).
  - **How it is set:** `agentbus background <agent> <text> [--for d] | --clear` writes
    `{p}:background:<agent>`, a hash of `text` (60 characters at most), `since` (Redis TIME) and
    `by`.
  - **Expiry:** 30 minutes by default, 24 hours at most.
  - **What busmon shows:** the text on the agent's row (`… 2 checks running 4m`) and a header
    chip (`… master: 2 checks running +1`).
  - **Its limit:** it is what the agent remembers to say, not what runs.
- **The person's item 24** (`docs/memory/open-questions/2026-11-17-agent-background-work.md`)
  starts from that count. It proposes folding into idea i2, "sub-agent child rows in busmon"
  (Q6).

- **A judgement, not a display.** `agentstate.Background` (herdr exit 4.3b) judges whether an
  agent has background work, from three sources a client profile may declare authoritative.
  - `hooks`: sub-agent start and stop.
  - `process`: the agent's descendants and their CPU over a window.
  - `screen`: a running-tasks list on screen.
- **Who uses it.** The dispatcher's `HostRuntime.background` uses it only to skip a `/clear`, a
  relaunch or a drain while work runs (busmon's `bg work` flag shows those skips, 10 minutes).
- **What the profiles declare** (`agentstate/clients/*.toml`):
  - Claude Code: `authoritative = ['hooks', 'process']`.
  - Codex, Grok, Kimi and agy declare no source.
- **The hooks are not wired.** `HostRuntime.Hooks` is never set in production, and the launcher
  installs only `SessionStart`, `UserPromptSubmit` and `Stop` (`rolecfg/launch.go`
  `settingsJSON`). So the `hooks` source never says anything. Sub-agents are only filtered out
  of the turn flips (`turnhook.go`: Claude's `agent_id`, Grok's `subagentType`).
- **Nothing observed reaches busmon** but the skip events. An agent with a 40-minute build in
  the background reads `idle`, unless it declared the build itself.

## 2. Where the facts come from, per client

### Claude Code: hooks, the authoritative source

Read in Claude Code's hook reference, code.claude.com/docs/en/hooks, on 2026-11-18. The local
version is to be confirmed by a recorded hook payload (step 1's test fixture), as for every
client fact.

- **`Stop` and `SubagentStop` carry `background_tasks`**: each in-flight task's `id`, `type`
  (`shell`, `subagent`, `monitor`, `workflow`, `teammate`, `cloud session`, `MCP task`) and
  `status`. Shell tasks also carry `command`, all of them a `description`. The array is present
  when Claude's task registry is reachable, and empty when nothing is in flight.
- **`SubagentStop` carries the same two arrays, scoped to the parent session**: a sub-agent's
  stop is a fresher reading of the parent's tasks, recorded for the parent.
- **They also carry `session_crons`**: the wakeups scheduled with `CronCreate`,
  `ScheduleWakeup` or `/loop`, each with its `id`, `schedule` and `recurring`, plus a `prompt`.
- **`SubagentStart` and `SubagentStop`** carry `agent_id` and `agent_type`. They fire for
  sub-agents Claude starts, and also for its own internal agents (an empty `agent_type`).
- **Turn ends only.** The lists come at turn ends, which is exactly when the agent reads `idle`.
  A task that ends later is not reported until the next turn ends. The process source (below)
  says when its shell is gone.

### Every client on a holder: the process tree, a corroborating source

- **What is read.** The dispatcher already reads each terminal's processes (`Host.Processes`:
  pid, start time, command name, CPU) at every observe pass, for `HostRuntime.background`.
- **What counts.** A descendant of the agent's process that is not one of the profile's helpers
  (MCP servers, the client's own daemons) and has used CPU over the window is running
  background work.
- **Its limits:**
  - It cannot tell a sub-agent (which runs inside the client's own process) from nothing.
  - For Codex and Grok, whose tools need not run under their own process (the profile note), it
    cannot be authoritative.
  - It is a count of processes, not of tasks: one `make` is several.

### Codex, Grok, Kimi, agy: no authoritative source known

- **Codex:** hooks `UserPromptSubmit`, `Stop`, `SessionStart` (`rolecfg/resume.go`). No
  background-task list is known in its hook payload.
- **Grok:** hooks in its own file (`rolecfg/grokhooks.go`). Its sub-agent events carry
  `subagentType`; a start/stop pair is not verified.
- **Kimi, agy:** no hooks for this.
- For these four, busmon shows the process source only, marked `?` (§3), until a recorded
  payload proves more (`agentbus term record`, as the profiles' other facts were proved).

## 3. What busmon shows

**One background mark per agent**, in the row's existing slot (`… <what> <age>`), never two:

- **The agent declared its work** (`agentbus background`): its text and age, as today. The
  agent's words say best what runs.
- **It declared nothing, but work is observed:** `… 2 bg tasks 14m` (the count and the oldest's
  age).
  - From Claude's hooks: the tasks of `background_tasks` (shell, subagent, monitor and the
    others), counted together.
  - From the process source only: `… bg ≥1? 14m`, a lower bound, marked as such (Q5).
  - Scheduled wakeups (`session_crons`) are not counted as running work: a small `⏰` after the
    mark, the list on the page only (Q2).
- **The header chip** keeps the declared texts, and counts an agent with observed work alone as
  `bg`.
- **The state column is unchanged:** an agent with only background work still reads `idle` (Q1).

**On the agent's page, a Background section:**

- the declared text, if any, with its age and who set it;
- one line per observed task: its type, its status and how long it has run (`shell · running ·
  14m`, `subagent Explore · running · 3m`);
- for a shell task, the program's name only (`make`, `go`), read live from the holder's process
  list while the page is open, like the screen preview (Q3);
- the source, so the person knows how much to trust it: `from Claude Code's hooks at its last
  turn end (4m ago)`, or `from the process tree`;
- the scheduled wakeups, with their schedule.

**Ages.** Neither source gives a task's start, so its age is counted from when agentbus first saw
it: its first Stop, or the process's own start from the process tree. The page says so.

## 4. What is stored, and what never is

**Never in Redis:**
- a task's `command`, `description` or a cron's `prompt` (they can hold anything the agent
  typed, secrets included);
- a process's arguments;
- screen text (the rule `bus/observed.go` states).

**What is stored**, per agent, in a new hash `{p}:bgtasks:<agent>`, written by the agent's own
hook through `agentbus` as the turn flips are.
- It is apart from the declared record `{p}:background:<agent>`: two writers (the agent's words,
  its client's facts) with two lifetimes, so neither replaces nor clears the other.
- `agentbus background --clear` clears the declared text only; the observed tasks end with the
  client's next report.
- The key class `bgtasks:*` is added to `keykinds` as runtime, beside `background:*`, and `busmon
  --delete` and `projects delete` remove it with the project.

Its value:
- `{source: "hooks", at: <Redis TIME of the Stop>, pid: <the agent's process>, tasks: [{id_hash,
  type, status, first_seen}], crons: [{id_hash, schedule, recurring}]}`.
- `id_hash` is a digest of the task id, which keeps `first_seen` across turn ends without storing
  ids.
- `type` and `status` are Claude's labels, capped at 32 characters: an unknown `type` (Claude
  falls back to its raw discriminant) is stored as `other`, a status outside the known ones as
  `other`.
- A sub-agent's `agent_type` is stored when it matches `[A-Za-z0-9_-]{1,32}` (a built-in or a
  user-defined agent's name), else as `subagent` only: it can be any name the person wrote.
- `schedule` is a cron expression, accepted only if it matches the five-field shape.
- The entry expires one hour after its last write, and is dropped when the agent's process
  changes (a relaunch) or at `SessionStart`.

**The process source** goes into the dispatcher's observation as additive fields (fenced,
like the rest): `bg_procs` (the count of active non-helper descendants) and `bg_oldest` (the
oldest one's start, Redis-clock). No names, no arguments.

## 5. Tests first, per step

1. **Hooks (step 1):**
   - a recorded Claude `Stop` payload with two shell tasks and a sub-agent writes three tasks,
     their types and first-seen times, and no command or description (a test greps every key
     written for the payload's words);
   - a second Stop keeps first-seen by id hash; an empty list clears the entry;
   - a `SubagentStop` refreshes the parent's entry, keyed to the parent;
   - an unknown `type` is stored as `other`; an `agent_type` outside the pattern as `subagent`;
   - `SessionStart` and a new pid drop it.
2. **Process source (step 2):** a terminal with a busy `make` child and an idle MCP helper
   counts 1 and its start; the observation carries no name.
3. **busmon (step 3):**
   - one mark per agent: the declared text wins over the observed count, and the page shows both;
   - the observed count from each source;
   - the `?` mark for the process-only clients;
   - crons on the page only;
   - the page's live process names, read only while the page is open;
   - stale entries (past their hour, or another pid) not shown.

## 6. Questions for the person

- **Q1.** An agent whose turn has ended but whose background work runs: should it read `idle`
  with the `bg 2 · 14m` flag beside it, or get its own state word (`idle · bg`, or `waiting
  on bg`)? Recommended: the flag. The state stays what the agent said, and the flag sits next
  to it.
- **Q2.** Scheduled wakeups (`/loop`, `ScheduleWakeup`, `CronCreate`): should they count as
  background work on the row, or be shown on the page only? Recommended: the page only, with a
  small `⏰` mark on the row. A wakeup is not running work.
- **Q3.** On the page, how much of a shell task should show: the program name only (`make`),
  read live from the holder, or the whole command line, also live and never stored?
  Recommended: the program name only. Arguments can hold secrets, and the screen and `t`
  attach show the rest.
- **Q4.** Should long background work alert anyone (the desktop, master) after some time, say
  2 hours? Recommended: no alert for now. Show it, and decide after a week of seeing it.
- **Q5.** For Codex, Grok, Kimi and agy, with only the process tree: show the `bg ≥1?` flag, or
  nothing until a client's own source is proved? Recommended: show it with its `?`. A process
  that burns CPU under the agent is a fact worth seeing.

- **Q6.** Item 24's note proposes folding this into idea i2 (sub-agent child rows under the
  agent). Should the observed tasks become child rows under the agent's row, or stay one mark
  on the row plus the page's list? Recommended: one mark plus the page for now. Child rows can
  come later on the same data, without a new source.
- **Q7.** Should the dispatcher's skip (no `/clear`, relaunch or drain while work runs) also
  honour the agent's declared text? Recommended: yes. Expiry bounds it (30 minutes by default),
  and a declaration is the agent saying its work must not be cut.

## 7. Steps

One PR each, from origin/dev, base dev:

1. Claude's hooks: install `Stop`'s list reading (the existing `Stop` hook reads
   `background_tasks` and `session_crons`) and `SubagentStart`/`SubagentStop`; write
   `{p}:bgtasks:<agent>` as §4 says; wire `HostRuntime.Hooks` from it, so the dispatcher's skip
   also uses the hooks it was meant to (and the declared record, with Q7's yes).
2. The process source in the observation (`bg_procs`, `bg_oldest`).
3. busmon: one mark per agent, the page's section, the live process names (§3).
