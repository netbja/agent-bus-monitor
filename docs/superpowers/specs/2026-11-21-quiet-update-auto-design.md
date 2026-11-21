# Quiet update, step 2: the automatic mode, and pruning old trees

Status: r4, spec only. Base: origin/dev `25a34672`. Author: architect, from master's directive of
2026-11-21 (cmd `1795299412029-0`, idea i1). It builds on step 1 as built: `agentbus update`
(spec [2026-11-20](2026-11-20-agentbus-update-design.md), package `update/`,
`cmd/agentbus/updatecmd.go`). It replaces the automatic parts of the r13 quiet-update spec
([2026-11-14](2026-11-14-quiet-update-design.md): §4.4 the window, §4.7 the updater service, §4.1's
tree lifetime) with a smaller design, and keeps that spec's decisions 2, 3, 4 and 9.

## 1. What exists, what is missing

| Fact | Today (dev `25a34672`) |
|---|---|
| A manual update | Built: `agentbus [--project <p>] update`, with a plan, one yes, a snapshot, the swap, the restart of `<p>`'s `dispatch` and `missions`, and an automatic rollback on any failure (`update.Run`, `update/run.go:68`). |
| Snapshots | Built: under `<state>/agentbus/updates/<id>/`, the last 3 kept (`update.Keep`, `update.Prune`, `update/snapshot.go:263`), and `--rollback`, `--list`. |
| One update at a time | Built: a `flock` on `<updates>/lock` (`update.Lock`, `update/snapshot.go:278`). |
| A notice of a new release | Built: busmon's header `↑ vX.Y.Z`, once a day (`updates.check`, `cmd/busmon/update.go`). |
| An update nobody types | Missing. Nothing installs unattended. |
| Old trees | Missing. Each update adds `${XDG_DATA_HOME:-~/.local/share}/agentbus/src/<tag>/` (a worktree of the person's clone, 69 MB for v0.33.0), and nothing removes one (step 1 §3.2). |

## 2. From the person's side

- **Nothing changes until the person says so.** `updates.auto` defaults to `notify`: the
  busmon header, as today.
- **The person turns it on once,** in `~/.config/agentbus/config.toml`:
  ```toml
  [updates]
  auto = "install"
  window = "03:00-05:00"
  ```
  That setting is the person's yes, given ahead. Only the person can set it (§6).
- **Then, on a night with a release and a quiet team,** the update runs in the window as if the
  person had typed `agentbus update --yes`. In the morning master has one report:
  ```
  updater: agentbus v0.33.0 -> v0.34.0 installed at 03:05; restarted agent-bus-monitor dispatch,
  missions; demo dispatch. skills changed: agent-bus (agentbus roles skill-users agent-bus).
  Undo: agentbus update --rollback
  ```
- **A night it does not run** (a project in work, a failed check), nothing changes. It tries
  again at the next window. Master hears of it only when it matters: a failure, an interrupted
  update, or a release waiting longer than `updates.max_wait` (§4.5).
- **Old trees go away on their own** once nothing needs them (§5), after any successful update,
  manual or automatic.

## 3. What it never does

- **Restart a project in work.** It installs only when every project whose services run on this
  host is quiet (§4.3). It never stops an agent, never relaunches one, never restarts a holder.
- **Move `main`, or any branch.** It reads tags with `git fetch --tags origin` and never pushes,
  tags, checks out or resets a branch in the person's clone. A release is a tag on `origin/main`
  (`update.Find`, step 1 §3.1), and only master makes one, a fast-forward of `main` on the person's
  yes (decision 2026-11-16, dev branch flow). So an unattended install only ever installs a release
  the person has already approved.
- **Install outside the window,** or when `updates.auto` is not `install`.
- **Install a release that needs a migration** (`docs/releases/<tag>.migrate` in the tag, the
  r13 rule §4.6). It reports "vX needs you" instead. A manual update of such a release is
  `./setup.sh` with its notes, as in step 1 §4.
- **Retry a release whose automatic install failed.** It reports once. The person retries by
  hand with `agentbus update` (§4.5).
- **Remove a tree that something still needs,** a dirty tree, or anything outside
  `<src>/v*` (§5).
- **Write to `config.toml`, an env file or `notify.toml`,** or rebuild the broker's container. Its
  only broker writes are the reports of §4.5.
- **Run under an agent** (§6).

## 4. The automatic mode

### 4.1 The updater service

- **One per host,** for a managed build only (a `buildinfo.Repo` under `<src>/`; a developer
  build has none, as r13 §4.1). It is not the dispatcher: the dispatcher never runs repository
  code, and an update builds code (CLAUDE.md).
- **What runs:** `agentbus service run updater`. Its loop is a `time.Ticker` of 5 minutes and one
  test (§4.2). A pass is a child process, `<bindir>/agentbus update --auto`: the binary installed
  now, so a pass always runs the newest logic and the loop itself never needs an update.
- **Its unit,** the same in the three service modes (r13 §4.8): `agentbus-updater.service`
  (systemd, `KillMode=process`), `dev.agentbus.updater` (launchd, `AbandonProcessGroup`), or
  `agentbus service supervise updater` with `<rundir>/agentbus/updater.pid` (`none`). Stopping
  the loop never kills a pass in flight.
- **Who writes the unit:** `agentbus up` of any project, when the build is managed and the unit is
  missing, through the same `Manager.Install` call it makes for `dispatch` (`cmd/agentbus/up.go:518`),
  with a host spec (no project). `setup install` makes the same call. So a host updated by
  `agentbus update`, with no new `setup install`, gets the updater at its next `up`.
  - `Manager` names a unit by project and kind today. The host spec needs a unit name with no
    project. That is the one change to `service/`.
- **Its lifecycle:**
  - `agentbus down <p>` leaves it running: it belongs to no project.
  - `service list` and `service status` show it on its own line, with its last pass.
  - `service gc` never touches it (it runs from the bin dir, not from a build copy).
  - An update never restarts it.
  - `setup uninstall` stops it and removes its unit, its state file and the lock.
- **The updater health check** is amber when `updates.auto = install` and the updater is not
  running, or its last pass failed.

### 4.2 The loop's test, each tick

1. Read `updates.auto` and `updates.window` afresh, so a change needs no restart. With `notify`,
   stop here.
2. Read the local wall clock (`time.Now()`, `time.Local`) and the state file (§4.4).
3. **Inside the window, and no pass has finished in this occurrence of it:** run a pass.
4. **Outside the window, and the last check is more than 24 hours old:** run a check-only pass,
   `update --auto --check`. It fetches and finds the release, never stages or installs. It exists
   so that `max_wait` is counted on a host that is never awake in the window.

Window occurrences follow r13 §4.7: `HH:MM-HH:MM` local; a window that crosses midnight belongs to
the day it starts; bounds built with `time.Date` in `time.Local`; a last pass dated in the future
counts as no pass. A doubled pass on a daylight-saving night is harmless: the second finds the
release current.

### 4.3 One pass: `agentbus update --auto`

It is `update.Run` with these differences, in order:

1. **Guards:** the agent refusal (§6); the lock (a held lock defers the pass); an interrupted
   update (`update.Interrupted`) changes nothing and is reported once (§4.5).
2. **Find** (`update.Find`). Up to date: the pass is finished.
   - **The fetch never prompts:** `GIT_TERMINAL_PROMPT=0` and
     `GIT_SSH_COMMAND="ssh -o BatchMode=yes"`. A service has no terminal, and may have no
     `SSH_AUTH_SOCK`. A failed fetch is a failed pass, with git's message. (Checked on this host,
     2026-11-21: `git ls-remote origin` over SSH works with `SSH_AUTH_SOCK` unset.)
3. **Skip what it must not install:** a tag with a `.migrate` file, and a tag whose last
   automatic install has `ok=false` in the history. Each is reported once.
4. **The quiet test.** The projects judged are every project with any of these on this host:
   - a running `dispatch` or `missions` (`update.Deps.Running`, as step 1 reads them);
   - an active holder of any build: `HolderManager.HolderUnits("")` with `HolderActive`
     (`service/holder.go`), or under `services.manager = none` a holder supervisor whose pidfile
     names a live process. A project whose dispatch and missions are stopped or failed can still
     have agents at work in its holder (`down` itself says agents may still run when a holder
     cannot be stopped), so its agents are judged too (reviewer-codex on r2).
   **It fails closed:** when the services, a holder's state or a project's broker cannot be read,
   the pass is deferred, never run.
   A project is **in work** when, read on its broker with Redis `TIME`:
   - one of its agents in `{p}:agents` has state `working`; or
   - one of its agents' status changed less than 10 minutes ago; or
   - **its conductor is at work:** a mission of any change is `checking` (the conductor runs the
     change's tests) or `integrating` (it merges the change), the states of `mission/state.go:15`,
     `:18`. They are read without a token: `mission.Store.Changes`, then `Store.Missions` per
     change (`mission/store.go:100`, `:127`). The `{p}:agents` hash does not hold the conductor,
     so without this a mission at work at night would have its `missions` service restarted under
     it (foureyes on r1).
   A suspended project has no running services and no active holder, so it is not among them.
   **A stale `working`** (an agent that died without setting its state back) defers every night;
   the `max_wait` notice of §4.5 names that agent, and the person or master clears it.
   If any project is in work, the pass is **deferred**: it changes nothing, and the next tick in
   the same window tries again.
5. **Install:** step 1's stage, snapshot, swap, then restart **every** project's stale `dispatch`
   and `missions` from step 4's list, one at a time, each judged green as step 1 §3.4 step 6
   judges it. A red one rolls the whole update back, as in step 1. (Question 1: which projects
   an automatic install restarts.)
6. **Tell** (§4.5), then prune (§5).

The yes: `--auto` installs only when `updates.auto = install`, read from `config.toml` at the
start of the pass. Without it, `--auto` behaves as `--check`. `--auto` never asks on a terminal.

### 4.4 The state file

`<updates>/auto.json`, written by `files.WriteAtomic`, the host's own record in local wall-clock
time with its zone (r13 §4.10):

| Field | Meaning |
|---|---|
| `last_pass` | when the last pass finished, and its result: `installed`, `current`, `deferred`, `skipped`, `failed`, `rolled_back` |
| `last_check` | when the last fetch succeeded |
| `waiting` | the release found and not installed, and since when |
| `told` | per notice kind, the tag or snapshot id it was last sent for, so each is sent once |

A missing or unreadable file is an empty one: the worst case is one repeated notice.

### 4.5 What master hears

One `report` from `updater` to each touched project's escalation target (the project's
`AGENT_BUS_ESCALATE_TO` in its env file, as the dispatcher reads it, `internal/dispatchsvc/dispatchconfig.go:35`; master by default). The sender `updater` is a service identity like `dispatch` and `conductor`.

| Event | Wake | Sent to |
|---|---|---|
| Installed: from, to, the restarted services, the skills changed with their `skill-users` command, the undo command | no | every project restarted |
| Failed and rolled back: why, and that the tag is not retried | yes | every project in step 4's list |
| The rollback failed too: the manual commands (`update.manualCommands`) | yes | the same |
| An interrupted update: `agentbus update --rollback <id>` | yes | the same |
| A release that needs a migration | no | the same |
| A release waiting longer than `updates.max_wait` (7 days), with the reason of the last pass (the window never reached, or the project and agent in work) | yes | the same |

Each is sent once per tag or snapshot (`told`). A broker that cannot be reached loses the notice,
not the update; the history line and `auto.json` still record it.

## 5. Pruning old trees

**When:** after every successful update, manual or automatic, right after `update.Prune` removes
old snapshots. `agentbus update --list` also lists the trees, each with why it is kept.

**Which:** each directory `<src>/<name>` where `<name>` is a `vX.Y.Z` tag and the person's clone
lists it as a worktree (`git worktree list --porcelain`). Nothing else under `<src>` is touched.

**A tree is kept** while any of these holds:
1. It is the installed binaries' tree (`buildinfo.Repo` of `<bindir>/agentbus`, read by running
   its `version`).
2. A kept snapshot's manifest names it, as `from_tree` or `new_tree`. A rollback needs it: the
   restored binaries are stamped with it.
3. A holder build of it is still on disk: a holder unit of any project, or a binary copy under
   `<data>/agentbus/holder/`, whose key is `service.BuildKey(<tag>+"+"+<tree HEAD, 12 chars>)`, the
   key the tree's own build gets (`holder.BuildID`). Agents launched by that holder may still read
   the tree's `roles.toml` and scripts.
4. It is dirty (`mission.Git.Dirty`). A managed tree is never edited, so a dirty one is the
   person's: kept and named.

**Before the test,** the prune runs the existing holder collection for each project with running
services: `service.GCHolders` and `service.GCHolderBinaries`, with the new build as current. They
remove only a holder that is not active and whose socket does not answer, so they never stop an
agent. Without them an old holder unit, inactive for weeks, would pin its tree forever.

**How:** `git -C <clone> worktree remove <tree>`, never `--force`, so git itself refuses a dirty
tree; then `git -C <clone> worktree prune`. A clone that is gone (the tree's `.git` points to
nothing) removes nothing, and the update says so, as step 1 §3.2 already refuses.

**What it may break:** a busmon started before the update reads its own tree for About's release
notes. That busmon's About shows "notes not found" until busmon restarts. Nothing else reads a tree
whose holders are all gone.

## 6. Who may change it

- **`updates.auto`, `updates.window` and `updates.max_wait` are `PersonOnly`:** `config.Set`
  and `Unset` refuse them, and busmon shows them without editing (`config/write.go:60`, `:98`;
  `cmd/busmon/settings_ui.go:402`). They are machine keys, not `Project`: the updater belongs to
  the host. `roles.toml` already denies agents `Edit(~/.config/agentbus/config.toml)`
  (`rolecfg/configdeny_test.go`).
- **`update --auto`** gets step 1's agent refusal (`agentcaller.Refuse`, step 1 §5). The updater
  service is started by the service manager, so no agent is among its ancestors.
- **Under `broker.acl`,** a pass connects as any CLI command does: `connectEntryOf`
  (`cmd/agentbus/connectentry.go:11`) maps `update` to `bus.ACLEntryCLI`, and with no identity and
  no agent among its ancestors `ACLUserFor` names `busmon` (`bus/acl.go:204`). That holds whether
  the updater service or the person's shell started it, and the build changes nothing there. The
  `busmon` user reads `{p}:agents` and the conductor's records and writes reports, which is all a
  pass does on the broker.
- **`update --auto --check`** reads only, and is not refused, like `--check`.

## 7. Settings (in `config/keys.go`)

| Key | Default | Meaning |
|---|---|---|
| `updates.check` | on | exists: busmon's daily release check |
| `updates.auto` | `notify` | `notify` (busmon's header only) or `install` (the updater installs in the window) |
| `updates.window` | `03:00-05:00` | when `install` may run, local time (question 2) |
| `updates.max_wait` | 7 d | when to tell master a release is still waiting |

r13's `off` and `stage` values, `updates.health_wait` and `updates.keep` are dropped: `off` is
`updates.check = false`; `stage` saves a build that takes a minute; step 1 fixed the 60 s settle
and the 3 snapshots as constants.

## 8. Testing

- **The seam:** step 1's fakes (a temporary repository with tags on `main` and a stray one, a
  fake build through setup's `Runner`, a fake service manager), plus a fake clock for the loop and
  a disposable Redis for `{p}:agents`.
- **The loop:** a pass inside the window, one per occurrence; none outside it; a check pass after
  24 hours outside; a window across midnight; a last pass in the future; `notify` runs nothing; a
  setting changed between ticks applies at the next.
- **The quiet test:** an agent `working` defers; a status changed 9 minutes ago defers, 11 minutes
  ago does not; a project with no running service is not judged; a mission `checking` or `integrating` defers,
  one `in_review` or `integrated` does not; an active holder
  with `dispatch` and `missions` absent and one agent `working` defers; an unreadable holder state
  defers.
- **The skips:** a `.migrate` tag is never installed and is reported once; a tag with a failed
  automatic install is not retried; a manual `update` of it still runs.
- **Never a branch:** after a pass, the clone's branches, `HEAD` and status are unchanged.
- **The fetch never prompts:** with no `SSH_AUTH_SOCK` and a remote that asks for a password,
  the pass fails within its timeout instead of hanging.
- **Notices:** each kind once per tag; a broker down loses the notice and keeps the history line.
- **Pruning:** a tree named by a kept snapshot stays; the installed tree stays; a tree with an
  active holder unit stays; a dirty tree stays and is named; an inactive, silent holder is
  collected and then its tree removed; a directory under `<src>` that is not a worktree, or not a
  `vX.Y.Z` name, is never touched.
- **Refusals:** `update --auto` under `AGENT_BUS_AGENT` is refused; `config set updates.auto
  install` is refused.
- **macOS:** the launchd job starts, survives a `down`, and leaves a pass running when unloaded
  (manual check on a Mac, with step 1's signature check).

## 9. Out of scope

- r13's managed clone and `install.sh`; broker migrations; busmon running the update; holder
  drains; Windows.
- A per-project opt-out of automatic restarts (question 1's option C, if the person wants it).

## 10. Questions for the person

1. **Which projects does an automatic install restart?**
   - **A (recommended): every project whose services run on this host, and only when all are quiet.**
     The binaries are per host, so every project runs the new `agentbus` at its next call anyway.
     One snapshot covers all of them, and a red service rolls everything back.
   - B: none. It installs the binaries and reports each stale service with its `service restart`
     command, for master or the person to run.
   - C: only the projects listed in a new `updates.auto_projects`; the others are listed, as in B.
2. **The default window?**
   - **A (recommended): `03:00-05:00`.** Few turns run then; a laptop asleep at that hour
     triggers the `max_wait` notice after 7 days.
   - B: `12:00-14:00`, for a machine that sleeps at night.
   - C: no window: any time the team is quiet.
3. **Which old trees are removed?**
   - **A (recommended): any tree nothing needs (§5):** not installed, not named by a kept
     snapshot, no holder build of it, clean. With 3 snapshots, at most about four trees stay.
   - B: the same, but always keep the previous release's tree too, even after its snapshot is
     gone.
   - C: keep the N newest trees, whatever uses them.
4. **What does master hear?**
   - **A (recommended): the table of §4.5:** an install without a wake, a failure or a long wait
     with one.
   - B: every pass, deferred ones included, without a wake.
   - C: nothing on the bus: the history line and `agentbus update --list` only.

## Changelog

- **r2** (foureyes on r1): the quiet test also reads the conductor's missions: a mission
  `checking` or `integrating` keeps its project in work (§4.3 step 4, §8).
- **r3** (reviewer-codex on r1): the projects judged include every project with an active holder,
  not only those with running `dispatch` or `missions`; an unreadable state defers the pass
  (§4.3 step 4, §8). foureyes' notes on r1: the ACL user of a pass (§6), and a stale `working`
  caught by the `max_wait` notice (§4.3).
- **r4** (foureyes on r3): §6 corrected: a pass connects as `busmon`, the CLI entry
  (`connectEntryOf`), not as `dispatcher`; nothing changes in the build.
