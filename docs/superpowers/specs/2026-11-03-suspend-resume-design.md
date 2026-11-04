# Suspend and resume: put a whole host's team to sleep, and bring it back

Status: spec, answered (§12). Subject `suspend-spec`. Request: the person, 2026-11-03, after a
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

`PauseRecord` gains additive fields:

```go
Kind     string          `json:"kind,omitempty"`     // "" = a timed pause (today); "suspend"
Services map[string]bool `json:"services,omitempty"` // kind -> was active, read before the pause
Prev     *PauseRecord    `json:"prev,omitempty"`     // the pause the suspend replaced, if any
Host     string          `json:"host,omitempty"`     // a suspend's host (os.Hostname)
Boot     string          `json:"boot,omitempty"`     // that host's boot when it suspended (below)
```

`Boot` identifies the running boot of the host: Linux `/proc/sys/kernel/random/boot_id`, macOS
`kern.boottime` (`sysctl`), read once by `suspend`; empty when it cannot be read. It is only
compared (§8), never trusted for anything else.

A suspend pause has no deadline in meaning. Its `Until` is set to `at + 365 days`, so a dispatcher
of an older release, which does not know `kind`, never ends it in practice; busmon and `pause
--status` show it as `suspended since <at>`, never by its `Until`. `Services` is the service
manager's state read just before the pause is written, in the same record, so it is on the bus
**before any service is stopped** and nothing ever rewrites it (reviewer-codex: a retry after the
stops would otherwise read every service inactive).

### 3.1 The chosen design: pause first, then wait for quiet

Rounds 4 and 5 of review (both reviewers) showed that letting the dispatcher wake agents to read a
suspend directive while the project is not yet paused needs a barrier phase, a filtered wake path in
the dispatcher, a fenced commit and a predicate shared between the round and the commit, and still
left in-flight reservations crossing it. The smaller design closes the same races with less:

- **No directive, no hand-off.** A suspend does not ask agents to do anything. Their files stay on
  disk through a reboot, and resume reopens the same conversation (`--resume <IdentSession>`), so
  the only thing a suspend can lose is a turn cut in the middle. It therefore waits for running
  turns to end; it never starts a new one.
- **Pause first.** `Bus.Suspend` writes the suspend pause at once. From then on the dispatcher starts
  no new work: its pass already stops at the pause, and the one race left is a pass that read the
  project before the pause and reserves a wake after it.
- **The reservation is pause-aware** (the one dispatcher-side change, `bus/dispatchkeys.go`):
  `reserveWake`'s script returns "not reserved" when `{p}:pause` exists, in the same script as its
  lease check, for every pause (a timed pause gains the same guarantee). So once the pause is written,
  no wake can be reserved; a reservation written before it is visible as a wake record in state
  `prompting` (or `prompted` once typed), which the predicate below waits out.
- **One predicate, `suspendQuiet`**, pure, used by every round and by nothing else (there is no
  separate commit check that could judge differently, reviewer-codex and foureyes).

`suspendQuiet(facts)` reads, per agent of `{p}:agents`:

| Agent | Counts as |
|---|---|
| dormant (`bus.DormantAfter`, as the stall guard reads it) | ignored |
| its verified process proved gone (`dispatch.ProcGone`: `ErrNoProcess`, a pid readable `/proc` lists no more, a recycled pid) | ignored, recorded `gone` (a dead agent left `working` never blocks, reviewer-codex) |
| a wake record in state `prompting` | busy (`wake in flight`), **never released by age** (reviewer-codex: a dispatcher held between its reservation and its prompt may still type): it is resolved only by evidence that no late prompt remains possible, which gives its boundary `B` (below) |
| a wake record `prompted` or `unobserved` (the input may have been sent even when its marker was not seen) | busy (`woken, turn not seen ending`) until evidence later than its boundary `B` |
| otherwise | `bus.EffectiveState(snapshot, obs, now, turn_stale_after)` with `obs` an `Observation` built from the suspend's **own** pane inspection of this round (read-only, `Runtime.Inspect` on the verified pane, `since` the first round that saw that state): `working` = busy; a stale auto-state flip with the pane seen ready reads idle (foureyes), as everywhere else |

**The boundary `B`** of a wake is never the record's `At` (the reservation's time, which a prompt
typed later does not move). It is the first moment the suspend has proof that the wake's input can no
longer arrive later:

- the suspend's own read (Redis `TIME`) that first finds the record out of `prompting`, in `prompted`,
  `unobserved` or any later state: the dispatcher has returned from its prompt; or
- the moment the suspend proves the dispatcher gone: the dispatch service read inactive by the
  service manager **and** `{p}:dispatch` absent (the lease lapses within its `PX 15000` once the
  process has exited), both read after the pause. No process of ours can type into a pane then.

After `B`, the wake counts as answered only by a declaration or auto-state flip to a non-working
state whose time is later than `B`, or by own pane inspections taken at or after `B + settle`
(`settle` = 5 s, longer than a client's turn start after its input) that see the pane ready and not
busy in two consecutive rounds. A `prompting` record with the dispatcher alive and holding stays busy
until the timeout: the suspend then cancels, or `--force` goes on (the residual it accepts).

To reach the second proof quickly, the suspend stops the **dispatch** service right after the pause
(§4.1 step 2): a paused dispatcher wakes nobody anyway, and once it has exited no wake reserved
before the pause can still be typed. Missions stops with the rest at the end.

The suspend inspects the panes itself because a paused dispatcher no longer observes them; when herdr
cannot be reached, the inspection is absent and `EffectiveState` reads the snapshot alone, said in the
output. The project is **quiet** when no agent is busy in two consecutive rounds 2 s apart.

Nothing in the predicate can go from quiet to busy without a new turn, and with the pause in place and
reservations refused, the only new turns left are the person typing into a pane and an agent whose
`subscribe` is armed being sent a message by someone not paused (the person, or another project's
agent). Residual, stated: such a turn starting after the last round is cut by the reboot; the team's
agents do not stay armed while idle.

Rules, each in the one script that writes the key (so a reader never races a writer):

| Writer | On a suspend pause |
|---|---|
| `Bus.Suspend` (new, one script) | writes `kind=suspend`, `by`, `notify`, `at` and `Until` from Redis `TIME`, `reason`, `host`, `boot`, `services`; an existing timed pause (a person's or a quota pause) is moved into `prev` whole; an existing suspend is left as it is and reported (`already suspended since …`) |
| `Bus.SuspendCancel` (new, CAS on the record written) | a suspend that timed out without `--force`, called only **after** the CLI has started again every kind the record's `Services` marks active (§4.1 step 3), and never when one of those starts failed (the suspend then stays), so a process dying between the two leaves the record saying what to restart: `prev` back if still in the future, else the key deleted, and the pause's `notify` target gets today's resume directive (it may have been held from wakes during the wait) |
| `reserveWake` (dispatcher) | refuses while any pause exists (above) |
| `FencedResume` (dispatcher, when due) | never ends a suspend: returns 0 when the stored record decodes to `kind = "suspend"` (the dispatcher logs `paused`) |
| `FencedQuotaPause` | its script today replaces a pause whose `reason` equals `quota <provider>`, so a suspend whose reason the person typed as `quota anthropic` would be taken: the script refuses (returns 0) on a record that decodes to `kind = "suspend"`, before the reason test (foureyes) |
| `FencedQuotaUnpause*` | already deletes only its own exact record; unchanged |
| `SetPause` (`agentbus pause`) | refused: `project suspended since …: agentbus resume brings it back`; it moves to a script so the check and the write are one |
| `Bus.Resume` (`agentbus resume` today) | refused with `ErrSuspended`; the CLI's `resume` then runs the full resume (§5) instead, see §4's verb table |
| `Bus.Unsuspend` (new, one script, compare-and-swap on the record read) | if `prev` is a timed pause still in the future (Redis `TIME`), puts `prev` back as the pause (no resume directive: the team stays paused until that deadline, and the dispatcher ends it as usual); else deletes the key and XADDs the resume directive (§5 step 6) |

`{p}:pause` is already in `projectKinds` and is a record (restored by `restore`); no new key, no new
kind, so `TestEveryWrittenKeyKindIsClassified` needs nothing. Apart from `reserveWake`, the
dispatcher's pass is unchanged: a paused project already wakes, escalates, clears, observes, retires
and alerts nobody (`loop.go`, the `paused` check); the passes before it (quota, context, tokens,
hand-off voiding) only read or write records and run as they do under a timed pause.

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
person) or doing half the job. Decided (§12, Q1).

`suspend` and `resume --all` sit above the project-required exit (they name projects themselves, like
`backup`); plain `resume` stays where it is. Both are writes for the identity guard (`identityGuard`):
an agent runs them only as itself, and every role's tool rules **deny** `agentbus suspend*`,
`agentbus * suspend*`, `agentbus resume --all*` and `agentbus * resume --all*` (shared `[tool_rules]`,
like `reload --as`: the second form catches `-p <p>` before the verb, the usual spelling): the team never puts the host to
sleep or wakes it; the person does, from a shell or busmon. Master keeps today's `agentbus resume`
(a timed pause), which on a suspended project is refused for an agent (`ErrSuspended` read by the CLI
before §5 runs: only a person's process runs §5, decided as `notifications test` decides it, no
`AGENT_BUS_AGENT` and no process under a registered agent's verified pid). Defence in depth, not a
boundary, as for every tool rule.

Table entries in `cmd/agentbus/commands.go` (so `TestEveryDispatchedCommandIsInTheTable` and
`TestEveryFlagLiteralIsInTheTable` hold): `suspend` (flags above, short `-p` before the command
only, as today), and `resume` gains `--all`, `--partial`, `--dry-run`, `--yes`. Help text:

```text
agentbus suspend               put every project of this host to sleep: pause, let running turns
                               end, record a snapshot, stop the services
agentbus suspend -p <p>        one project
agentbus suspend --status      what is suspended, and what each snapshot holds
agentbus resume --all          bring every suspended project back: services, missing agents on
                               their sessions, then lift the pause and tell master what was open
```

### 4.1 `suspend`, per project, in this order

1. **Read and refuse early** (nothing written yet): the broker answers; the project is not already
   suspended (else say so, skip it, exit 0 for it, unless its snapshot is missing: see below); no
   swap, reload or retire intent is held (`{p}:swap:<role>`, `PX 180000`: `a swap of <role> is in
   flight; retry once it ends`, refused even with `--force`, since stopping under a relaunch half-done
   is the one state resume could not repair); a missions attempt `integrating` is said (not refused:
   `down` already interrupts it and the conductor redoes it at start, `Startup`).
2. **Pause**: read the service manager's state of each installed kind, then `Bus.Suspend` (§3) with
   it as `Services`. From here the dispatcher reserves no wake (§3.1) and the restart intent is on the
   bus, before anything is stopped. Then stop the **dispatch** service at once (`Manager.Stop`), so a
   wake reserved before the pause can no longer be typed (§3.1, the boundary `B`); with no service
   manager (tab mode) the dispatcher keeps running, and a `prompting` record resolves only when it
   leaves that state.
3. **Wait for quiet** (§3.1), re-reading every 2 s, at most `--wait` (default the setting
   `suspend.wait`, 10 min, §12 Q5; `--no-wait` and `suspend.wait = 0` mean a wait of zero: one
   round, then the same decision as a timeout, so they never skip step 2 nor the rule below,
   reviewer-codex). A zero wait cannot see quiet when evidence needs time: a stale auto-state flip
   reads idle only after `dispatch.turn_stale_after` (2 min) of rounds seeing its pane ready, and a
   wake needs `B + settle`; such an agent makes a zero-wait suspend cancel unless `--force`
   (foureyes). Each round prints the agents still busy and why (`coder: working (pane busy)`,
   `master: wake in flight`).

   On timeout without `--force`, cancel (foureyes): first start again every kind the record's
   `Services` marks active and that is not active now (the dispatch service step 2 stopped;
   `Manager.Start`, the restart budget reset first, as `service start` does), read from the record, not
   from memory; then `Bus.SuspendCancel`; exit 1, naming each agent still busy and since when. The
   project is then as it was: its services running, its previous timed pause back if any, and master's
   resume directive woken for by the dispatcher just restarted. A start that fails **keeps the
   suspend** (reviewer-codex): `SuspendCancel` does not run, so the record and its `Services` stay as the
   recovery intent; the CLI says `could not start <kind> again: <p> stays suspended; agentbus resume
   brings it back`, writes the same line as a `for=person` notice (busmon marks it unread), and exits
   1. Cancelling then would leave the services down with nothing on the bus to say which to start. A
   process dying before the cancel leaves a suspend whose `Services` resume reads as usual.

   The same cancel runs on every other way out of steps 2-3 (reviewer-codex): the person's `Ctrl+C`
   (SIGINT), a SIGTERM or a SIGHUP (a closed terminal or ssh session, or busmon exiting while its `Z`
   child runs, foureyes) during the wait, a failed early stop, and an error reading the facts for
   more than three rounds in a row; the signal is caught for the cancel's duration only (a second one
   exits at once, leaving the suspend). A SIGKILL or a crash cannot run it: the project then stays
   suspended with `Services` on the record, `suspend --status` says so, and `agentbus resume` (or
   `suspend -p <p>` to finish) restores the services from it.
   With `--force`, go on and record them as `busy at suspend` with their reason in the snapshot: their
   turn is cut if the machine goes down (the residual `--force` accepts, said on the terminal).
4. **Read the snapshot's facts** (§6) from the bus, herdr (if reachable) and the service manager.
5. **Stop the services** through the service manager (`Manager.Stop`, as `down` does), each kind the
   record's `Services` marks active (or found active now) and not already stopped at step 2. No service manager (tab mode): say
   `dispatch and missions run as herdr tabs: they stay; paused, they wake nobody`.
6. **Write the snapshot** (§6), its `services` copied from the record, then print one line per
   project: `suspended (N agents, M open items); services stopped; snapshot <path>`.

A failure at 4-6, or the process dying anywhere after step 2, leaves the project suspended with
`Services` intact: the bus is the truth, the snapshot a convenience. A suspend that died while
waiting is still a suspend (its services may still run): resume brings it back as any other, and
`agentbus suspend -p <p>` completes it. That retry, on a suspended project whose snapshot is missing
or older than its pause's `at`, runs steps 3-6 (waiting for quiet again) and **never rewrites
`Services`**; on a suspended project with a current snapshot it is a no-op (`already suspended since
…`). A record with no `Services` (the manager could not be read) makes resume start every installed
kind.

Projects are suspended one after the other, in the projects directory's order, each to the end;
`--project` narrows. `--dry-run` runs step 1 and prints what 2-7 would do, writing nothing.
Without `--yes` it asks once on the terminal for the whole list (`setup.OpenTTY`); no terminal and no
`--yes` prints the plan and exits 2, like `start`.

Agents are **left running**: idle, paused, they cost nothing, and a reboot ends them anyway. Closing
their panes was considered and set aside (§12, Q2).

## 5. `resume` of a suspended project, in this order

1. **Read**: the suspend pause (else this is §7's crash path), the snapshot if present and taken at or
   after the pause's `at` (an older one is ignored and said), the herdr session from the project's
   services env file (`HERDR_SESSION`, as `up` records it; the snapshot's copy when the env file
   cannot be read).
2. **herdr**: the session must run. When it does not, resume does not start herdr (it needs a
   terminal; `start` does that in the person's terminal): it says `herdr session <s> is not running:
   start it (herdr --session <s>, or agentbus start in <folder>) and run agentbus resume again`, and
   goes on with step 3 only; the pause stays (step 6 is not reached without `--partial`).
3. **Services**: start every kind the suspend record's `Services` marks active (the snapshot's copy
   is only read when the record has none), or, with neither, every installed kind (`Manager.Start`, the restart budget reset first, as `service start` does). The
   dispatcher starts on a paused project and wakes nobody yet.
4. **Agents**: `up`'s relaunch on the project's open workspace with `Resume: true` (`relaunchUp`, under
   `upLock`), so the existing rules decide each template tab: a live agent is left (herdr's native
   restore may already have brought it back), a dead one is relaunched in a new pane split from its
   tab or a new tab, on its `IdentSession`; nothing ever types into an existing pane. A workspace
   not open is created by `up`'s own path (`Create: true`). Pops are **not** relaunched (they are
   spawned for one piece of work; their template is not the project's): the summary names each with
   its session and the command (`agent-spawn <role> <p> --resume <id>`) for master (§12, Q3).
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

**What resume keeps.** A resume never changes a project's configuration: `up` runs with `KeepEnv`, so the
project's services env file stays as it is (the escalation target, the reload policy and every key set by hand,
such as `AGENT_BUS_AUTO_CLEAR` or a quota value), only `HERDR_SESSION` and `HERDR_SOCKET_PATH` taken from the
session resume runs in. The keys the caller's environment would have changed are named on stdout (never a
value), with `agentbus up <p>` as the way to apply them; an env file that cannot be read installs nothing
rather than replacing it. Resume holds the project's `upLock` from its first read to its last write, so a suspend
or another resume of the project is refused, not interleaved. A service the record wants that cannot be read or
started keeps the suspend whatever `--partial` says (`--partial` is about agents; a project without its dispatcher
wakes nobody), and so does a template that cannot be read (nothing is done). The agents waited for are the
template's agent tabs plus every non-pop agent the snapshot knows alive at the suspend (with no snapshot, every
verified non-pop agent of the bus), dormant ones excepted.

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
  "boot": "6f1c…",
  "by": "person-name",
  "reason": "OS updates",
  "herdr": {"session": "agent-bus", "workspace": "w-3", "reachable": true},
  "services": {"dispatch": {"was_active": true, "stopped": true}, "missions": {"was_active": true, "stopped": true}},
  "pause_replaced": {"kind": "", "reason": "quota anthropic", "until": 1793750000000},
  "agents": {
    "coder": {"tier": "boot", "role": "coder", "profile": "claude", "client": "claude-code",
              "provider": "anthropic", "model": "opus", "pane": "p-12", "session": "c0ffee…",
              "state": "idle", "busy_at_suspend": "", "handoff_report": ""}
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
suspended project it reads the pause (2 s) and, by the setting `suspend.auto_resume` (§12, Q6):

- **off** (the default): it starts nothing and prints `<p>: suspended since …: agentbus resume brings
  it back`;
- **on**, and only **after a reboot or a crash**: the record's `Host` is this host and its `Boot` is
  non-empty and differs from the current boot (foureyes: focus events are frequent, and a person who
  suspends from busmon inside that very workspace and switches back to it before the reboot would
  otherwise wake the team just before the machine goes down). On the same boot, or with either boot
  unknown, or another host, it starts nothing and prints `<p>: suspended: agentbus resume brings it
  back` (one message fits every case, foureyes). Otherwise it starts the full resume of that project (§5), exactly as `agentbus resume -p <p> --yes`
  would, with the same rules: services, `up`'s relaunch with `--resume` (never typing into an existing
  pane), the proof, and the pause lifted only when every boot agent is back (no `--partial` from a
  hook, ever: a missing agent leaves the project suspended, said in the log and by a `for=person`
  notice). The hook must not hold herdr's event: it starts the resume as a detached child
  (`setsid`, as `report-resume`'s sender), its output in `<state>/agentbus/<p>/suspend.log`, and
  returns at once. `upLock` makes a second event while one runs a no-op (`resume already running`).
  It tries **once per boot and suspend** (foureyes): the boot and the suspend's `at` are recorded in
  `<state>/agentbus/<p>/auto-resume.json` under the lock, released before the spawn (the resume takes it
  without waiting; the mark, not the lock, keeps a second event from starting another), and a later focus on that boot,
  the resume having left the project suspended, prints `suspended: the auto-resume on this boot did not
  complete (agentbus resume brings it back)` and starts nothing, so a missing agent never earns the
  person a new resume and a new notice at every focus. A mark that cannot be written starts nothing.
  The resume directive and the notice are written as the person (`identity.as`), since the setting is
  the person's standing consent; with no recorded name the hook does not resume and says why.

A pause it cannot read keeps today's behaviour (start the services, never a resume), since a broker
that does not answer is the crash case, where starting is right; the dispatcher then reads the suspend
itself and wakes nobody. herdr's native restore of agents is not ours to stop: an agent restored into
a suspended project boots, declares idle, and is woken by nobody (or, with `auto_resume` on, is found
live by the resume's relaunch).

Residual, stated: with `auto_resume` on, an agent can start a resume after a reboot through herdr
(focusing or creating the workspace from the herdr skill), which the deny rules on `suspend` and
`resume --all` do not cover. It is opt-in, off by default, and limited to a new boot; the person who
turns it on accepts that any focus after a reboot resumes, whoever focuses.

## 9. busmon

- **Header and state column**: a suspend pause reads `suspended since 21:40` (and `· by <name>`,
  `· <reason>` as room allows), in the pause segment's place and colour; agents read `paused` as
  under any pause (`stateLook`).
- **Key `Z`** (sleep; free on every table, `ModeShell`): one key for both, on the open project only.
  Not suspended: a confirmation `Suspend <p>? The project is paused now, running turns may end (up to <suspend.wait>),
  then its services stop. y / n`. Suspended: `Resume <p>? Services, missing
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
| Already suspended | skipped (`already suspended since …`), unless its snapshot is missing: steps 5-7 only, `Services` kept | — |
| A suspend that died while waiting | the project is suspended (services perhaps still running); `suspend -p <p>` completes it, `Services` kept | resumes it as any suspend |
| A wake reserved just before the pause | waited out (`wake in flight`, then until its turn is seen ending) | — |
| A dormant agent, or one whose process is gone, left `working` | ignored, recorded (`gone`) | relaunched by `up`'s rules if it is a boot agent |
| Resume of a project not suspended | — | §7 (crash path), or `nothing to resume` |
| An agent keeps working past `--wait` | cancelled for that project without `--force` (names it, dispatch started again, prior pause back; if dispatch cannot start again, the suspend stays and `agentbus resume` is named); `--force` records it | its turn is lost if the machine went down; the summary says `busy at suspend` |
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

- `bus` (Redis, opt-in): `Suspend` writes `kind`, the far `Until`, `services`, `host`, `boot`, moves
  a timed pause into `prev`, leaves an existing suspend; `SuspendCancel` restores a future `prev`,
  else deletes and sends the resume directive, and refuses a changed record; `reserveWake` refuses
  while any pause exists (a timed one and a suspend) and still refuses a lost lease first;
  `FencedResume` never ends a suspend however due its `Until`; `SetPause` and `Resume` refuse it;
  `FencedQuotaPause` does not replace it, even when its reason reads `quota <provider>`; `Unsuspend`
  restores a future `prev` without a directive, deletes and directs otherwise, and refuses a record
  changed since it was read (compare-and-swap); an older reader (a `PauseRecord` without the
  fields) decodes the record.
- `dispatch` (fake runtime, opt-in Redis), the interleavings reviewer-codex named, on the real
  `prompt` path with its hooks: a pass that read the project before the pause and reaches
  `ReserveWake` after it reserves nothing and types nothing; a pass stopped at `afterReserve` (the
  reservation written, the prompt not sent) while the pause is written: the suspend reads
  `prompting` and stays busy **past the prompt budget** while the dispatcher holds there with its
  lease alive and the pane still ready (no release by age); released, the prompt is typed and the
  suspend stays busy until evidence later than `B` (the turn seen starting then ending); the same with
  the dispatcher stopped instead of released: `B` is the proof it is gone, and a prompt it typed just
  before exiting is caught by the inspections after `B + settle`; a prompt sent whose marker is not
  seen (`unobserved`) is busy like `prompted`; a non-working declaration between the reservation and
  the actual prompt does not end the wait (it is not later than `B`);
  a suspended project gets no wake, clear, escalation or pop retire across many passes, with the
  suspend's `Until` set in the past to prove `kind` decides.
- `suspendQuiet` (pure, table): dormant working ignored; a gone process working ignored and recorded;
  `prompting` busy at any age while the dispatcher is not proved gone; `prompted` and `unobserved`
  busy until a non-working declaration or flip later than `B`, or two ready inspections at or after
  `B + settle`; `B` is the suspend's read or the dispatcher's proved exit, never the record's `At`; a stale working flip with a ready, not busy inspection is quiet (foureyes); a declared
  working with no inspection is busy; quiet needs two consecutive rounds.
- `cmd/agentbus` suspend (pure `suspendPlan` + injected deps, a fake bus, herdr and manager): the
  order is services read → pause (with `Services`) → wait → read → stop → snapshot (a fake records
  calls); timeout without `--force` starts dispatch again from the record's `Services` (budget reset)
  before `SuspendCancel`, the prior timed pause back, and the dispatcher, restarted, wakes master for
  the resume directive (fake manager and dispatcher); a start that fails does not cancel: the record and `Services` are kept,
  the notice and the `agentbus resume` line are printed, exit 1, and a later `resume` starts dispatch
  from the record;
  a process killed between the start and the cancel leaves the suspend with `Services` intact;
  SIGINT, SIGTERM and SIGHUP during the wait, a failed early stop and repeated read errors run the same
  cancel (dispatch started again, prior pause kept); a second signal exits leaving the suspend, which
  `resume` then restores from `Services`; `--force`
  records the busy agents and their reasons; `--no-wait` and `suspend.wait = 0` pause, judge one round,
  and then cancel or (with `--force`) go on, never leaving a suspend without `Services`; an intent
  refuses even with `--force`; `--dry-run` writes nothing. Service intent (reviewer-codex): dispatch
  and missions active, a failure (or the process killed) after the first stop, and after both stops
  before the snapshot: the record still marks both active; the retry stops what is left and leaves
  `Services` alone; resume then starts both. A record with no `Services` makes resume start every
  installed kind. No terminal and no `--yes` exits 2.
- snapshot: golden JSON from a fixture; a reason holding a token is redacted whole; no field holds a
  message text (the fixture's messages carry a marker the test greps for); 0600 in 0700; written by
  rename; `unknown` filled for each failed read; `[]` only after a successful read.
- resume (pure `resumePlan`): services recorded active are started; relaunch called with `Resume:
  true` and never types into an existing pane (`typedIntoExisting` stays the invariant); pops are
  named, not spawned; the pause is not lifted while a boot agent is missing, lifted with `--partial`;
  `prev` restored when future; the summary is cut under `CmdMaxRunes` with the `+N more` tail; herdr
  not running starts services only; a crash host (no suspend) touches no pause and creates no closed
  workspace; idempotence: a second `resume --all` says `nothing to resume`.
- `upHook`: with `auto_resume` off a suspended project's services stay stopped; on, with the same
  boot (focus right after the suspend) nothing starts and the message says `suspended: agentbus resume brings it back`;
  with an empty recorded or current boot, or another host, nothing starts; with another boot the hook starts
  the detached resume (fake child, exact argv, `--partial` never passed) and returns at once; a second
  event while it runs is a no-op; no recorded person name, no resume; an unreadable pause starts the
  services and resumes nothing.
- tool rules: every shipped role denies `agentbus suspend*`, `agentbus -p x suspend`, `agentbus resume --all*` and `agentbus -p x resume --all`; `resume` on
  a suspended project refuses an agent caller.
- busmon: `Z` opens the right confirmation by the pause kind; `y` runs the fake child with the exact
  argv; the header golden for a suspended project; the pause popup's first button on a suspend.
- `TestEveryDispatchedCommandIsInTheTable` / `TestEveryFlagLiteralIsInTheTable` cover the new verbs.

## 12. Decisions (the person's answers, 2026-11-03, through master)

Every question was answered with the recommendation (Q6 made configurable); none is left open.

- **Q1. Verb: `resume`.** `agentbus resume` does the full resume (§5) on a suspended project and stays
  today's verb for a timed pause; `resume --all` covers the host. No second verb.
- **Q2. Agents are left open** at suspend: idle and paused, they cost nothing, a reboot ends them, and
  resume finds them live. Nothing closes their panes.
- **Q3. Pops are not relaunched.** The summary names each with its session and the command
  (`agent-spawn <role> <p> --resume <id>`); master decides whether it is still needed.
- **Q4. The pause is never lifted with a boot agent missing** unless the person passes `--partial`.
- **Q5. Wait: 10 minutes by default, and a setting.** `suspend.wait` in the registry (`config/keys.go`,
  group Services, env `AGENT_BUS_SUSPEND_WAIT`, kind Duration, default 10 min, read by `suspend`,
  `fileDuration`; `0` = do not wait, the same as `--no-wait`); `--wait` on the command line wins.
  It shows on busmon's C page with the other Services keys, and the `Z` confirmation names its value.
  Its test: the registry's own tests (group held, env literal only in the registry) plus `suspend`
  reading `--wait`, then the env, then config.toml, then the default.
- **Q6. No automatic resume by default, configurable.** The setting `suspend.auto_resume`
  (`config/keys.go`, group Services, env `AGENT_BUS_SUSPEND_AUTO_RESUME`, kind Bool, default `false`,
  read by the workspace hook; shown on busmon's C page with the other Services keys, an on/off).
  Off, the hook only refrains from starting a suspended project's services; on, focusing or creating
  the project's workspace starts its full resume, with the same rules (§8). The registry's tests hold
  its group and env literal; the hook's tests cover both values.

## 13. Out of scope

- Restoring Redis from the snapshot (that is `restore`); snapshot inside backup archives.
- Starting herdr, docker or the broker (the person's terminal and docker own them).
- Enabling services at login, or any autostart.
- Asking agents to commit or hand off: their files stay on disk and their sessions are resumed
  (§3.1); resume does not inspect worktrees.
- A host-wide dispatcher (decided, not built: `2026-10-26-host-dispatcher`): suspend works project by
  project and will keep doing so on top of it.
- Remote projects on another host: `--all` means this host's projects.
