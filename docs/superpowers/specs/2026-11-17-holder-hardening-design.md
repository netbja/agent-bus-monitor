# Holder hardening: what a holder death, restart or reboot does today, and what to change

Status: spec, r4, for review (subject `holder-hardening-spec`). r4: master (the person asked how
our holder compares with herdr-core: §7, Q8). r3: reviewer-codex (the wait only at
condition 2b of an otherwise eligible unplanned restart; intentional, suspended and the earlier
conditions keep their verdict at once), foureyes (the notice names the wait). r2: reviewer-codex (the reboot
judgement must wait for the holder to answer: H1b, and reboot-resume §3 amended), foureyes's notes
(an older build's agents after a reboot, H3's notice mark, Q7's cost). Not built. Base: origin/dev
`079ed636`. Author: coder, task `holder-hardening-spec`, from master's directive of 2026-11-17
relaying the person's request for holder robustness. One PR (this spec); the build is §9's steps.
The person's decisions are §8.

## 1. Why

Since the herdr exit (decision 2026-11-15) every agent's terminal lives in one process per project
and build: the holder (`holder/`, run as a service unit by `service/holder.go`). On 2026-11-16 the
holder of agent-bus-monitor would not start (its socket path passed the limit, fixed by #553, #556
and #562) and the team was down until the person found it. The person asks what happens when the
holder dies, restarts or the machine reboots, how it comes back, who is told, how it is tested, and
whether an update can be one command.

## 2. Today, measured on `079ed636`

### 2.1 The holder crashes

| Fact | Where |
|---|---|
| The unit restarts it on failure after 2 s, at most 5 starts in 300 s; an exit 0 is not restarted. launchd: `KeepAlive` on an unsuccessful exit, throttle 10 s | `service/holder.go:28-31, 345-361, 513-516` |
| `KillMode=control-group`: every agent dies with the holder. Even outside its cgroup an agent could not outlive it: the holder owns each terminal's PTY master, and its close hangs the client up | `service/holder.go:360` |
| The restarted holder is empty. The dispatcher's look at a terminal of the dead one reads it gone (`Inspection.Gone`), never ready, so no wake is typed | `dispatch/hostruntime.go:114-125` |
| Health `holder` (every minute, in the dispatch service) warns `holder_restarted`, naming the agents that died; its handler is **sentinel**, which died with the holder | `health/health.go:123`, `cmd/agentbus/healthchecks.go:87` |
| Self-repair relaunches each dead agent, resumed from `{p}:resume`, after a 60 s grace and 3 dead reads; **one relaunch per pass, 2 minutes apart** (`ProjectPause`), master first; per agent at most one per 30 min and 3 per day | `selfrepair/decide.go:29-32, 197, 222-246`, `cmd/agentbus/selfrepairchore.go:32`, `config/keys.go:348-366` |

So a team of 8 required agents is back in about **60 s + 7 × 2 min ≈ 15 minutes** after a crash,
with its conversations (a client whose resume is not verified, or an agent without a proved
session, is reported to master instead, never started fresh). A second crash within 30 minutes
leaves every agent waiting on its per-agent budget; a third in a day is a crash loop, escalated.

**Five crashes in 300 s and systemd stops restarting it** (`failed`). Health `holder` then fails;
its remediation is sentinel's ("starts the current build's instance once per episode"), and
sentinel is dead. **Nothing starts the holder again**, and self-repair holds every agent
(`HoldHolder`, "the holder does not answer").

**Who is told.** The failure is placed "in hand" on busmon's health page (its `Person` list is
empty: `health/health.go:70-85`), so it is not in For you. The dispatcher's escalations go to
master as a cmd and a plain notice; they go **to the person** only when the escalation target
itself is stuck, held or blocked (`dispatch/loop.go:826-861`, `recipientCannotAct`), never merely
because it is dead. In practice the person learns of a dead holder when they look at busmon, or
when master's own unread messages escalate (then "the escalation recipient itself is stuck"). No
desktop notice is sent for it.

### 2.2 The holder restarted on purpose

`agentbus --project <p> service restart holder --kill-agents` is the person's only way, and kills
every agent like a crash (`service/holder.go:52-54`); recovery is §2.1's. `up` never restarts a
running holder; `setup.sh install` and `update` never touch one (`setup/services.go:105-110`).

### 2.3 The host reboots

- **No unit starts at boot or at login unless enabled, and the holder is never enabled.** Every
  unit is written without `[Install]` (`systemctl --user list-unit-files 'agentbus*'` reads
  `static` for all of them on this machine). `services.start_at_login` adds `[Install]` and enables
  **dispatch and missions only**: `service.Kinds` is `{Dispatch, Missions}`
  (`service/paths.go:22`, `service/login.go:49-90`). The holder's plist lives in
  `~/.local/share/agentbus/launchd`, not `~/Library/LaunchAgents` (`service/launchd.go:24`), so
  macOS does not load it at login either.
- **With `start_at_login` on**, after a reboot and a login the dispatcher runs and the holder does
  not: self-repair holds (`HoldHolder`), health `holder` fails, sentinel (to start it) is not
  running. Nothing comes back until the person runs `agentbus up <p> --resume`.
- **With it off** (the default), nothing runs until `agentbus up`.
- Linger (`loginctl show-user` reads `Linger=yes` on this machine) would start a user's enabled
  units at boot; with no unit enabled it starts none.
- The broker comes back only if the container engine starts at boot (compose's `restart:
  unless-stopped`; reboot-resume §6.2).

**Reboot-resume** (spec 2026-11-15, r5) is built to step 1 only (#548): the `lifecycle` package
(`run.json`, `unplanned.json`) and its writers in `up`, `down`, `suspend` and the dispatch
service's SIGTERM (`cmd/agentbus/runrecord.go`). Nothing calls `lifecycle.Judge` yet. Remaining:

- **A**: the judgement at dispatch start (`{p}:reboot`, verdicts, the repository check, the
  counter, the notice), `selfrepair.HoldReboot`, `agentbus resume` resetting the counter;
- **B**: busmon's popup and For-you item;
- **C**: `services.start_at_boot` and `agentbus service boot`.

**A gap in that spec:** its §3.1 condition 2b promises an unattended restart through "the holder
service, started at boot with the other services, §6.1", and §6.1 enables "the units as for
`start_at_login`", which never includes the holder (above). Built as written, A would always judge
`unplanned`, `why` `holder not answering`, and never restart a team. §3's change H1 closes it.

## 3. Starting at boot and after a reboot

**H1. The current build's holder is enabled with the project's other services.** When
`start_at_login` (and later `start_at_boot`) is on, `service login` and `up` give the current
build's holder unit its `[Install]` (`WantedBy=default.target`) and enable it; on launchd they write
its plist into `~/Library/LaunchAgents`, as for dispatch. Only the **current** build's unit: an older
build drains and is never started again (`service/holder.go:14-20`). The enable follows the build:
`up` on a new build enables the new key and disables the old one (a running old holder keeps
running; disable starts and stops nothing). `service gc` already removes a disabled old unit.

**After an upgrade not yet drained**, the enable has moved to the new build: a reboot then starts
only the new, empty holder. The old build's agents are not lost: they are dead, and self-repair (or
reboot-resume's restart) relaunches them into the new holder, resumed. Nobody should expect the old
holder back after a reboot; `service gc` removes it.

**H1b. Condition 2b waits for the holder (amends reboot-resume §3.1).** Self-repair already holds
while the holder does not answer, but reboot-resume's judgement is **one-shot**: it runs once before
the dispatcher's first pass, and its condition 2b ("the holder answers") would record `unplanned`,
`holder not answering`, whenever the dispatch service wins the race at boot or login. `HoldReboot`
would then keep the team stopped after the holder came up. Unit ordering does not fix it: `After=`
with `Type=simple` orders the starts, not the socket's readiness, and launchd has no order at all.
So, on every platform, **only where an automatic restart is otherwise possible**:

1. **Classification first, unchanged.** `lifecycle.Judge` classifies as reboot-resume §2.2 says,
   without waiting: `none`, `intentional` and `suspended` keep their verdict, whatever the holder
   does (an intentional or suspended project is never turned into `unplanned` by a slow holder).
2. **Then conditions 1 and 2, unchanged**, also without waiting: `setting off`, `self-repair off`,
   `self-repair in shadow mode` are recorded with their own `why`.
3. **Only at 2b, for an `unplanned` classification whose 1 and 2 held: a readiness phase.** The
   dispatch service, holding the lease, polls the current build's holder (health `holder`'s probe:
   its socket answers a `Describe`) every 2 s for at most **2 minutes** (`holderReadyWait`, a
   constant). Nothing is decided during it: `run.json` is not rewritten (a dispatcher that restarts
   meanwhile judges again from the start), `{p}:reboot` is not written, the counter is neither read
   nor incremented, and self-repair holds every agent (hold reason `reboot pending`, this boot).
4. **The holder answers within the wait:** the judgement goes on from condition 3 (the repository
   check) as reboot-resume §3.1 specifies.
5. **It does not:** the verdict is `unplanned`, `why` `holder not answering` (the closed set
   unchanged), final for this boot: no relaunch, nothing counted. §3.3's notice says it, naming the
   wait (`… not restarted: its holder did not answer within 2 minutes of the dispatcher's start`),
   so the person knows a slow start (a disk check at boot) cost the automatic restart; busmon's
   popup offers the relaunch, as for any other `unplanned`. A holder answering after the wait does
   not reopen the verdict: the person relaunches.

Tests (with reboot-resume A, deterministic, fake clock and fake holder probe):

- eligible `unplanned`, the dispatcher first, the holder answering after 30 s → `restarting` exactly
  once, the counter incremented once, nothing relaunched before the verdict;
- eligible `unplanned`, the holder never answering → `unplanned`, `holder not answering` after 2
  minutes, no relaunch, the counter untouched, the notice naming the wait;
- a dispatcher restart during the phase → judged once, counted once;
- `intentional`, and `suspended`, with no holder answering → their own verdict at once, no wait, no
  `holder not answering`, the popup's relaunch or resume as reboot-resume §4;
- `unplanned` with `services.unplanned_restart` off, and with self-repair off → `setting off` or
  `self-repair off` at once, no wait;
- the same boot (`none`) → no phase, no wait.

**H2. Reboot-resume's remaining steps, in this order:** A (with H1 in place, so condition 2b can
hold), then C (boot), then B (the popup). Until A, a reboot after H1 brings the holder and the
dispatcher back at login and **self-repair relaunches the whole team, intentional reboot or not**:
reboot-resume §5 says so and gates it with `HoldReboot`. So H1 ships **with or after** A, never
before (§8, Q2).

## 4. An alarm when the holder stops answering

**H3. A holder that does not answer is the person's**, not sentinel's: sentinel is one of the
agents it held. Health `holder`:

- `Person: [Fail]`, `Command: agentbus --project <p> service start holder`: in For you;
- a desktop notice once per episode, naming the project and the command, sent through
  `FencedNotifyOnceForPerson` with the mark `holder-down:<episode>` (the episode being the health
  check's first failing reading, Redis TIME): a dispatcher restart in the same episode does not
  notify twice (as `prompt-notify`'s once-per-episode marks);
- the warn `holder_restarted` stays in hand (self-repair is bringing the agents back), but its text
  says how many are waiting and the time the queue takes (§2.1's pace).

**H4. The dispatcher may start the current build's holder unit**, once per episode, when health
`holder` fails and the unit is not `active` (never a restart, never another build's, never
`up`): the act sentinel's remediation describes, done by the one process still running. A new
automated act on a service, so the person's (§8, Q3). Without H4, H3 tells the person, who runs
the command.

**H5. A hung holder** (alive, its socket not answering) is not restarted by systemd. Options:
`WatchdogSec=` with the holder notifying systemd from its accept loop (systemd then kills and
restarts it, killing its agents), or health `holder` alone telling the person (H3). §8, Q4.

**Out of scope:** a dead dispatch service. Its unit restarts it within its budget (`services`
health, `service/budget.go`); when the budget is spent, busmon's header shows dispatch down. A
second watcher of the watcher is not proposed.

## 5. An endurance test

**What a disposable test can prove** (`go test`, in-process, the e2e release test's fixtures:
`holder.NewServer`, the fake Claude client, `dispatch.Run` with self-repair on a fast clock):

1. a team of two fake agents, each woken once (working, then idle, drained);
2. the holder server closed and its children killed (a crash), then a new server on the same
   socket (the unit's restart);
3. self-repair relaunches both, **resumed on their recorded sessions** (the fake records its
   `--resume` argument), master first, one per pass;
4. each is woken again and drains;
5. health `holder` reads `holder_restarted` naming both, then OK;
6. with H3: a holder that never comes back fails `holder`, placed in For you.

Proposed as `TestHolderRestartEndToEnd`, beside `TestReleaseEndToEnd`, under the same release gate.

**What it cannot prove:**

- systemd's or launchd's own behaviour: `Restart=`, the start limit, `KillMode`, `[Install]` and
  linger at boot, launchd's `KeepAlive` (the unit text is tested; the manager is faked);
- a real client's resume (Claude Code's `--resume` of a real session; the fake only records the
  argument);
- a real reboot, and the broker container at boot;
- slow leaks over hours (descriptors, memory, the holder's per-terminal buffers).

Those need a **live check** on the person's machine (a scripted ask: kill the holder with
`systemctl --user kill -s KILL <unit>`, watch the team come back, time it) or a **soak run** in a
disposable VM or a systemd container (podman `--systemd=always`, linger, a test project with fake
clients, the holder killed every N minutes for hours, wakes counted). §8, Q5.

## 6. Upgrading: can it be one command?

Today, per project:

1. `./setup.sh update`: `git pull --ff-only`, build, install, then offer to restart the dispatch
   and missions services still on the older build; it never touches a holder
   (`setup/update.go`, `setup/services.go:147-192`);
2. `agentbus up <p>` (or `agentbus --project <p> service start holder`): installs and starts the
   new build's holder beside the old one, which keeps its agents;
3. `agentbus holder drain <p>`: moves each idle agent to the new holder, resumed, one at a time;
   skips an agent working, blocked, with a person active, a draft or background work (run it
   again later), and gc's the old build once it is empty (`holderdrain/drain.go`). The person's
   command: an agent's shell is refused.

**H6. `agentbus update`** (or `setup.sh update` running it at its end): 1, then for every project
on this host with services, 2 and 3; it prints, per project, the agents moved and the agents
skipped with why, and ends with the one line to run again for the skipped ones. It stays the
person's command (no agent runs it, as for drain) and moves only idle agents.

**H7, optional: the dispatcher finishes the drain.** An agent skipped because it was busy is moved
by the dispatcher the next time it is idle, with drain's own checks. A new automated act (it closes
a terminal and starts one, as self-repair and a done pop's end already do), so the person's (§8,
Q6). Without H7, the skipped agents wait for the next `update` or `holder drain`, and health
`holder-draining` keeps telling master.

The dev branch flow's step 5 (setup builds the newest tag on `main` in `<repo>/.release`) changes
what step 1 builds, not this sequence: H6 composes with it.

## 7. Gap with herdr-core

The person asked whether our holder works like herdr-core (`hc`, `/data/AI-Docs/herdr-core`), whose
holder survives what ours does not.

**herdr-core.** Its holder (`crates/herdr-core/src/holder.rs`, 465 lines) "owns every PTY, is every
program's parent, always reads output. Spawn, pump, resize, signal, reap. **No parsing, no
policy.**" It keeps a **1 MiB tail per terminal** (`TAIL_BYTES`) so that a separate process, the
core, which parses screens and serves the API, can crash or be restarted and rebuild every screen
from the tails; the holder restarts a crashed core itself. So `hc update` and `hc server restart`
**keep running terminals** ("Updates restart the core while holders keep the terminals running",
`docs/security.md`), and kept terminals survive a core loss.

**Ours.** One process does both: the holder owns the PTYs **and** runs the screen emulator
(`screen`, on `charmbracelet/x/vt`) in each terminal's read loop, under that terminal's lock
(`holder/term.go:58-77`: `t.scr.Write(buf)` for every read, then the 1 MiB `tail`, then the
viewers). It also serves the API, the viewers (attach), the submit protocol, the holds and the
emulator's answers to the program's queries (DSR, DA, the cursor position: `answer`). There is no
`recover()` anywhere in `holder/` or `screen/`.

What that means for agents today:

- **A parser panic kills every agent of the project.** A panic in `scr.Write` happens in a
  terminal's read goroutine; Go ends the process on an unrecovered panic in any goroutine, so the
  holder exits, the unit restarts it empty (§2.1), and every agent dies with it, not only the one
  whose output triggered it. The output that caused it is not kept: the tail dies with the process.
- **A parser hang freezes that agent and blinds the dispatcher, while the alarm stays green.** A
  `Write` that never returns keeps that terminal's lock: its output is no longer read, so the
  program blocks once the PTY's buffer is full (the agent freezes mid-turn). Every call that takes
  the lock waits: `Screen`, `Activity`, attach, and **`List`**, which takes every terminal's lock in
  turn (`terminal.info`, `holder/term.go:144`), so the dispatcher's look at *any* agent of the
  project, and self-repair's, hang on their deadlines. `Describe` takes only the server's lock
  (`holder/server.go:330-334`), so health `holder`'s probe still answers: it reads **OK**. Nothing
  restarts the process (systemd sees it alive; §4's H5 watchdog would see it alive too, if it beat
  from the accept loop).
- **An upgrade cannot keep the terminals.** A new build is a new holder; agents move to it only by
  being relaunched (`holder drain`), which `hc` avoids by restarting only the core.

**Three ways forward, with their cost** (§8, Q8):

| Option | What it changes | Cost (coder, with reviews) |
|---|---|---|
| **G1. Contain the parser** | a `recover()` around each terminal's `scr.Write` and `Snapshot`: a panic resets that terminal's emulator (rebuilt from its 1 MiB tail, or blank), records the bytes that triggered it in the holder's log, warns; the program and every other agent keep running. A hang is bounded by moving `Write` out of the lock path: the read loop appends to the tail and hands the bytes to a per-terminal emulator goroutine through a bounded queue; a queue full for N seconds marks that screen `stuck` (the dispatcher reads it unknown, never ready) and the program keeps being read. `List`/`info` stop taking the emulator's lock. Health `holder` probes `List` too, so a stuck holder reads fail | **1-2 PRs, ~400 lines with tests; 1-2 days.** Kills neither failure mode at the root: a stuck goroutine stays stuck until the holder restarts |
| **G2. Split like herdr-core** | a minimal PTY owner (spawn, pump, resize, signal, reap, the tail, the query answers it must keep giving while no core runs) as the service unit; the current holder becomes a restartable core, its child, connected over a socket, rebuilding screens from the tails at start. A core crash, hang (killed by the owner) or upgrade keeps every program running; `holder drain` is needed only when the owner itself changes | **5-7 PRs, ~2,500-3,500 lines with tests; 2-3 weeks**, plus a live check per platform. New risks: a second protocol and process, the query answers (DSR/DA) during a core restart, the owner's own upgrade still needs a drain |
| **G3. Keep as is** | the alarm and the endurance test of §4-§5 only | none beyond §4-§5 |

G1 then G2 is possible: G1's per-terminal emulator goroutine and tail are what G2's core would
rebuild from, so G1 is not wasted work.

## 8. Questions for the person

1. **Enable the holder with the others (H1)?** When "start at login" is on, the holder starts at
   login too, so the team's terminals are there when the dispatcher is. Without it, "start at
   login" brings back a dispatcher with no agents.
2. **H1 only with reboot-resume A?** With H1 alone, every reboot (intentional too) brings the team
   back by itself through self-repair; A makes an intentional reboot ask first (your 2026-11-15
   decision). Ship them together (recommended), or H1 first and accept the automatic restart
   meanwhile?
3. **May the dispatcher start a stopped holder (H4)?** Once per episode, never a restart, never
   while agents could be killed. Or only tell you (H3), and you run the command?
4. **A hung holder (H5):** let systemd's watchdog kill and restart it (its agents die and come back
   through self-repair), or only alarm you?
5. **The endurance test:** the in-process test (§5) under the release gate is proposed. Do you also
   want a scripted live check on your machine before each release, or a soak run in a VM or
   container (longer to build)?
6. **One command to update (H6)?** And should the dispatcher move a busy agent later by itself
   (H7), or does a skipped agent wait for your next `update`?
7. **The recovery pace after a crash:** 2 minutes between relaunches (`ProjectPause`) gives about
   15 minutes for 8 agents. Keep it (it protects quotas and the machine), or shorten it after a
   holder crash only, when every agent died at once? The cost of shorter: eight resumed sessions
   start within a minute or two, mostly on one provider's quota, each reloading its whole
   conversation.
8. **The parser inside the holder (§7).** Today a bug in the screen emulator kills every agent (a
   panic) or freezes one agent and blinds the dispatcher while the holder alarm stays green (a
   hang). Contain it inside the holder first (G1, 1-2 days), split the PTY owner from the parser as
   herdr-core does (G2, 2-3 weeks, and updates then keep the agents running), G1 then G2, or keep it
   as is (G3)?

## 9. Build order (each its own PR, into dev)

1. H3 (health `holder` to the person, the notice), the endurance test of §5, and G1 if Q8 says so: they change no
   behaviour of the services, and the test pins today's recovery first.
2. Reboot-resume A, with H1 and H1b (§8 Q1, Q2).
3. H4 and H5 as the person answers Q3 and Q4.
4. H6 (and H7 if Q6 says so).
5. Reboot-resume C, then B.

Each step adds its operator-note fragment; step 2 also amends the herdr-exit decision note
(the holder is enabled with the other services) and reboot-resume §6.1 (the holder in the units it
enables).
