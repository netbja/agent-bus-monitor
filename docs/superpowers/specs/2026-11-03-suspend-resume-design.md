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
Kind     string            `json:"kind,omitempty"`     // "" = a timed pause (today); "suspend"
Phase    string            `json:"phase,omitempty"`    // a suspend's phase: "draining", then "suspended"
Drain    map[string]string `json:"drain,omitempty"`    // draining: agent -> its suspend directive's id
Services map[string]bool   `json:"services,omitempty"` // suspended: kind -> was active before any stop
Prev     *PauseRecord      `json:"prev,omitempty"`     // the pause the suspend replaced, if any
Host     string            `json:"host,omitempty"`     // a suspend's host (os.Hostname)
Boot     string            `json:"boot,omitempty"`     // that host's boot when it suspended (below)
```

`Boot` identifies the running boot of the host: Linux `/proc/sys/kernel/random/boot_id`, macOS
`kern.boottime` (`sysctl`), read once by `suspend`; empty when it cannot be read. It is only
compared (§8), never trusted for anything else.

A suspend has two phases (reviewer-codex: the team must be quiesced, not merely the agents first
seen working):

- **draining**: the barrier. The project is paused for everything but the suspend itself: the
  dispatcher wakes only an agent listed in `Drain` whose directive it has not yet acknowledged (§3.1),
  so ordinary traffic, a person's message included, waits; it escalates, clears, retires and alerts
  nobody; it still observes the panes (the wait reads `EffectiveState`, which needs observations).
  `Until` is a real deadline, `at + wait + 2 min`: if the suspend process dies, the dispatcher ends
  the draining when due (§3.1) and the project is as it was.
- **suspended**: the project sleeps. `Until` is set to `at + 365 days`, so a dispatcher of an older
  release, which does not know `kind`, never ends it in practice; busmon and `pause --status` show it
  as `suspended since <at>`, never by its `Until`. `Services` is written in the same script that
  enters this phase, from the service manager's state read just before, and **before any service is
  stopped**; nothing rewrites it afterwards (a retry and the resume read it from the record).

An older dispatcher that does not know `kind` reads a draining record as a plain pause and wakes
nobody, the directive's target included: the suspend then runs into its timeout, which is safe.

Rules, each in the one script that writes the key (so a reader never races a writer):

| Writer | On a suspend pause |
|---|---|
| `Bus.SuspendBegin` (new, one script) | writes `kind=suspend`, `phase=draining`, `by`, `notify`, `at` and `Until` from Redis `TIME`, `reason`, `host`, `boot`, an empty `drain`; an existing timed pause (a person's or a quota pause) is moved into `prev` whole; an existing suspend in either phase is left as it is and reported (`already suspended since …` / `a suspend is in progress`) |
| `Bus.SuspendDrain` (new, CAS on the record read) | adds `agent -> directive id` to `drain` (the directive is XADDed in the same script, so the barrier knows it before the dispatcher can see it) |
| `Bus.SuspendCommit` (new, one script, CAS on the draining record read) | §4.1 step 4: refuses (returns which) when any agent of `{p}:agents` decodes to state `working` (declared or flipped) and is not in the `--force` list, or when a wake record of the project (`{p}:wake:<a>`, keys passed) is `prompted` at or after the draining's `at` for an agent not yet answered; else writes `phase=suspended`, `Services`, the far `Until`, drops `drain` |
| `Bus.SuspendCancel` (new, CAS) | ends a draining that timed out or was refused: `prev` back if still in the future, else the key deleted; no resume directive (nothing was stopped), one `for=person` notice `suspend of <p> cancelled: <why>` |
| `FencedResume` (dispatcher, when due) | a **suspended** record is never ended (returns 0, logs `paused`); a **draining** record past its `Until` is ended exactly as `SuspendCancel` does (the suspend process died), its notice from `dispatch` |
| `FencedQuotaPause` | its script today replaces a pause whose `reason` equals `quota <provider>`, so a suspend whose reason the person typed as `quota anthropic` would be taken: the script refuses (returns 0) on a record that decodes to `kind = "suspend"`, before the reason test (foureyes) |
| `FencedQuotaUnpause*` | already deletes only its own exact record; unchanged |
| `SetPause` (`agentbus pause`) | refused in either phase: `project suspended since …: agentbus resume brings it back`; it moves to a script so the check and the write are one |
| `Bus.Resume` (`agentbus resume` today) | refused with `ErrSuspended`; the CLI's `resume` then runs the full resume (§5) instead, see §4's verb table |
| `Bus.Unsuspend` (new, one script, compare-and-swap on the record read) | only from **suspended**: if `prev` is a timed pause still in the future (Redis `TIME`), puts `prev` back as the pause (no resume directive: the team stays paused until that deadline, and the dispatcher ends it as usual); else deletes the key and XADDs the resume directive (§5 step 6) |

`{p}:pause` is already in `projectKinds` and is a record (restored by `restore`); no new key, no new
kind, so `TestEveryWrittenKeyKindIsClassified` needs nothing.

### 3.1 The dispatcher

A **suspended** project changes nothing in the pass: a paused project already wakes, escalates,
clears, observes, retires and alerts nobody (`loop.go`, the `paused` check); the passes before it
(quota, context, tokens, hand-off voiding) only read or write records and run as they do under a
timed pause.

A **draining** project takes a narrower path in `paused`: when the record is due, `FencedResume`
cancels it (above); else the pass goes on to the observation and to the wakes, where only the agents
of `Drain` whose directive id is still unacknowledged (`Obligation`: above `LastDelivered`, or in
`PendingIDs`) are passed to `Decide`, every other agent treated as owed nothing; no escalation, no
clear, no pop retire, no context alert, no prompt notice. An agent that has read its directive is
never woken again by this suspend, so a woken master or reviewer cannot start ordinary work through
the dispatcher while the barrier holds. `Decide` itself is unchanged (pure); the filter is in the
loop, read from the record of the same pass.

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
2. **Raise the barrier**: `Bus.SuspendBegin` (§3), phase **draining**, `Until` = now + `--wait` +
   2 min. From here the dispatcher wakes only the suspend's own directives (§3.1). With `--no-wait`,
   steps 3-4 are skipped and step 5 runs at once (agents still working are recorded as such, as
   with `--force`).
3. **Ask, and wait**, re-reading every 2 s, at most `--wait` (default the setting `suspend.wait`,
   10 min, §12 Q5). Each round reads every registered, non-dormant agent (`bus.DormantAfter`), not a
   set frozen at the start (reviewer-codex), and:
   - an agent whose effective state (`bus.EffectiveState`, the stall guard's reading) is `working` and
     that has no unanswered directive of this run gets one, through `Bus.SuspendDrain` (the directive
     and its `drain` entry in one script), waking, from the person, with an expiry of the deadline;
     an agent that answered and is seen `working` again (a person typing in its pane, an armed agent
     woken by a message) gets a new one with a new nonce; the helper is `Bus.HumanCmdTTL`'s fields
     (`human=yes`, waking, `expires_at` from Redis `TIME`), so a directive still unread after the
     suspend drains as expired, never as a stale order:

     > Suspend of <p> requested by <person>: finish or stop your current step at a clean point,
     > commit your work in progress on your branch, report where you are (agentbus report), then
     > declare idle with exactly this message: agentbus status idle "suspended <nonce>". Do not
     > declare a hand-off: your session is resumed as it is.

     `<nonce>` is 8 hex characters from `crypto/rand`, one per directive, kept in the suspend
     process's memory only (never in the snapshot or the record): it is how the wait tells this
     directive's answer from the agent's own end-of-step idle.
   - an agent that got a directive has **answered it** when all of: its directive is acknowledged (the
     id at or below the group's `LastDelivered` and not in its `PendingIDs`, `Obligation`; a drain ACKs
     after output); its last **declaration** (`DeclaredTS`, never an auto-state flip) is later than the
     directive's id, declares a state other than `working`, with exactly the message `suspended
     <nonce>` (compared, never stored); and its effective state is not `working`.
   - the round is **quiet** when every agent read is either answered or not `working` and owes no
     answer, and no agent has a directive unanswered.

   Neither a turn end nor the agent's own end-of-step idle answers a directive (foureyes): while the
   barrier holds, the dispatcher still wakes that agent for its directive, which it reads and acts on
   in a turn of its own (commit, report); only the nonce, learnt from the directive alone, proves the
   declaration answers it. Agents idle, blocked or waiting and never seen working get no directive
   (they are at a clean point, and the barrier keeps them there; their asks and gates persist).

   Not a hand-off: a hand-off would make the dispatcher type `/clear`, and the point of a suspend is to
   resume the *same* conversation (`--resume <IdentSession>`); the draining phase already stops the
   clear pass, but the directive says it anyway.
4. **Commit**, on the first quiet round: read the service manager's state of each installed kind, then
   `Bus.SuspendCommit` (§3) with that state as `Services`. The script re-checks, against the stored
   snapshots and wake records, that no agent turned `working` and no wake was typed for an unanswered
   agent since the round read them; a refusal is not an error, the wait goes on (and its agent gets a
   directive at the next round). From here the project is **suspended** and its services' restart
   intent is on the bus, before anything is stopped (reviewer-codex).

   On timeout: without `--force`, `Bus.SuspendCancel`, exit 1, and name each agent still working or
   not answered and since when; nothing was stopped, the directives already sent expire, and an agent
   that stops later has lost nothing. With `--force`, commit with those agents in the script's
   `--force` list and record them as `working at suspend` in the snapshot.
5. **Read the snapshot's facts** (§6) from the bus, herdr (if reachable) and the service manager.
6. **Stop the services** through the service manager (`Manager.Stop`, as `down` does), each kind the
   record's `Services` marks active (or found active now). No service manager (tab mode): say
   `dispatch and missions run as herdr tabs: they stay; paused, they wake nobody`.
7. **Write the snapshot** (§6), its `services` copied from the record, then print one line per
   project: `suspended (N agents, M open items); services stopped; snapshot <path>`.

A failure at 5-7, or the process dying there, leaves the project suspended with `Services` intact
(step 4 holds): the bus is the truth, the snapshot a convenience. It is said, with the command to
retry: `agentbus suspend -p <p>` on a **suspended** project whose snapshot is missing or older than
its pause's `at` completes steps 5-7 only, stopping whatever is still active and **never rewriting
`Services`** (a retry after every stop succeeded would otherwise read both services inactive). On a
suspended project with a current snapshot it is a no-op (`already suspended since …`). A record with
no `Services` (the manager could not be read at commit) makes resume start every installed kind.

Residual, stated: an agent whose subscribe is armed can be woken by a message addressed to it between
the last round and the commit, and start a turn before its turn-start hook flips it `working`; the
commit's re-check then sees it idle. The window is the hook's latency; the team's agents do not stay
armed while idle (`no idle subscribe`), and every other sender is either paused or the person who is
suspending.

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
   A suspend directive still unread (only after `--force`, or an agent that never declared) counts
   in the agent's obligation, since `Obligation` does not read `expires_at`: the dispatcher wakes that
   agent once and the drain shows it as `expired` with no payload. Harmless, and said in the
   summary (`coder: 1 expired suspend directive`) rather than special-cased.
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
  "boot": "6f1c…",
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
suspended project it reads the pause (2 s) and, by the setting `suspend.auto_resume` (§12, Q6):

- **off** (the default): it starts nothing and prints `<p>: suspended since …: agentbus resume brings
  it back`;
- **on**, and only **after a reboot or a crash**: the record's `Host` is this host and its `Boot` is
  non-empty and differs from the current boot (foureyes: focus events are frequent, and a person who
  suspends from busmon inside that very workspace and switches back to it before the reboot would
  otherwise wake the team just before the machine goes down). On the same boot, or with either boot
  unknown, or another host, it starts nothing and prints `<p>: suspended on this boot: agentbus
  resume brings it back`. Then it starts the full resume of that project (§5), exactly as `agentbus resume -p <p> --yes`
  would, with the same rules: services, `up`'s relaunch with `--resume` (never typing into an existing
  pane), the proof, and the pause lifted only when every boot agent is back (no `--partial` from a
  hook, ever: a missing agent leaves the project suspended, said in the log and by a `for=person`
  notice). The hook must not hold herdr's event: it starts the resume as a detached child
  (`setsid`, as `report-resume`'s sender), its output in `<state>/agentbus/<p>/suspend.log`, and
  returns at once. `upLock` makes a second event while one runs a no-op (`resume already running`).
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

- **Header and state column**: a draining suspend reads `suspending… (N to answer)`; a suspend pause reads `suspended since 21:40` (and `· by <name>`,
  `· <reason>` as room allows), in the pause segment's place and colour; agents read `paused` as
  under any pause (`stateLook`).
- **Key `Z`** (sleep; free on every table, `ModeShell`): one key for both, on the open project only.
  Not suspended: a confirmation `Suspend <p>? Working agents are asked to stop (up to <suspend.wait>), then the
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
| Already suspended | skipped (`already suspended since …`), unless its snapshot is missing: steps 5-7 only, `Services` kept | — |
| A suspend in progress (draining) | refused (`a suspend is in progress`); a dead one is cancelled by the dispatcher at its deadline | refused until it commits or is cancelled |
| An agent starts working during the wait | it gets its directive and holds the commit (§4.1 step 3) | — |
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

- `bus` (Redis, opt-in): `SuspendBegin` writes `kind`, `phase=draining`, a real `Until`, moves a timed
  pause into `prev`, leaves an existing suspend in either phase; `SuspendDrain` adds the entry and
  XADDs the directive in one script, refused on a changed record; `SuspendCommit` refuses while an
  agent's stored state is `working` (declared or flipped) and not forced, and while a wake record is
  `prompted` since the draining began for an unanswered agent, and otherwise writes `phase=suspended`
  and `Services` and the far `Until`; `SuspendCancel` restores a future `prev`, else deletes, and
  sends the person's notice; `FencedResume` never ends a suspended record however due its `Until`,
  and cancels a draining one once due; `SetPause` and `Resume` refuse both phases; `FencedQuotaPause`
  does not replace either, even when its reason reads `quota <provider>`; `Unsuspend` refuses a
  draining record, restores a future `prev` without a directive, deletes and directs otherwise, and
  refuses a record changed since it was read (compare-and-swap); an older reader (a `PauseRecord`
  without the fields) decodes the record; the directive carries `human=yes`, no `wake=no`, an
  `expires_at` from Redis `TIME`, and drains as expired after it.
- `dispatch` (fake runtime, opt-in Redis): a suspended project gets no wake, clear, escalation or
  pop retire across many passes, with the suspend's `Until` set in the past to prove `kind` decides;
  a draining project wakes only an agent of `drain` whose directive is unacknowledged (master idle
  with an ordinary message waiting is not woken; an agent that read its directive is not woken again),
  escalates and clears nobody, still observes, and a due draining record is cancelled.
- `cmd/agentbus` suspend (pure `suspendPlan` + injected deps, a fake bus, dispatcher and manager):
  the barrier is raised before any directive; a dormant agent gets none; the wait ends once each
  directive is answered (acknowledged, a later declaration non-working with its nonce, not
  effectively working) and not at a mere turn end, an auto-state flip or the agent's own idle declared
  after the directive without the nonce (the race: the agent declares idle of its own, a fake
  dispatcher then wakes it for the directive, and the wait goes on until `suspended <nonce>`); a wrong
  or another agent's nonce does not end it; the nonce appears in no snapshot or record field.
  Barrier cases (reviewer-codex): an agent idle at the start and seen working during the wait gets a
  directive and holds the commit until it answers; an answered agent seen working again gets a new
  nonce; a commit raced by a wake (the fake turns an agent `working` between the quiet round and the
  script) is refused and the wait goes on. Timeout without `--force` cancels the draining and stops
  nothing; `--force` commits with them listed; an intent refuses even with `--force`; `--dry-run`
  writes nothing; the order is barrier → directives → wait → commit (with `Services`) → read → stop →
  snapshot (a fake records calls). Service intent (reviewer-codex): dispatch and missions active, a
  failure (or the process killed) after the first stop, and after both stops before the snapshot: the
  record still marks both active; the retry stops what is left and leaves `Services` alone; resume
  then starts both. A record with no `Services` makes resume start every installed kind. No terminal
  and no `--yes` exits 2.
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
  boot (focus right after the suspend) nothing starts and the message says `suspended on this boot`;
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
- Saving uncommitted work for an agent: the directive asks it to commit; resume does not inspect
  worktrees.
- A host-wide dispatcher (decided, not built: `2026-10-26-host-dispatcher`): suspend works project by
  project and will keep doing so on top of it.
- Remote projects on another host: `--all` means this host's projects.
