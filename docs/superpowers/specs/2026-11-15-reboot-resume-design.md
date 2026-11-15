# After a reboot: ask after an intentional one, restart after an unplanned one (reboot-resume)

Status: spec, r1. Task `reboot-resume-spec` (master's directive of 2026-11-15, from the person's
answer 1794777093334-0 to the ask `autoresume10b`). Low priority; it does not block herdr-10b.

## 1. Why, and what the person asked

The suspend spec (2026-11-03, §8) brought a suspended project back after a reboot through herdr's
workspace hook (`upHook`), under `suspend.auto_resume`. With herdr gone (herdr exit, step 10)
there is no workspace event, so nothing reacts to a reboot. Agents live in the holder, which does
not survive a reboot; `{p}:resume` (herdr exit §4.7) keeps each agent's session, so `agentbus up
<p> --resume` brings them back with their conversations.

The person's answer, in substance:

- a reboot can be **intentional** (the person restarted the machine) or **unplanned** (crash,
  power cut);
- intentional: at the next start of busmon, a **popup offers to relaunch** the project;
- unplanned: agentbus **restarts the project by itself**, under a setting;
- after **3 unplanned restarts** the project is **stopped**, to protect its integrity.

Not a reboot, and not covered here: a network cut (the holder keeps the agents; busmon
reconnects), a crash of one service (its manager restarts it; self-repair handles dead agents),
a logout without reboot (unchanged: the services stop and start again at login as today).

## 2. Telling the two apart

**A clean shutdown sends a signal; a crash or a power cut sends none.** On a clean shutdown or
reboot, systemd (user units), launchd (LaunchAgents) and the init systems of Linux without systemd
(`services.manager = none`, whose supervisor forwards it) send SIGTERM to our services before
stopping. A kernel panic, a power cut or a hard reset end them without it. The dispatch service
records which one happened, in a **local file**, and the next boot reads it.

### 2.1 The run record

`<statedir>/agentbus/<p>/run.json`, mode 0600, written by write-to-temp, fsync, rename:

```json
{"boot": "<boot id>", "state": "running", "cause": "", "at": "2026-11-15T21:40:00Z"}
```

| Writer | When | Writes |
|---|---|---|
| dispatch service | at start, once the judgement below is recorded (§3) | `running`, this boot |
| dispatch service | on SIGTERM, before it exits, only if the file says `running` for this boot | `stopped`, cause `signal` |
| `agentbus up <p>` | before it starts the services | `running`, this boot (the person started it: nothing to judge) |
| `agentbus down <p>` | before it stops the services | `stopped`, cause `down` |
| `agentbus suspend` | with its pause record, before it stops the services | `stopped`, cause `suspend` |

Why a local file and not Redis: at shutdown the broker (a container) stops at the same time as
our services, so a write to it can be refused or lost; a local fsync'd file is the shortest
reliable path. The boot id is `dispatch.BootID()` (Linux `/proc/sys/kernel/random/boot_id`,
macOS `kern.bootsessionuuid`). `at` is wall-clock and only displayed; nothing is judged from it.

### 2.2 The judgement

A pure function, `lifecycle.Judge(prev, boot)`, used by the dispatch service and by busmon:

| `run.json` | Verdict |
|---|---|
| missing, unreadable, or either boot id unknown | `none` (nothing automatic, nothing asked) |
| same boot | `none` (not a reboot) |
| other boot, `stopped`, cause `down` | `none` (the person stopped it) |
| other boot, `stopped`, cause `suspend` | `suspended` (the suspend's own path, §4) |
| other boot, `stopped`, cause `signal` | `intentional` |
| other boot, `running` | `unplanned` |

Residual, stated: a dispatch service already dead when the machine shuts down cleanly (its
restart budget spent) leaves `running`, and the next boot reads `unplanned`: at worst one
automatic restart, counted (§3.2). A SIGTERM from `systemctl --user stop` by hand, then a reboot,
reads `intentional`: correct, the person stopped it.

## 3. The dispatch service at start

Once it holds the lease, before its first pass, the dispatch service judges `run.json` once.
Only then does it write `running` for this boot, so a crash in between judges again; every Redis
write below is fenced and keyed by the boot, so a second judgement of the same boot changes
nothing.

It records the verdict in `{p}:reboot` (a JSON string, fenced): `{boot, verdict, at}` (`at`
Redis TIME), for `intentional`, `suspended`, `unplanned` and `stopped` (§3.2). `none` writes
nothing. The record is deleted (fenced) by the dispatcher once every required agent of the
template is alive, the project has been relaunched, whoever did it.

### 3.1 Unplanned: restart by itself

When `services.unplanned_restart` is on and the counter allows it (§3.2) and the broker persists
(the `broker-persistence` check's reading: AOF or RDB on; unknown counts as no, since without
persistence the counter is lost with the reboot and the limit means nothing), the dispatcher
**relaunches the agents through self-repair**, the existing path: a new terminal in the holder per
dead agent, the role's template, `resume_session` from `{p}:resume`, master first. No new pane
writer, no `agentbus up` run by a service. Deaths of an older boot are **reboot deaths**: they do
not count in self-repair's per-agent budgets (R5); the reboot counter counts them instead.
The escalation target is told (`restarted after an unplanned stop of this machine (2 of 3 in
24 h)`), and the person gets one notice (`FencedNotifyOnceForPerson`, mark `reboot:<boot>`).

Otherwise (setting off, persistence unknown or off) the verdict is kept as `unplanned` and treated
as intentional: busmon asks (§4), and self-repair holds (§5).

### 3.2 The counter, and "stopped"

`{p}:unplanned`, a sorted set: member the boot id, score Redis TIME (ms) of the judgement,
`ZADD NX` (one boot counts once). Before relaunching, the dispatcher counts the members within
`services.unplanned_window` (24 h). With this boot counted:

- count ≤ `services.unplanned_max` (3): relaunch (§3.1);
- count > 3: **the project is stopped**.

**Stopped** means: the dispatcher writes a suspend record (`{p}:pause`, kind `suspend`, by
`dispatch`, reason `stopped after 4 unplanned restarts of this machine in 24 h`, fenced). The
existing suspend rules then apply: nobody is woken, self-repair relaunches nothing, busmon shows
`suspended since …`. Unlike the person's suspend, the services keep running (they are not stopped
by the dispatcher), so health, busmon and notifications still work. Verdict in `{p}:reboot`:
`stopped`. The escalation target is told, the person gets one notice and busmon's popup (§4) says
why, with `git status` and `git fsck` as the first checks.

**Only the person lifts it**: `agentbus resume` (the existing command, from busmon's popup or
palette) ends the suspend, relaunches with `--resume`, and **clears `{p}:unplanned`**: the
person's resume resets the counter, as the person alone resets a run budget. Older members also
age out of the window by themselves.

## 4. busmon: the popup

At start, for each project of the list on this host:

- `{p}:reboot` with `intentional` or `unplanned` (not restarted): **Relaunch `<p>`?** `This machine
  restarted at 21:40 (<intentional: "a clean restart" | unplanned: "an unexpected stop">); the agents
  of <p> are not running.` Enter runs `agentbus up <p> --resume` (as `busmon --pick` runs `up`);
  Esc closes it until busmon's next start. For you keeps an item `relaunch <p>` while the record
  stands.
- `suspended`: **Resume `<p>`?** `<p> was suspended before the restart.` Enter runs `agentbus
  resume -p <p> --yes`.
- `stopped`: **`<p>` is stopped.** `4 unplanned restarts in 24 h. Check the repository first (git
  status, git fsck), then resume.` Enter runs `agentbus resume -p <p> --yes`.
- **The services did not start** (no dispatcher: `services.start_at_login` off, or `none`
  manager): no record exists, so busmon judges `run.json` itself, read-only, with the same
  function, and offers the same popup for `intentional`, `unplanned` and `suspended`. busmon never
  counts, never writes `{p}:reboot`, never relaunches by itself.

A busmon on another host (a laptop reading a remote bus) shows the text and the command to run
there; it runs nothing.

## 5. Self-repair after a reboot

After the herdr cutover, agents come back "through `up` or self-repair with their recorded
resume" (herdr exit §4.4a), and the holder service starts with the others at login. After a
reboot the holder answers empty and every agent is dead, so self-repair would relaunch the team
after **any** reboot, intentional or not. This spec gates it: a new hold kind `reboot`
(`selfrepair.HoldReboot`) while `{p}:reboot` stands for this boot with a verdict other than an
allowed unplanned restart. The hold ends when the record is deleted (every required agent alive,
§3). A death of the current boot after that is ordinary self-repair.

## 6. Settings (`config/keys.go`, group Services, machine-only like `services.start_at_login`)

| Name | Env | Kind | Default | Rule |
|---|---|---|---|---|
| `services.unplanned_restart` | `AGENT_BUS_UNPLANNED_RESTART` | Bool | `true` | effective only where services start at login |
| `services.unplanned_max` | `AGENT_BUS_UNPLANNED_MAX` | Int | 3 | 1 to 10 |
| `services.unplanned_window` | `AGENT_BUS_UNPLANNED_WINDOW` | Duration | 24h | 1h to 7 days |

`suspend.auto_resume` is **retired**: its reader (the workspace hook) goes with herdr. A suspended
project gets the popup (§4) instead. The key leaves the registry: a value left in `config.toml`
then draws the file reader's existing `unknown key` warning, and the environment variable is no
longer read; the operator note says so.

## 7. Tests and footprint

Tests:
- `lifecycle.Judge`: every row of §2.2; unreadable and unknown boot → `none`.
- run record: the SIGTERM handler writes `stopped` only over `running` of this boot (never over
  `down` or `suspend`); `up`, `down`, `suspend` write theirs before acting (order checked with fakes);
  the file is 0600 and replaced atomically.
- dispatch start: `unplanned` with persistence on → self-repair relaunches, reboot deaths not
  counted in R5; the fourth in the window → suspend record by `dispatch`, services left running,
  nothing relaunched, notices once; persistence unknown → no relaunch, hold; `intentional` → hold,
  record written; a second start on the same boot changes nothing; lost lease writes nothing;
  the record deleted once every required agent is alive.
- `agentbus resume` clears `{p}:unplanned`.
- busmon: each popup's text and command; no record + services down → local judgement; remote
  host → command shown, nothing run.

Footprint (two PRs):
- A: `lifecycle/` (run record, `Judge`), SIGTERM handling in the dispatch service, `up`/`down`/
  `suspend` writes, `bus/reboot.go` (`{p}:reboot`, `{p}:unplanned`, fenced scripts, the
  dispatcher's suspend), dispatch start, `selfrepair/decide.go` (`HoldReboot`) and the chore's
  reboot deaths, `agentbus resume` (counter reset), `config/keys.go` (three keys, one retired),
  `bus/keykinds.go` (two keys), operator note fragment.
- B: busmon popup and For-you item.

## 8. Questions for the person

1. **"After 3"**: proposed, three automatic restarts in 24 h are allowed and a fourth unplanned
   stop in that window stops the project. Or should the third already stop it?
2. **Default of `services.unplanned_restart`**: proposed on. It acts only where the services start
   at login (`services.start_at_login`, off by default): with that off, an unplanned reboot gets
   the popup like an intentional one. Should turning on unplanned restart also turn on start at
   login?
3. **Stopped**: proposed, a suspend by the dispatcher with the services left running so you are
   told; only your `agentbus resume` lifts it and resets the counter. Or should it stop the
   services as well?
4. **`suspend.auto_resume` retired**, a suspended project gets the popup after a reboot. Or should a
   suspended project restart by itself like an unplanned stop?
5. **A network cut is not a reboot** here (agents keep running in the holder). Did you mean the
   machine itself losing power or network, or something else (an SSH session to a remote host)?
