# After a reboot: ask after an intentional one, restart after an unplanned one (reboot-resume)

Status: spec, r5 (r4 reviewer-codex: an unattended relaunch needs the holder, not herdr, §3.1
condition 2b; the count is absent for a stop by the repository check, §3). r4: Task `reboot-resume-spec` (master's directive of 2026-11-15, from the person's
answer 1794777093334-0 to the ask `autoresume10b`), then `reboot-resume-r4` (the person's review of
r2, human directive 1794778417884-0). Low priority; it does not block herdr-10b.

Revisions: r2 foureyes (the verdicts name whether a restart happens; self-repair must be `on`; the
dispatcher's suspend fields). r3 reviewer-codex (the counter leaves the broker for a local fsync'd
file, failing closed). r4 reviewer-codex (the counter file's bootstrap and loss, §3.2; the complete
`{p}:reboot` schema, §3) and the person's review: services started at boot (§6.1), a repository
integrity check before a restart (§3.1), the broker at boot (§6.2), platforms (§2.3), suspend and
hibernate (§2.3), one notice per reboot (§3.3), and the person's decisions (§8).

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

Not a reboot, and not covered here: a network loss (a Wi-Fi cut that disconnects the LLM
clients; the person's point 5: a separate spec, network-resume), a sleep or hibernation (§2.3),
a crash of one service (its manager restarts it; self-repair handles dead agents),
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

### 2.3 Platforms, sleep and hibernation

- **Where the boot id is unknown, nothing happens.** `BootID()` reads Linux and macOS only. On the
  BSDs (and Windows, out of scope since 2026-11-11) it is unknown, so the verdict is always `none`:
  nothing breaks, nothing restarts, nothing is asked. The operator note says so.
- **Sleep and hibernation are not a reboot.** The boot id survives them (Linux keeps
  `boot_id` in the kernel's memory, which hibernation restores; macOS keeps `kern.bootsessionuuid`
  across sleep), so the run record reads the same boot: `none`. Self-repair's own rule for a
  resume from sleep (a monotonic jump, same boot id) is unchanged.

Residual, stated: a dispatch service already dead when the machine shuts down cleanly (its
restart budget spent) leaves `running`, and the next boot reads `unplanned`: at worst one
automatic restart, counted (§3.2). A SIGTERM from `systemctl --user stop` by hand, then a reboot,
reads `intentional`: correct, the person stopped it.

## 3. The dispatch service at start

Once it holds the lease, before its first pass, the dispatch service judges `run.json` once.
Only then does it write `running` for this boot, so a crash in between judges again; every Redis
write below is fenced and keyed by the boot, so a second judgement of the same boot changes
nothing.

It records the verdict in `{p}:reboot` (a JSON string, fenced). `none` writes nothing. The
complete schema:

| Field | Type | Present |
|---|---|---|
| `boot` | string, the boot id judged | always |
| `verdict` | one of the table below | always |
| `why` | string, one of the closed reasons of §3.1 | `unplanned` and `stopped` only |
| `n` | int, restarts counted in the window, this one included | `restarting`, and `stopped` by the limit (`why` `limit: …`) only |
| `max` | int, `services.unplanned_max` when judged | as `n` |
| `window` | int, `services.unplanned_window` in ms when judged | as `n` |
| `at` | int, Redis TIME (ms) of the judgement | always |

busmon renders `n of max in <window>` (`2 of 3 in 24 h`) from these three fields, and nothing else;
a record missing one of them where it must be present is shown as `count unknown`. A `stopped` by
the repository check (§3.1, condition 3) is judged before the counter is read: it carries no `n`,
`max` or `window`, and busmon shows its `why` only.

The verdicts, a closed set, each with what busmon and self-repair do:

| `verdict` | Meaning | busmon (§4) | self-repair (§5) |
|---|---|---|---|
| `intentional` | clean shutdown | popup: relaunch | holds |
| `suspended` | suspended before the shutdown | popup: resume | holds (the suspend already holds it) |
| `restarting` | unplanned, restarted by itself (§3.1) | no popup; a header line `restarting after an unexpected stop (2 of 3 in 24 h)` | relaunches |
| `unplanned` | unplanned, **not** restarted; `why` names the failing condition (§3.1: 1, 2 or 4) | popup: relaunch, with `why` | holds |
| `stopped` | the repository check failed, or the limit was passed (§3.1: 3 or 5); `why` says which | popup: stopped | holds (the suspend holds it) |

 The record is deleted (fenced) by the dispatcher once every required agent of the
template is alive, the project has been relaunched, whoever did it.

### 3.1 Unplanned: restart by itself

When every condition holds, in this order (the first that fails is `why`):

1. `services.unplanned_restart` is on (`why`: `setting off`);
2. self-repair's mode is `on` (`self-repair off`, `self-repair in shadow mode`: neither can
   restart a team, so the verdict says so rather than claiming a restart);
   2b. **self-repair can relaunch without a person's session**: its relaunch path is the holder's
   `Start` (herdr exit, step 10: a new terminal in the project's holder service, started at boot
   with the other services, §6.1), and the holder answers. Before that cutover, self-repair creates
   panes through herdr and refuses without a herdr session (`relaunch.ErrNoSession`), which no boot
   service, linger, cron line or LaunchDaemon provides: `why` `relaunch needs an interactive
   session`; and with a holder that does not answer, `why` `holder not answering`. So an
   unattended restart is promised only after the herdr-10 cutover; before it, an unplanned reboot
   gets the popup, as an intentional one;
3. **the repository is intact** (the person's point 2), a read-only check of the project's
   repository: no `index.lock` in its git directory or in any of its worktrees' (`git worktree
   list --porcelain`, then a stat of each `index.lock`), and `git fsck --no-dangling --no-progress`
   exits 0. It runs as `git -c core.fsmonitor=false -c core.hooksPath=/dev/null …` in the
   project's directory, with a 5-minute limit, so no repository-configured program runs (the
   dispatcher never runs repository code). On failure the verdict is **`stopped`** (§3.2's
   stop), `why` `index.lock present: <path>`, `git fsck failed (exit <n>)` or `git fsck timed
   out`: agents never restart on a damaged repository, and nothing is counted;
4. this restart is counted, durably, before anything is relaunched (§3.2; `counter not written`
   or `counter missing`: no restart, fail closed);
5. the counter allows it (§3.2; when it does not, the verdict is `stopped`, `why` `limit: <n> in
   <window>`),

the verdict is `restarting` and the dispatcher
**relaunches the agents through self-repair**, the existing path: a new terminal in the holder per
dead agent, the role's template, `resume_session` from `{p}:resume`, master first. No new pane
writer, no `agentbus up` run by a service. Deaths of an older boot are **reboot deaths**: they do
not count in self-repair's per-agent budgets (R5); the reboot counter counts them instead.
The escalation target is told (`restarted after an unplanned stop of this machine (2 of 3 in
24 h)`), and the person gets one notice (`FencedNotifyOnceForPerson`, mark `reboot:<boot>`).

When 1, 2, 2b or 4 fails, the verdict is `unplanned` with its `why`, treated as intentional: busmon
asks (§4), self-repair holds (§5), and nothing is counted (only a restart counts). When 3 or 5
fails, the verdict is `stopped`.

The closed set of `why`: `setting off`, `self-repair off`, `self-repair in shadow mode`,
`relaunch needs an interactive session`, `holder not answering`,
`index.lock present: <path>`, `git fsck failed (exit <n>)`, `git fsck timed out`, `counter not
written`, `counter missing`, `limit: <n> in <window>`.

### 3.2 The counter, and "stopped"

**Not in the broker.** A power cut is the very event counted, and the broker cannot promise the
count survives one: AOF with `appendfsync everysec` loses up to a second, `no` leaves it to the
OS, RDB keeps a periodic snapshot. A crash soon after each restart could then erase every count
and pass the limit forever. The counter lives beside the run record, in a local file the
dispatcher fsyncs **before** relaunching anything:

`<statedir>/agentbus/<p>/unplanned.json`, mode 0600, write-to-temp, fsync the file, rename, fsync
the directory: `[{"boot": "<boot id>", "at": <ms>}, …]`, one entry per restart (a boot already
listed is not added again, so a second judgement of the same boot counts nothing). `at` is
Redis TIME read at the judgement (the persisted-time rule); the window is judged against Redis
TIME too. Entries older than `services.unplanned_window` (24 h) are dropped at each write.

**Bootstrap and loss.** The file is created, as `[]`, by whoever creates `run.json` (the dispatch
service on its first start, or `agentbus up`), in the same step and before `run.json` is renamed
into place: so `run.json` present means the counter was created. Then:

- `run.json` missing (first start, or an install older than this): verdict `none` (§2.2), both
  files created; nothing to count.
- `run.json` present, `unplanned.json` **missing**: the counter was lost or deleted. Never read as
  empty (that would reset the budget): `why` `counter missing`, verdict `unplanned`, no restart,
  the person told. `agentbus up` or `agentbus resume`, the person's acts, recreate it as `[]`.
- unreadable or malformed: `counter not written`, as above. Never a reset.

Order: conditions 1 to 3 hold; read the file; add this boot; count within the window:

Conditions 1 to 3 are checked first, so the file counts only restarts the dispatcher would make.

- count ≤ `services.unplanned_max` (3): write the file (failure: `counter not written`), then
  relaunch (§3.1);
- count > 3: **the project is stopped** (the file is written too; its failure does not undo the
  stop).

`{p}:reboot` carries `n`, `max` and `window` for busmon's line (§3's schema); the file is the
count's only source.

**Stopped** means: the dispatcher writes a suspend record in `{p}:pause`, through a fenced
variant of `Suspend`'s script (`FencedSuspend`: the lease check, then the same body), with the
fields `agentbus resume` reads:

| Field | Value |
|---|---|
| `kind` | `suspend` |
| `by` | `dispatch` |
| `notify` | the escalation target (who gets the resume directive) |
| `reason` | `stopped after 4 unplanned restarts of this machine in 24 h`, or `stopped: the repository needs a check (<why>)` |
| `host`, `boot` | this host, this boot |
| `services` | `{dispatch: true, missions: <active now>, holder: true}`: the services running when it stopped, so `resume` starts any that has stopped since and stops none |
| `at`, `until` | set by the script from Redis TIME, as for the person's suspend |
| `prev` | a timed pause it replaced, as the script does |

An existing suspend is left as it is (the script's own rule): the person's suspend wins. The
existing suspend rules then apply: nobody is woken, self-repair relaunches nothing, busmon shows
`suspended since …`. Unlike the person's suspend, the services keep running (they are not stopped
by the dispatcher), so health, busmon and notifications still work. Verdict in `{p}:reboot`:
`stopped`. The escalation target is told, the person gets one notice and busmon's popup (§4) says
why, with `git status` and `git fsck` as the first checks.

### 3.3 One notice per reboot

For every verdict but `none`, the dispatcher sends the person one notice
(`FencedNotifyOnceForPerson`, mark `reboot:<boot>`), so a reboot is never silent when busmon is
not opened afterwards (the person's point 5): the popup's text (§4), or for `restarting` the line
`<p> restarted after an unexpected stop of this machine (n of max in 24 h)`. It needs a dispatch
service running after the reboot (§6.1); without one, only busmon's popup tells.

### 3.4 Lifting a stop

**Only the person lifts it**: `agentbus resume` (the existing command, from busmon's popup or
palette) ends the suspend, relaunches with `--resume`, and **empties `unplanned.json`** (it runs on the project's host): the
person's resume resets the counter, as the person alone resets a run budget. Older members also
age out of the window by themselves.

## 4. busmon: the popup

At start, for each project of the list on this host:

- `intentional` or `unplanned`: **Relaunch `<p>`?** `This machine restarted at 21:40 (<intentional:
  "a clean restart" | unplanned: "an unexpected stop, not restarted: <why>">); the agents of <p> are
  not running.` Enter runs `agentbus up <p> --resume` (as `busmon --pick` runs `up`);
  Esc closes it until busmon's next start. For you keeps an item `relaunch <p>` while the record
  stands.
- `restarting`: no popup; the header line of §3's table until the record goes.
- `suspended`: **Resume `<p>`?** `<p> was suspended before the restart.` Enter runs `agentbus
  resume -p <p> --yes`.
- `stopped`: **`<p>` is stopped.** `<why>. Check the repository first (git status, git fsck),
  then resume.` Enter runs `agentbus resume -p <p> --yes`.
- **The services did not start** (no dispatcher: neither `services.start_at_boot` nor
  `services.start_at_login` on): no record exists, so busmon judges `run.json` itself, read-only, with the same
  function, and offers the same popup for `intentional`, `unplanned` and `suspended`. busmon never
  counts, never writes `{p}:reboot`, never relaunches by itself.

A busmon on another host (a laptop reading a remote bus) shows the text and the command to run
there; it runs nothing.

## 5. Self-repair after a reboot

After the herdr cutover, agents come back "through `up` or self-repair with their recorded
resume" (herdr exit §4.4a), and the holder service starts with the others at boot or login. After a
reboot the holder answers empty and every agent is dead, so self-repair would relaunch the team
after **any** reboot, intentional or not. This spec gates it: a new hold kind `reboot`
(`selfrepair.HoldReboot`) while `{p}:reboot` stands for this boot with any verdict but
`restarting` (§3's table). The hold ends when the record is deleted (every required agent alive,
§3). A death of the current boot after that is ordinary self-repair.

## 6. Starting without the person

### 6.1 `services.start_at_boot` (the person's point 1)

With services started at login only, an unplanned reboot stops at the login screen: nothing runs
until the person logs in, and then the popup does the same job. For an unattended restart the
services must start at boot. A new setting, `services.start_at_boot` (Bool, default off,
machine-only), applied by `agentbus service boot` (enable or disable, as `agentbus service login`
applies `start_at_login`; `agentbus up` applies both), one backend per platform behind the same
interface as `LoginStart`:

| Platform | Mechanism | Needs root |
|---|---|---|
| Linux with systemd | `loginctl enable-linger <user>`, and the units enabled as for `start_at_login` (the user manager then starts at boot) | no, where polkit allows a user to linger itself; otherwise the command says so and prints the line to run |
| Linux without systemd (`services.manager = none`) | a system service of the init (OpenRC, runit…) running as the user; fallback, an `@reboot` line in the user's crontab running `agentbus --project <p> service start` (services only, never agents) | the system service yes; the crontab no |
| macOS | a LaunchDaemon in `/Library/LaunchDaemons` with `UserName` set to the person, instead of the LaunchAgent | yes |
| Windows, BSDs | out of scope (a boot id is unknown there, §2.3) | |

A step that needs root is never run by agentbus: `agentbus service boot` and busmon's C page print
what to run. Linger changes one thing the herdr exit decided (Q5, "without linger"): with
`start_at_boot` on, logging out no longer ends the team. It is opt-in and said on the C page.

**busmon's C page warns** when `services.unplanned_restart` is on and neither `start_at_boot` nor
`start_at_login` is: `unplanned restart cannot act: the services start neither at boot nor at
login`; with `start_at_login` only: `unplanned restart acts only once you log in`. No coupling
(the person's answer 2): turning one setting on never turns another on.

### 6.2 The broker at boot (the person's point 3)

The broker must be up before the dispatcher can judge. It is a requirement, stated, not managed:
the shipped compose's `restart: unless-stopped` brings it back only when the container engine
starts at boot (Docker's daemon enabled; rootless Podman needs a user unit or quadlet, and linger).
The C page says so next to `start_at_boot`. A dispatch service started before the broker judges
nothing: it judges only once it holds the lease (§3), so it retries its connection as today, leaves
`run.json` untouched, relaunches nothing, and judges once the broker answers.

### 6.3 Settings (`config/keys.go`, group Services, machine-only)

| Name | Env | Kind | Default | Rule |
|---|---|---|---|---|
| `services.start_at_boot` | — | Bool | `false` | machine-only, file only, as `start_at_login` |
| `services.unplanned_restart` | `AGENT_BUS_UNPLANNED_RESTART` | Bool | `true` | acts only where the services start at boot (or at login, after the login) |
| `services.unplanned_max` | `AGENT_BUS_UNPLANNED_MAX` | Int | 3 | 1 to 10 |
| `services.unplanned_window` | `AGENT_BUS_UNPLANNED_WINDOW` | Duration | 24h | 1h to 7 days |

`suspend.auto_resume` is **retired**: its reader (the workspace hook) goes with herdr. A suspended
project gets the popup (§4) instead. The key leaves the registry: a value left in `config.toml`
then draws the file reader's existing `unknown key` warning, and the environment variable is no
longer read; the operator note says so.

## 7. Tests and footprint

Tests:
- `lifecycle.Judge`: every row of §2.2; unreadable and unknown boot → `none`; a fake suspend and
  a fake hibernation (same boot id, a monotonic jump) → `none`.
- `{p}:reboot`: each verdict's fields exactly as §3's schema (`why` only with `unplanned` and
  `stopped`; `n`, `max`, `window` only with `restarting` and a `stopped` by the limit; a `stopped`
  by the repository check has none, and busmon renders its `why` alone).
- relaunch path: self-repair on herdr (before the cutover) → `unplanned`, `relaunch needs an
  interactive session`, nothing counted; on the holder with no holder answering → `holder not
  answering`; on the holder answering → `restarting`.
- counter bootstrap: first start creates both files, `unplanned.json` before `run.json`; `run.json`
  present and `unplanned.json` missing → `counter missing`, no restart, never a reset; `up` and
  `resume` recreate it.
- integrity: an `index.lock` in the main git directory, then in a worktree's → `stopped` with its
  path; `git fsck` failing → `stopped`, exit code in `why`; a timeout (fake clock) → `stopped`;
  `core.fsmonitor` and hooks configured in the repository never run (a fake that records argv and
  a repository whose fsmonitor would write a file); nothing counted.
- broker: the dispatch service started with no broker judges nothing and writes nothing, then
  judges once when the broker answers.
- notice: every verdict but `none` sends one notice per boot; `restarting`'s carries `n of max`.
- `service boot`: each backend's files or commands with fakes (no `loginctl`, `launchctl` or
  crontab is run for real); root steps printed, never run; the C page's two warnings.
- run record: the SIGTERM handler writes `stopped` only over `running` of this boot (never over
  `down` or `suspend`); `up`, `down`, `suspend` write theirs before acting (order checked with fakes);
  the file is 0600 and replaced atomically.
- dispatch start: an unplanned stop with every condition → `restarting`, self-repair relaunches,
  reboot deaths not counted in R5; each failing condition alone (setting off, self-repair `off`,
  `shadow`, counter not written) → `unplanned` with that `why`, no relaunch, hold, nothing
  counted; the fourth restart in the window → `stopped`, a suspend record with every field of
  §3.2's table, services left running, nothing relaunched, notices once; an existing suspend of
  the person's left as it is; `agentbus resume` on the dispatcher's suspend starts no stopped
  service it should not and lifts it; `intentional` → hold, record written; a second start on the same boot changes nothing; lost lease writes nothing;
  the record deleted once every required agent is alive.
- `agentbus resume` empties `unplanned.json`.
- counter: written and fsync'd before the relaunch (a fake relaunch records the order); a write
  failure → `counter not written`, no relaunch; an unreadable or malformed file → no restart,
  never a reset; the same boot judged twice counts once; entries outside the window dropped; a
  restart, kill, restart sequence across simulated boots stops at the fourth with the broker
  flushed between boots (the count does not depend on the broker).
- busmon: each verdict's popup or header line, text and command; no record + services down → local judgement; remote
  host → command shown, nothing run.

Footprint (two PRs):
- A: `lifecycle/` (run record, counter file, `Judge`), SIGTERM handling in the dispatch service, `up`/`down`/
  `suspend` writes, `bus/reboot.go` (`{p}:reboot`, fenced scripts, the
  dispatcher's suspend), dispatch start, `selfrepair/decide.go` (`HoldReboot`) and the chore's
  reboot deaths, `agentbus resume` (counter reset), `config/keys.go` (three keys, one retired),
  `bus/keykinds.go` (`{p}:reboot`), the integrity check, the notice, operator note fragment.
- B: busmon popup and For-you item.
- C: `services.start_at_boot`, `agentbus service boot` and its backends (`service/`), the C page's
  warnings and broker line.

## 8. The person's decisions (directive 1794778417884-0)

1. Three automatic restarts in 24 h; the fourth stops the project.
2. No coupling: turning on unplanned restart turns on neither start at login nor start at boot;
   the C page warns instead (§6.1).
3. Stopped: the services keep running so the person is told; only `agentbus resume` lifts it.
4. A suspended project gets the popup; it never restarts by itself.
5. The network loss meant a Wi-Fi cut that disconnects the LLM clients: not a reboot, out of
   scope here, for a separate spec (network-resume).

No question left open.
