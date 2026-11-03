# Suspend and resume: put a whole host's team to sleep, and bring it back

Status: spec, open questions in §12. Subject `suspend-spec`. Request: the person, 2026-11-03, after a
reboot for OS updates that left the team to be brought back by hand: "an option to put everything on
pause, like a snapshot, to resume easily after a power cut, a crash, or simply my choice". v0.27,
step 1 (master's plan task `suspend-spec`).

## 1. Why, and what already exists

Bringing a team back today takes several gestures, each one right on its own:

| Piece | Today | After a reboot |
|---|---|---|
| Broker | `docker-compose.yml`, `restart: unless-stopped` | comes back with docker |
| Agents | herdr panes; every Claude Code launch reports its resume argv to herdr (`roles report-resume`) | herdr's native session restore relaunches them *if* herdr is started and accepted the argv; else `agentbus up <p> --resume` by hand, per project |
| Services | systemd `--user` / launchd units, never enabled at login | stopped; the workspace hook (`plugin workspace-event`, `upHook`) starts them when the workspace is focused |
| Pause | `{p}:pause`, a deadline; the dispatcher ends it when due (`FencedResume`) | survives in Redis |
| Open work | asks, gates, board, obligations, hand-offs: all in Redis | survives in Redis |

What is missing is not a mechanism but **one gesture each way**, and a **fact** that the team was
put to sleep on purpose:

- nothing stops the team cleanly before a planned reboot: an agent mid-turn loses its turn, a
  missions check is killed, and the dispatcher may wake an agent while the machine goes down;
- after the reboot nothing says which projects to bring back, on which herdr session, nor what was
  open; the person reads busmon project by project;
- the workspace hook starts services as soon as a workspace is focused, so a project the person
  wanted asleep wakes when they merely look at it.

`agentbus suspend` and `agentbus resume` (§4, §5) are that gesture. They are a front door over what
exists (pause, `down`, `up --resume`'s relaunch), like `agentbus start` is over `up`: **no new pane
writer, no new supervisor, nothing restored into Redis.**

## 2. Words

- **Suspended**: a project whose `{p}:pause` record has `kind = "suspend"` (§3). It is the only
  source of truth; the local snapshot file (§6) is what the host remembers about it.
- **Snapshot**: `<state>/agentbus/<p>/suspend.json`, written by `suspend`, read by `resume`, renamed
  `suspend.<taken_at>.json` once a resume completes (kept 5, the older removed).
- **Boot agent** / **pop**: `tier` as recorded in the agent's verified snapshot (`AgentSnapshot.Tier`);
  an agent with no tier recorded is read as boot when its role is a tab of the project's template.
- **Host's projects**: the templates in the projects directory (`rolecfg.ProjectsDir`) whose project
  the broker has (`bus.Projects`), as the picker reads them (`projlist`). `--project <p>` narrows to
  one, from the command line only (never `AGENT_BUS_PROJECT`, as `backup` does).

## 3. The suspend pause (bus change)

`PauseRecord` gains two additive fields:

```go
Kind string        `json:"kind,omitempty"` // "" = a timed pause (today); "suspend"
Prev *PauseRecord  `json:"prev,omitempty"` // the pause the suspend replaced, if any
```

A suspend pause has no deadline in meaning. Its `Until` is set to `at + 365 days`, so a dispatcher
of an older release, which does not know `kind`, never ends it in practice; busmon and `pause
--status` show it as `suspended since <at>`, never by its `Until`.

Rules, each in the one script that writes the key (so a reader never races a writer):

| Writer | On a suspend pause |
|---|---|
| `Bus.Suspend` (new, one script) | writes `kind=suspend`, `by`, `notify`, `at` from Redis `TIME`, `reason`; an existing timed pause (a person's or a quota pause) is moved into `prev` whole; an existing suspend pause is left as it is and reported (`already suspended since …`) |
| `FencedResume` (dispatcher, when due) | never ends it: the script returns 0 when the stored record decodes to `kind = "suspend"` (the dispatcher logs `paused`, as for any pause not due) |
| `FencedQuotaPause` | already never replaces another reason; unchanged |
| `FencedQuotaUnpause*` | already deletes only its own exact record; unchanged |
| `SetPause` (`agentbus pause`) | refused: `project suspended since …: agentbus resume brings it back`; it moves to a script so the check and the write are one |
| `Bus.Resume` (`agentbus resume` today) | refused with `ErrSuspended`; the CLI's `resume` then runs the full resume (§5) instead, see §4's verb table |
| `Bus.Unsuspend` (new, one script, compare-and-swap on the record read) | if `prev` is a timed pause still in the future (Redis `TIME`), puts `prev` back as the pause (no resume directive: the team stays paused until that deadline, and the dispatcher ends it as usual); else deletes the key and XADDs the resume directive (§5 step 6) |

`{p}:pause` is already in `projectKinds` and is a record (restored by `restore`); no new key, no new
kind, so `TestEveryWrittenKeyKindIsClassified` needs nothing. The dispatcher's pass is unchanged: a
paused project already wakes, escalates, clears, observes, retires and alerts nobody (`loop.go`, the
`paused` check); the passes before it (quota, context, tokens, hand-off voiding) only read or write
records and run as they do under a timed pause.

## 4. The CLI

### Verb table

| Command | What it does |
|---|---|
| `agentbus suspend [--project <p>] [--wait <d>] [--no-wait] [--force] [--reason <text>] [--dry-run] [--yes]` | §4.1, every project of the host or one |
| `agentbus suspend --status` | per project: suspended since / by / reason, snapshot path, what it recorded (counts); exit 0 when one is suspended, 1 when none |
| `agentbus resume [message...]` | today's verb, unchanged for a timed pause; on a **suspended** project it runs §5 for that project |
| `agentbus resume --all [--dry-run] [--partial] [--yes]` | §5 for every suspended project of the host, and the crash path (§7) for the host's projects that are not suspended but whose services or agents are down |

One verb, `resume`, because the person's sentence is "resume": a timed pause needs only its lift, a
suspended project needs services and agents too, and the bus record says which. A second verb
(`unsuspend`, `wake`) would leave `resume` on a suspended project either refused (a dead end for the
person) or doing half the job. Open question Q1.

`suspend` and `resume --all` sit above the project-required exit (they name projects themselves, like
`backup`); plain `resume` stays where it is. Both are writes for the identity guard (`identityGuard`):
an agent runs them only as itself, and every role's tool rules **deny** `agentbus suspend*` and
`agentbus resume --all*` (shared `[tool_rules]`, like `reload --as`): the team never puts the host to
sleep or wakes it; the person does, from a shell or busmon. Master keeps today's `agentbus resume`
(a timed pause), which on a suspended project is refused for an agent (`ErrSuspended` read by the CLI
before §5 runs: only a person's process runs §5, decided as `notifications test` decides it, no
`AGENT_BUS_AGENT` and no process under a registered agent's verified pid). Defence in depth, not a
boundary, as for every tool rule.

Table entries in `cmd/agentbus/commands.go` (so `TestEveryDispatchedCommandIsInTheTable` and
`TestEveryFlagLiteralIsInTheTable` hold): `suspend` (flags above, short `-p` before the command
only, as today), and `resume` gains `--all`, `--partial`, `--dry-run`, `--yes`. Help text:

```text
agentbus suspend               put every project of this host to sleep: ask working agents to stop
                               at a clean point, pause, record a snapshot, stop the services
agentbus suspend -p <p>        one project
agentbus suspend --status      what is suspended, and what each snapshot holds
agentbus resume --all          bring every suspended project back: services, missing agents on
                               their sessions, then lift the pause and tell master what was open
```

### 4.1 `suspend`, per project, in this order

1. **Read and refuse early** (nothing written yet): the broker answers; the project is not already
   suspended (else say so, skip it, exit 0 for it); no swap, reload or retire intent is held
   (`{p}:swap:<role>`, `PX 180000`: `a swap of <role> is in flight; retry once it ends`, refused
   even with `--force`, since stopping under a relaunch half-done is the one state resume could
   not repair); a missions attempt `integrating` is said (not refused: `down` already interrupts
   it and the conductor redoes it at start, `Startup`).
2. **Ask the agents to stop at a clean point**, unless `--no-wait`: every registered, non-dormant agent
   (`bus.DormantAfter`) whose effective state (`bus.EffectiveState`, the stall guard's reading) is
   `working` gets one waking directive from the person, with an expiry of `--wait` (default 10 min).
   That is a new helper, `Bus.HumanCmdTTL`: `HumanCmd` (`human=yes`, waking) plus `CmdNoWakeTTL`'s
   `expires_at` from Redis `TIME`, so a directive still unread when the suspend ended drains as
   expired after the resume, never as a stale order:

   > Suspend of <p> requested by <person>: finish or stop your current step at a clean point,
   > commit your work in progress on your branch, report where you are (agentbus report), then
   > declare idle (agentbus status idle "suspended"). Do not declare a hand-off: your session is
   > resumed as it is.

   Not a hand-off: a hand-off makes the dispatcher type `/clear` while the project is not yet paused,
   and the point of a suspend is to resume the *same* conversation (`--resume <IdentSession>`).
   Agents idle, blocked or waiting get nothing (they are already at a clean point; their open
   asks and gates persist in Redis).
3. **Wait** until no such agent reads `working` through `bus.EffectiveState` (re-read every 2 s),
   at most `--wait`. A turn that ends counts as a clean point even when the agent never read the
   directive: its turn-end hook flips it idle (auto-state), or a fresh observation sees its pane
   ready (an agent with no hooks); the directive then expires unread, and nothing is lost, since the
   resumed session is the same conversation. On timeout:
   without `--force`, stop here for that project, write nothing more, exit 1 and name each agent still
   working and since when; the directives already sent stay (an agent that stops later has lost
   nothing). With `--force`, go on and record them as `working at suspend` in the snapshot.
4. **Pause**: `Bus.Suspend` (§3). From here no agent of the project is woken.
5. **Read the snapshot's facts** (§6) from the bus, herdr (if reachable) and the service manager.
6. **Stop the services** through the service manager (`Manager.Stop`, as `down` does), recording for
   each kind whether it was active. No service manager (tab mode): say `dispatch and missions run as
   herdr tabs: they stay; paused, they wake nobody`.
7. **Write the snapshot** (§6), then print one line per project: `suspended (N agents, M open items);
   services stopped; snapshot <path>`.

A failure at 5-7 leaves the project suspended (step 4 holds): the bus is the truth, the snapshot a
convenience, and resume reads the live bus when the file is missing (§7). It is said, with the
command to retry (`agentbus suspend -p <p>` sees the suspend and only completes steps 5-7: the one
case where "already suspended" is not a no-op is a project whose snapshot file is missing or older
than its pause's `at`).

Projects are suspended one after the other, in the projects directory's order, each to the end;
`--project` narrows. `--dry-run` runs step 1 and prints what 2-7 would do, writing nothing.
Without `--yes` it asks once on the terminal for the whole list (`setup.OpenTTY`); no terminal and no
`--yes` prints the plan and exits 2, like `start`.

Agents are **left running**: idle, paused, they cost nothing, and a reboot ends them anyway. Closing
their panes is open question Q2.

## 5. `resume` of a suspended project, in this order

1. **Read**: the suspend pause (else this is §7's crash path), the snapshot if present and taken at or
   after the pause's `at` (an older one is ignored and said), the herdr session from the project's
   services env file (`HERDR_SESSION`, as `up` records it; the snapshot's copy when the env file
   cannot be read).
2. **herdr**: the session must run. When it does not, resume does not start herdr (it needs a
   terminal; `start` does that in the person's terminal): it says `herdr session <s> is not running:
   start it (herdr --session <s>, or agentbus start in <folder>) and run agentbus resume again`, and
   goes on with step 3 only; the pause stays (step 6 is not reached without `--partial`).
3. **Services**: start every kind the snapshot recorded as active, or, with no snapshot, every
   installed kind (`Manager.Start`, the restart budget reset first, as `service start` does). The
   dispatcher starts on a paused project and wakes nobody yet.
4. **Agents**: `up`'s relaunch on the project's open workspace with `Resume: true` (`relaunchUp`, under
   `upLock`), so the existing rules decide each template tab: a live agent is left (herdr's native
   restore may already have brought it back), a dead one is relaunched in a new pane split from its
   tab or a new tab, on its `IdentSession`; nothing ever types into an existing pane. A workspace
   not open is created by `up`'s own path (`Create: true`). Pops are **not** relaunched (they are
   spawned for one piece of work; their template is not the project's): the summary names each with
   its session and the command (`agent-spawn <role> <p> --resume <id>`) for master. Open question Q3.
5. **Wait for proof**, bounded 90 s: each relaunched agent's verified identity, a new live pid in its
   new pane (the proof `relaunch` already waits for). On Linux only, like every verified identity; on
   macOS the agents are counted as `relaunched, not provable` and the step does not wait.
6. **Lift**: once every boot agent the snapshot (or the live bus) knows is live or proven, or with
   `--partial`, `Bus.Unsuspend` (§3): either the previous timed pause comes back, or the key goes and
   master (the record's `notify`, else the escalation target) gets **one** waking directive, from the
   person (`human=yes`), on its own line per item, cut under `CmdMaxRunes` with `(+N more: agentbus
   suspend --status -p <p>)`:

   > <p> resumed after a suspend (<since> → <now>, by <person>). Agents: coder resumed (session
   > kept), foureyes fresh (no session to resume), ui-kimi (pop) not relaunched: agent-spawn
   > ui-kimi <p> --resume <id>. Open: 2 asks (coder, master), 1 gate (foureyes ← coder, subject
   > x), board: 3 tasks not done, 4 messages waiting (coder 3, sentinel 1). Missing: none.

   The waiting messages are then delivered by the dispatcher as usual: nothing is replayed by resume.
7. **Close**: the snapshot is renamed `suspend.<taken_at>.json` (the last 5 kept); one line per project
   on stdout, the same summary.

A step that fails is said with its command and does not stop the next project. The pause is never
lifted on a project whose agents are not all back, unless `--partial`: a woken agent whose reviewer
is missing would stall, and a resume directive to a master that is missing would be lost.

## 6. The snapshot

`<state>/agentbus/<p>/suspend.json`, 0600 in a 0700 folder (the project's state directory,
`service.Paths.StateDir`), written to a temporary file in the folder and renamed. JSON, versioned:

```json
{
  "v": 1,
  "project": "p",
  "taken_at": 1793743724567,
  "host": "laptop",
  "by": "person-name",
  "reason": "OS updates",
  "herdr": {"session": "agent-bus", "workspace": "w-3", "reachable": true},
  "services": {"dispatch": {"was_active": true, "stopped": true}, "missions": {"was_active": true, "stopped": true}},
  "pause_replaced": {"kind": "", "reason": "quota anthropic", "until": 1793750000000},
  "agents": {
    "coder": {"tier": "boot", "role": "coder", "profile": "claude", "client": "claude-code",
              "provider": "anthropic", "model": "opus", "pane": "p-12", "session": "c0ffee…",
              "state": "idle", "working_at_suspend": false, "handoff_report": ""}
  },
  "open": {
    "asks": [{"id": "1793…-0", "agent": "coder", "questions": 2}],
    "gates": [{"agent": "foureyes", "ref": "1793…-0", "challenger": "coder", "subject": "x"}],
    "board": [{"task": "suspend-spec", "owner": "coder", "state": "accepted"}],
    "waiting": {"coder": 3},
    "handoffs": ["sentinel"],
    "missions": [{"change": "c", "state": "integrating"}]
  },
  "unknown": ["herdr: workspace not read (session not running)"]
}
```

Rules:

- **Identifiers, states and counts only.** No message text, no report text, no ask or gate text, no
  status message: what was open is read again from the bus at resume, where it still is. The only free
  text is `reason`, given by the person, and it goes through `health.Redact` (whole field on any hit),
  as the history archive does. No environment value, no URL, no path but the snapshot's own; nothing
  from the services env file but `HERDR_SESSION`.
- `session` is the verified `IdentSession` (never `Session`): the one the relaunch resumes.
- A read that fails is a line in `unknown`, never an empty list: `[]` means "read, none".
- The snapshot is not a backup. Redis is never written from it; `backup`/`restore` stay the way to
  restore records. It does not enter backup archives (it is runtime state of one host).

## 7. After a crash or a power cut (no suspend)

`resume --all` also serves a host that went down without a suspend. For each host project that is
**not** suspended:

- services installed and not active: started (as the workspace hook would; said, since the person
  asked for it);
- agents: the same relaunch as §5 step 4, but only when the workspace is open (a crash does not
  open a workspace the person had closed: `agentbus up <p> --resume` is named instead);
- no pause is touched (a timed pause runs its course);
- a summary to the person on stdout, and to master only when something was relaunched, saying what
  it could not know: `no suspend snapshot: turns running at the crash are lost; the open work below
  is what the bus holds now`, plus any snapshot found older than the last suspend's resume (ignored).

A project that **is** suspended takes §5 whatever the snapshot: missing, the live bus stands in, and
the summary says `no snapshot: read from the bus now`.

`resume --all` on a host where nothing is suspended and everything runs prints `nothing to resume`
per project and exits 0.

## 8. The workspace hook

`upHook` (herdr's `workspace.created`/`workspace.focused`) starts a project's stopped services. On a
suspended project it must not: it reads the pause (2 s), and on `kind = "suspend"` starts nothing and
prints `<p>: suspended since …: agentbus resume brings it back`. A pause it cannot read keeps today's
behaviour (start), since a broker that does not answer is the crash case, where starting is right;
the dispatcher then reads the suspend itself and wakes nobody. herdr's native restore of agents is not
ours to stop: an agent restored into a suspended project boots, declares idle, and is woken by nobody.

## 9. busmon

- **Header and state column**: a suspend pause reads `suspended since 21:40` (and `· by <name>`,
  `· <reason>` as room allows), in the pause segment's place and colour; agents read `paused` as
  under any pause (`stateLook`).
- **Key `Z`** (sleep; free on every table, `ModeShell`): one key for both, on the open project only.
  Not suspended: a confirmation `Suspend <p>? Working agents are asked to stop (up to 10 min), then the
  project is paused and its services stopped. y / n`. Suspended: `Resume <p>? Services, missing
  agents, then the pause lifted. y / n`. On `y`, busmon runs `agentbus suspend -p <p> --yes` or
  `agentbus resume --yes` as a child (as `--pick` runs `agentbus up`: busmon never starts a service
  itself, the CLI does), its output to `<state>/agentbus/<p>/suspend.log`, and shows its last line in
  the notice (`notice.go`) until it exits, then the outcome. A second `Z` while it runs says it runs.
- **The pause popup** (`pausepopup.go`, opened when the person sends to a paused project): on a
  suspended project its first button reads `Resume (services, agents)` and runs the same as `Z` then
  `y`; `Send anyway` stays (the entry waits for the resume).
- **Tour and help**: `Z` from the key table, as every key (`tourKeys`); no hand-written key in prose.

## 10. Failure table

| Case | suspend | resume |
|---|---|---|
| Broker down | refused for every project (nothing can be paused): exit 1 | §7 can start nothing that needs the bus; says `broker unreachable: start docker (docker compose up -d in <repo>)` and stops |
| A timed pause exists (the person's) | moved into `prev`; restored at resume if still in the future | `prev` back, no resume directive; summary says `paused until HH:MM by <name>` |
| A quota pause exists | moved into `prev` the same way; the dispatcher's quota pass re-judges it after resume (`FencedQuotaUnpause` ends it when the window clears) | same |
| Already suspended | skipped (`already suspended since …`), unless its snapshot is missing: steps 5-7 only | — |
| Resume of a project not suspended | — | §7 (crash path), or `nothing to resume` |
| An agent keeps working past `--wait` | refused for that project without `--force` (names it); `--force` records it | its turn is lost if the machine went down; the summary says `working at suspend` |
| An agent that refuses or never reads the directive | as above: only `working` agents are waited for, so an idle one never blocks | — |
| Open ask, gate, request, board task | recorded (ids, counts); never blocks | in the summary to master; they are still in Redis |
| Swap / reload / retire intent in flight | refused for that project, even with `--force` | an intent left by a crash expires (`PX 180000`); an interrupted ledger `begin` is closed by the next swap as today; the summary names it |
| Missions mid-integration | said; stopped as `down` stops it; redone at start | started; `Startup` repairs |
| No workspace open | snapshot records `herdr.reachable` and no pane; nothing else changes | §5: `up` creates the workspace; §7: not created, command named |
| herdr not running | suspend needs no herdr (bus only); `unknown` gets a line | services only, pause kept, the command to start the session (§5 step 2) |
| No service manager (tab mode) | services left (paused, inert), said | `up` relaunches the dispatch and missions tabs as it does today |
| macOS / launchd | `Manager.Stop` through launchctl as `down` | `Manager.Start`; agents relaunched by `up`'s rules, never proven (no `/proc`): `relaunched, not provable`; `--partial` is then needed to lift the pause (Q4) |
| Snapshot write fails | the project stays suspended; said with the retry | live bus read instead; said |
| Snapshot older than the pause's `at` | — | ignored and said; live bus read |
| Two suspends or resumes at once | each project under `upLock` (already per project, fails closed): the second says it is held | same |
| Master is missing at resume | — | the pause is not lifted without `--partial` (the resume directive would go to nobody); with it, the summary goes to the person on stdout and as a `for=person` notice |

## 11. Tests (the plan)

Every test runs on fakes or the disposable broker (`AGENTBUS_TEST_REDIS_URL`); none touches the
user's service manager, herdr or the production broker, and none starts a real session (`suspendDeps`
and `resumeDeps` injected like `startDeps`, `upDeps`).

- `bus` (Redis, opt-in): `Suspend` writes `kind`, moves a timed pause into `prev`, leaves an existing
  suspend; `FencedResume` never ends a suspend however due its `Until`; `SetPause` and `Resume` refuse
  it; `FencedQuotaPause` does not replace it; `Unsuspend` restores a future `prev` without a directive,
  deletes and directs otherwise, and refuses a record changed since it was read (compare-and-swap);
  an older reader (a `PauseRecord` without the fields) decodes the record; `HumanCmdTTL` writes
  `human=yes`, no `wake=no`, and an `expires_at` from Redis `TIME`, and drains as expired after it.
- `dispatch` (fake runtime, opt-in Redis): a suspended project gets no wake, clear, escalation or
  pop retire across many passes, with the suspend's `Until` set in the past to prove `kind` decides.
- `cmd/agentbus` suspend (pure `suspendPlan` + injected deps): only `working` agents get the directive;
  a dormant one does not; the wait ends early when they go idle; timeout without `--force` writes no
  pause; `--force` records them; an intent refuses even with `--force`; `--dry-run` writes nothing;
  the order is directive → pause → read → stop → snapshot (a fake records calls); a failed stop or
  write leaves the pause and says the retry; no terminal and no `--yes` exits 2.
- snapshot: golden JSON from a fixture; a reason holding a token is redacted whole; no field holds a
  message text (the fixture's messages carry a marker the test greps for); 0600 in 0700; written by
  rename; `unknown` filled for each failed read; `[]` only after a successful read.
- resume (pure `resumePlan`): services recorded active are started; relaunch called with `Resume:
  true` and never types into an existing pane (`typedIntoExisting` stays the invariant); pops are
  named, not spawned; the pause is not lifted while a boot agent is missing, lifted with `--partial`;
  `prev` restored when future; the summary is cut under `CmdMaxRunes` with the `+N more` tail; herdr
  not running starts services only; a crash host (no suspend) touches no pause and creates no closed
  workspace; idempotence: a second `resume --all` says `nothing to resume`.
- `upHook`: a suspended project's services stay stopped; an unreadable pause starts them.
- tool rules: every shipped role denies `agentbus suspend*` and `agentbus resume --all*`; `resume` on
  a suspended project refuses an agent caller.
- busmon: `Z` opens the right confirmation by the pause kind; `y` runs the fake child with the exact
  argv; the header golden for a suspended project; the pause popup's first button on a suspend.
- `TestEveryDispatchedCommandIsInTheTable` / `TestEveryFlagLiteralIsInTheTable` cover the new verbs.

## 12. Open questions (one recommendation each)

- **Q1. Verb.** `agentbus resume` does the full resume on a suspended project, and `resume --all` the
  host (recommended), or a separate verb (`agentbus wake`) leaving `resume` for timed pauses only?
- **Q2. Agents at suspend.** Leave them running, idle and paused (recommended: costs nothing, a
  reboot ends them, and resume finds them live), or close their panes (SIGTERM, as reload) so the
  machine is quiet and every agent comes back on its session at resume?
- **Q3. Pops.** Not relaunched, named to master with the command (recommended: a pop is spawned for
  one piece of work, and master decides whether it is still needed), or relaunched like boot agents?
- **Q4. Lifting the pause with agents missing.** Never without `--partial` (recommended), or lift when
  master alone is back, so master can re-spawn the rest itself?
- **Q5. Wait.** 10 minutes by default for working agents to reach a clean point (recommended), a
  setting `suspend.wait` (group Services) if the person wants another default?
- **Q6. Automatic resume.** None (recommended: waking the team is the person's act, as suspending
  it is), or should the workspace hook resume a suspended project when its workspace is focused?

## 13. Out of scope

- Restoring Redis from the snapshot (that is `restore`); snapshot inside backup archives.
- Starting herdr, docker or the broker (the person's terminal and docker own them).
- Enabling services at login, or any autostart.
- Saving uncommitted work for an agent: the directive asks it to commit; resume does not inspect
  worktrees.
- A host-wide dispatcher (decided, not built: `2026-10-26-host-dispatcher`): suspend works project by
  project and will keep doing so on top of it.
- Remote projects on another host: `--all` means this host's projects.
