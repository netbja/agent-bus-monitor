# An agent's background work, visible in busmon

Status: spec, r4, for review (subject `background-work-visible-spec`). r4: the person's answers
(2026-11-20, bus thread `1795188144565-0`, relayed by master as cmd `1795188170169-0`): §6 is now
their decisions, with two additions: the long-skip notice to master (§4a) and the bound on a
declaration (§4b). r4 is by architect. Base for r4: origin/dev `77eab8f2`. r3: reviewer-codex
(`agent_type` stored for a sub-agent task; Stop and SubagentStop timing told apart, `at` is the
event's Redis time), foureyes's step-1 notes (the parent's pid; the status set from a recorded
payload). r2: foureyes (it starts from
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
- **Where the code is** (r4). The judgement and the skips are in `dispatch/`:
  - `HostRuntime.background` (`dispatch/hostruntime.go:217`);
  - `Inspection.BackgroundSkip` (`dispatch/decide.go:43`);
  - the skip event, `loop.backgroundSkip` (`dispatch/hostevents.go:128`).

  The service that builds `HostRuntime` moved from `cmd/agentbus` to `internal/dispatchsvc` (#643,
  `internal/dispatchsvc/dispatchhost.go:24`). So step 1 wires `Hooks` there.
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
- **When the lists come** (r3, reviewer-codex):
  - **A parent `Stop`:** at each of the agent's turn ends, the parent's full snapshot. This is
    exactly when the agent starts to read `idle`.
  - **A `SubagentStop`:** whenever a sub-agent finishes, during the parent's turn or after it.
    It is a refresh of the same parent-scoped snapshot.
  - **Staleness:** the record's `at` is the Redis time of the last of these events. A task that
    ends with no event after it stays listed until the next Stop or SubagentStop, or the
    record's hour. The process source (below) says when a shell is gone, and the page shows how
    old the snapshot is.

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
- the source, so the person knows how much to trust it: `from Claude Code's hooks, last report 4m ago`
  (a turn end or a sub-agent's stop), or `from the process tree`;
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
- `{source: "hooks", event: "Stop"|"SubagentStop", at: <Redis TIME of that event's write>, pid:
  <the agent's proved process>, tasks: [{id_hash, type, agent_type?, status, first_seen}], crons:
  [{id_hash, schedule, recurring}]}`.
- `agent_type` is present for a `subagent` task only, sanitised as below (r3, reviewer-codex:
  the page's `subagent Explore` needs it).
- `pid` is always the parent agent's proved process (foureyes). A SubagentStop's hook runs in
  the parent's process, so its own pid is never written. Otherwise the drop on a new pid would
  fire at every sub-agent stop.
- `id_hash` is a digest of the task id, which keeps `first_seen` across turn ends without storing
  ids.
- `type` and `status` are Claude's labels, capped at 32 characters: an unknown `type` (Claude
  falls back to its raw discriminant) is stored as `other`, a status outside the known ones as
  `other`. The known statuses are fixed from step 1's recorded payload (the reference lists none;
  at least `running`), and named here then.
- A sub-agent's `agent_type` is stored when it matches `[A-Za-z0-9_-]{1,32}` (a built-in or a
  user-defined agent's name), else as `subagent` only: it can be any name the person wrote.
- `schedule` is a cron expression, accepted only if it matches the five-field shape.
- The entry expires one hour after its last write, and is dropped when the agent's process
  changes (a relaunch) or at `SessionStart`.

**The process source** goes into the dispatcher's observation as additive fields (fenced,
like the rest): `bg_procs` (the count of active non-helper descendants) and `bg_oldest` (the
oldest one's start, Redis-clock). No names, no arguments.

## 4a. A skip that lasts: one notice to master (Q4)

**Why.** Background work makes the dispatcher skip an agent's `/clear`, relaunch or drain (4.3b).
A forgotten task, such as a dev server left running, would hold that skip forever, and silently.

**The episode.** An agent's skip episode starts with the first skip of an action for its
background work, and its start is stored at that pass's Redis time. It ends at the first pass that
does not skip that action for that agent:
- the action ran;
- it is no longer pending;
- the work ended;
- the agent's process changed.

Each pending action (`auto-clear`, `relaunch`, `drain`) has its own episode.

**The notice.** When an episode has lasted `dispatch.background_notice_after`:
- **What is sent:** one `FencedCmdOnce` to the escalation target (master by default), wake=yes,
  episode = the episode's start.
  - The text: `background: <agent>'s <action> has been skipped for <n> h — <source>: <what>. If
    that work was forgotten (a dev server?), stop it or tell the agent; the dispatcher acts
    once it ends. See its page in busmon, or agentbus background <agent>.`
- **The source:** `<source>` is the source that made the judgement block:
  - `hooks`, `process` or `screen`, from `agentstate.Judgement`;
  - `declared`, from the agent's own record (§4b);
  - `unknown`, when no authoritative source was read (fail closed).
  - `<what>` is `Judgement.What()`, or the declared text. Neither is a command or arguments.
- **Once per episode.** A dispatcher restart does not repeat it: the episode's start and the
  notice are marks in the same fenced script as the other dispatcher marks
  (`bgskip:<agent>:<action>` holds the start; `bgskip-notice:<agent>:<action>` is the once mark).
  Their name functions go in `bus/context.go`'s style, beside the context marks.
  The episode's end deletes both.
- **Master's own skip:** when the agent is the escalation target itself, the notice is sent
  wake=no (it is not woken for its own forgotten task), and busmon shows it as today's notices.
- **No desktop alert, and no notification to the person** (the person's Q4).

**The setting:** `dispatch.background_notice_after`.
- In the registry (`config/keys.go`): Duration, default `2h`, `0` = off, `ReadBy: "dispatch"`,
  in the Health group.
- Its env name is `AGENT_BUS_BACKGROUND_NOTICE_AFTER`.

## 4b. The declared text holds the skip, within a bound (Q7)

- **The declaration counts.** The agent's declared record (`{p}:background:<agent>`, not
  expired) makes `BackgroundSkip` skip the agent, as observed work does, with source `declared`.
  - Its expiry bounds it: 30 minutes by default, 24 hours at most.
- **Context escalation keeps priority.** A declaration never holds off the `/clear` after a
  declared hand-off while the agent's context episode has escalated (context-escalation §2 step 2:
  the `context-escalate:<agent>` mark, `bus.ContextEscalateMark` (`bus/context.go:124`), is set for
  the current episode).
  - There, the declaration is ignored for `auto-clear` only. Observed work (`hooks`, `process`,
    `screen`) still skips, as 4.3b says, since a `/clear` would cut running work.
  - A declaration of up to 24 h can never let an agent's context overflow.
- **Relaunch and drain** honour the declaration with no such exception: neither empties a context
  that is filling.

## 5. Tests first, per step

1. **Hooks (step 1):**
   - a recorded Claude `Stop` payload with two shell tasks and a sub-agent writes three tasks,
     their types and first-seen times, and no command or description (a test greps every key
     written for the payload's words);
   - a second Stop keeps first-seen by id hash; an empty list clears the entry;
   - a `SubagentStop` refreshes the parent's entry, keyed to the parent, its `at` the event's time,
     its `pid` the parent's (no drop at a sub-agent's stop);
   - a sub-agent task keeps its sanitised `agent_type`;
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
4. **Skip notice and the declaration (step 4):**
   - **The notice:**
     - an `auto-clear` skipped for `background_notice_after` sends one notice to the escalation
       target, wake=yes, naming the agent, the action and the source;
     - a dispatcher restart in the middle sends no second one;
     - the action running, or the work ending, ends the episode and deletes both marks, so a new
       episode can notice again;
     - `0` sends none;
     - for the escalation target itself, the notice is wake=no.
   - **The declaration:**
     - a declared, unexpired record skips a `/clear`, with source `declared`;
     - an expired one does not;
     - with `context-escalate:<agent>` set for the episode, the declaration no longer skips the
       `/clear`, while a `process` source still does;
     - relaunch and drain honour the declaration.

## 6. The person's decisions (2026-11-20)

- **Q1: the flag beside `idle`.** No new state word. The state stays what the agent said (§3).
- **Q2: the page only, plus a small `⏰` mark on the row.** Scheduled wakeups are not counted as
  running work (§3).
- **Q3: the program name only,** read live from the holder while the page is open, and never
  stored (§3, §4).
- **Q4: no desktop alert.** When the dispatcher has skipped a `/clear`, relaunch or drain for
  about 2 h (a setting), master gets one notice per episode, naming the agent and the source of
  the skip (§4a). A forgotten task must not block the automaton silently.
- **Q5: yes,** `bg ≥1?` with its `?` for the process-only clients (§3).
- **Q6: one mark plus the page for now.** Child rows (idea i2) come later, on the same data.
- **Q7: yes, the skip honours the declared text, within a bound.** Context escalation keeps
  priority over a declaration. A declaration (up to 24 h) must never let an agent's context
  overflow (§4b).

## 7. Steps

One PR each, from origin/dev, base dev:

1. Claude's hooks: install `Stop`'s list reading (the existing `Stop` hook reads
   `background_tasks` and `session_crons`) and `SubagentStart`/`SubagentStop`; write
   `{p}:bgtasks:<agent>` as §4 says; wire `HostRuntime.Hooks` from it, so the dispatcher's skip
   also uses the hooks it was meant to.
2. The process source in the observation (`bg_procs`, `bg_oldest`).
3. busmon: one mark per agent, the page's section, the live process names (§3).
4. The skip notice and the declaration's bound (§4a, §4b):
   - the `bgskip` marks and the notice in `dispatch/`;
   - the declared record in `BackgroundSkip`, with the escalation exception for `auto-clear`;
   - the setting `dispatch.background_notice_after`.

   Step 1's "(and the declared record, with Q7's yes)" moves here, so the bound comes with it.
