# One-command setup and quiet update, with a snapshot to roll back to

Status: r12, spec only. Base: origin/main `c7e1400c`. Author: architect, from the ruflo analysis
the person approved on 2026-11-14 (task `quiet-update-spec`; r12 is task `quiet-update-r12`, the
person's review of r11). Low priority: it comes after the herdr exit work, and it assumes that
work is built (no herdr; the holder; `services.manager` with the `none` supervisor, herdr exit
§4.4b). The revision history is in the changelog at the end.

## 1. Problem

- **Installing takes two steps and some reading.** You clone, then run `./setup.sh install`.
- **Updating is manual and from a developer's angle.**
  - `./setup.sh update` (`setup/update.go`) pulls the checkout's branch, not a release.
  - It then reinstalls, and prints which project services "keep running the previous agentbus
    until restarted" (`setup/plugin.go`).
  - The person restarts those by hand, and there is no way back if the new build misbehaves.
- **busmon already knows when a release is out:** the header shows `↑ vX.Y.Z` (`cmd/busmon/update.go`,
  GitHub releases, once a day, `updates.check`). It cannot act on it.

The person wants an update that works like a router's firmware update:
- a discreet notice;
- an update that runs in the background and never cuts service: running agents are never stopped;
- a rollback that brings back the state from before the update.

## 2. Decided (the person, 2026-11-14)

1. **Install:** download, read, then run. No `curl | sh` in the documentation.
2. **`updates.auto` defaults to `notify`.** Today's behaviour; nothing installs on its own unless
   the person chooses it.
3. **An automatic install waits for a window.**
4. **Updates follow release tags,** not the branch's HEAD.
5. **Rollback restores a snapshot.** Before changing anything, an update records the builds, the
   settings, the service units and what is needed to restore the running team.
   `agentbus update --rollback` restores it.
6. **Release notes in For you:** only the version. The notes are one key away in About.
7. **Platforms:** macOS and Linux, with or without systemd (the person, 2026-11-11 and r11
   review). Windows is out. §4.8 says what each service mode supports.
8. **Simple, fast, reliable, no gas factory.** The guarantee is against a process crash or kill,
   not power loss (master, 2026-11-14). r12 cut the write-ahead journal down to one plan written
   with the snapshot (§4.3 step 4, changelog).

## 3. Solution, from the person's side

- **Install:** download `install.sh` from the repository, read it, then run `sh install.sh`.
  1. It checks git, Go and Docker, and clones the repository into the **managed clone**,
     `~/.local/share/agentbus/src/repo`. It then checks the latest release tag out as that release's
     own **source tree**, `~/.local/share/agentbus/src/<tag>/` (a `git worktree`, §4.1).
  2. It runs that tree's `./setup.sh install`, which asks only what it must, one question at a time,
     each with a recommended answer.
  3. It never asks for a secret in a chat.
- **Notice:** the header's `↑ vX.Y.Z` stays. For you shows only the version; About gives the
  release notes. Once the palette's class C lands, the palette gains `/update` (argv shown, `y`
  runs `agentbus update`). There is never a pop-up or a bell.
- **Update:** `agentbus update`, or the scheduled updater when `updates.auto` is `install` and the
  host has a scheduler (§4.7).
  1. It records a snapshot.
  2. It builds the new release beside the current one.
  3. It swaps the commands, then restarts the project services one at a time, each checked by
     health.
  4. Agents keep running. They move to the new build only when they are next relaunched.
  5. If any check fails, the update rolls itself back and says why in For you (except a
     migrating release's broker, which is the person's step, §4.5.3 step 5).
- **Rollback:** `agentbus update --rollback` restores the last snapshot. `--list` shows the
  snapshots, and `--rollback <id>` restores an older one.

## 4. Design

### 4.1 Where an update comes from

- **One source tree per release; no tree is ever moved.** The source tree is runtime state, not
  just provenance: binaries read it while they run.
  - **What reads it:** `buildinfo.Repo` names it, and three places read it at run time:
    - `scripts/bootstrap` (`cmd/agentbus/up.go` `repoDir`);
    - `roles.toml`, through `rolecfg.ManifestPath` when the tree holds one;
    - About (`cmd/busmon/about.go`).
  - **One tree per tag:** each release is checked out as its own worktree of the managed clone,
    `<src>/<tag>/`. Its binaries are stamped with that tree as `buildinfo.Repo`.
  - **A binary and its tree switch together.** Each binary names its own tree, so installing a
    binary also switches which tree that binary reads. Old binaries never see a new `roles.toml`,
    and new ones never see an old one.
  - **Rollback** swaps them back to a tree that is still there.
  - **A tree's lifetime** is that of its build. It is removed only when no kept snapshot names it
    and its holder build is gone, under the herdr-exit cleanup rule (herdr exit §4.4a: instance
    not active, no socket). A draining agent launched with that tree's `ROLES_TOML` keeps reading
    it until it ends.
  - The person never edits a managed tree. A dirty tree refuses the update and says so.
- **Managed or developer build:** `update` compares `buildinfo.Repo` with the managed source
  directory. A path under `<src>/` is a managed build. Any other path, such as the dogfood
  repository or any clone the person works in, is a developer build. So is an empty `Repo` (a `go
  install`).
  - A developer build is never moved by `agentbus update`, which only notifies.
  - There, `./setup.sh update` keeps its developer behaviour of pulling the branch.
- **A release** is the newest tag `vX.Y.Z` on the remote that is an ancestor of `origin/main`
  (`git merge-base --is-ancestor`). A stray tag on another branch is never installed. A tag older
  than the running version is never "updated" to: that is a rollback, done by name.

### 4.2 The snapshot

An update writes it **before any change** under `<state>/agentbus/updates/<id>/`, where `<id>` is
the host's local time plus the from and to versions. It is not Redis `TIME`: the broker may be the
very thing an update or a rollback has to work around. The update stops, changing nothing, if
any part cannot be written. The snapshot holds:

| Part | What | Where it comes from |
|---|---|---|
| **builds** | the running binaries (`agentbus`, `busmon`), their version, rev and build key (`service.BuildKey`), copied, not linked | the bin dir and the repository copies `BuildAndInstall` writes |
| **source tree** | the from tree's path and rev (`git rev-parse HEAD` of `<src>/<from-tag>/`); the tree itself stays in place and is not copied | `buildinfo.Repo` of the running binaries |
| **settings** | `config.toml`, each project's env file, `notify.toml`, a `roles.toml` named by `ROLES_TOML` outside the tree (the tree's own `roles.toml` is versioned with the tree, not a setting), every file of every installed role skill; each copied | the paths `config` and `setup` already know |
| **services** | the service mode (`systemd`, `launchd` or `none`, §4.8) and, per project, each service (dispatch, missions, holder per build) with its active state. Under systemd and launchd, each unit file too. Under `none` there is no unit file: its supervisor's pidfile record (kind, build key, pid and start time) | `service list`; the unit directory (systemd), LaunchAgents (launchd), or `<rundir>/agentbus/<project>/` (none) |
| **team** | per project: the roster from `{p}:agents` (role, profile, client, holder build), the `{p}:resume` records (`{session, profile, at}`), pause and suspend state, and the current holder build | the broker, read only |
| **broker** | a `agentbus backup` archive of the record keys of every project, made by the existing backup code; secrets left out as backup does | `backup` (2026-11-01 spec) |
| **restorer** | a copy of the new `agentbus` that writes the snapshot, plus the manual `README` (§4.5.3) | the staged build (§4.3) |
| **manifest** | `manifest.json`: a **format version**; the parts and their hashes; the from and to tags; whether the release declares a **broker migration** (§4.6); and the **write plan** (§4.3 step 4) | written last; a snapshot with no manifest is incomplete and never restored |

Retention: the last `updates.keep` snapshots (default 3). An older one is removed only after a newer
update has succeeded.

### 4.3 The update, step by step

**Who runs what.** The **new** version does everything that writes or reads a snapshot. The running
(old) binary may be older than this feature and may have no `agentbus update` at all, so it can
neither write a snapshot nor restore one.

- **The old binary checks and stages only** (steps 1 and 2), then execs the staged new binary:
  `<builds>/<buildkey>/agentbus update --apply`. It never installs. `setup/update.go` keeps the
  same rule: "the pull may change the steps, so this running binary never installs".
- **The new binary** writes the snapshot of the state still in place (step 3: old binaries, old
  tree, settings, services, team, broker archive), then runs steps 4 to 8.
- **The new binary copies itself into the snapshot as its restorer**, `<snapshot>/restorer/agentbus`.
  It wrote the manifest, so it always knows the manifest's format.
- **A rollback** of that snapshot runs that restorer (§4.5). The old binaries in the snapshot are
  only the files to put back; they never have to understand anything.
- **The first update into this feature, from a binary without `agentbus update`:**
  - On a developer build, `./setup.sh update` pulls, then execs the pulled installer
    (`setup/update.go`).
  - On a managed tree, the person re-runs `install.sh`.
  - Either way, the binary that runs is the new one, so it can snapshot first, and the snapshot can
    be rolled back.

1. **Check** (old binary). Find the release (§4.1). Already there: done. Developer build: notify
   only.
2. **Stage** (old binary), then exec the staged binary with `--apply`. Staging changes nothing in
   place, so the snapshot taken next still sees the whole old state. In detail:
   - Add the tag's source tree (`git worktree add <src>/<tag> <tag>` in the managed clone), and
     build the stamped binaries into `<datadir>/agentbus/builds/<buildkey>/`. Their
     `buildinfo.Repo` is that tree.
   - Run the new `agentbus version` and `busmon --version`.
   - Any failure: nothing has changed. The new tree and build are dropped, and For you says why.
3. **Snapshot** (the new binary, §4.2), before any change, with the restorer copy, the write plan
   (step 4) and the README (§4.5.3). If it cannot be written completely, the new tree and build
   are dropped and nothing has changed.
4. **Swap the commands** (the new binary from here on).
   - **Binaries:** install `agentbus` first, then `busmon`, each with `InstallFile`, which copies
     beside the target and then renames (verified: `setup/build.go`). A running agent keeps the
     file it started; the next call runs the new one, and reads the new tree. No tree is moved.
     - **The two renames.** They are two renames, not one. Between them, which lasts as long as
       one file copy, `agentbus` is new and `busmon` is old. That window is harmless: it is the
       state every update leaves anyway while an old busmon keeps running (step 7). An old busmon
       reads its own tree, and its commands go through the `agentbus` beside it (`agentbusBeside`,
       `cmd/busmon/pick.go`), which is the new one and accepts the old argv (the CLI's flags only
       grow, and the subscribe JSON is versioned). The reverse pairing, a new busmon with an old
       `agentbus`, never happens in an update, because `agentbus` goes first. A rollback installs
       in the reverse order, `busmon` first, for the same reason. A crash between the two renames
       is undone by rollback, which puts both binaries back whatever their state.
   - **Settings and skills,** by the write plan:
     - **The plan is complete before anything changes.** The new content of every file the update
       writes is built at step 3: the skills from the new tree, and a migration's settings from
       the current files (§4.6). The plan is a list of `{path, before, after}`, each a SHA-256 or
       the reserved value `absent`. A created file (a skill the release adds) has `before:
       absent`; a deleted one (a skill it drops) has `after: absent`. Skills are listed **per
       file**, and an empty directory left behind is removed with its last file.
     - The plan is written in the manifest, with the snapshot, so it is on disk before the first
       write.
     - Each write first checks that the file's current hash is still `before`. If the person
       changed it after the snapshot, the update stops and rolls back. Then it writes the file:
       a copy beside it, then a rename (`InstallFile`), or a removal for `after: absent`.
     - `config.toml` and the env files are in the plan only when the release declares a migration.
     - **The guarantee is against a process crash or kill** (the updater killed, `kill -9`, an
       OOM). Every file the update may write is in the plan before the first write, and each
       rename is atomic, so a file is wholly `before` or wholly `after`. Rollback decides each
       file by its current hash alone (§4.5.1). A third hash is the person's edit.
     - **Power loss or a kernel crash is out of scope** (decision 8). After a power cut during an
       update, a file may be neither `before` nor `after`; it is then kept and listed like the
       person's edit, and the README, which lists every file with its snapshot copy, lets the
       person pick. The README states this limit in its first lines.
5. **Services,** one project at a time and one service at a time (dispatch, then missions).
   - `agentbus service restart <kind>`, then wait for `agentbus health` to show that service green
     within `updates.health_wait` (default 60 s). The command is the same in the three service
     modes (§4.8); under `none` it stops the old build's supervisor and starts the new one's.
   - The dispatcher is fenced by its lease and reads from the stream, so agents are not touched.
     To verify by test: a wake for an entry written during the restart is delivered after it.
   - A red check is an automatic rollback (§4.5) of everything done so far, reported in For you.
6. **Holder: never restarted** (herdr exit §4.4, §4.4a, §4.4b).
   - The new build's holder is started beside the old one, and becomes the current build: a new
     unit under systemd or launchd, a new supervisor under `none`.
   - New terminals (`up`, a relaunch, a pop, self-repair) open on the new build. The old build
     drains as its terminals end.
   - `holder_draining` warns after `holder.drain_warn`, and `agentbus holder drain` finishes the
     drain on the person's word.
   - The update never drains on its own: that would relaunch agents.
7. **Tell.**
   - One report to master and the person: from and to, the release notes path, and which skills
     changed. The skills list is the one `setup update` prints today; master tells their users with
     a wake=no directive, as now.
   - A running busmon keeps its old binary, and its header says "updated to vX: restart busmon to
     use it". It never re-executes itself.
8. **Record** the outcome in `<state>/agentbus/updates/history` (from, to, at, ok, snapshot id).

### 4.4 The window (decision 3)

An automatic install (`updates.auto = install`) runs only when both hold:

- **Inside `updates.window`,** local time, default `03:00-05:00`.
- **Every project is quiet:** no agent working and none with background work (`agentstate`
  background judgement, herdr exit §4.3b), for at least 10 minutes. A suspended project counts as
  quiet.

Outside the window, or with a project busy, the scheduled updater waits for its next pass. After
`updates.max_wait` (default 7 days) without a window, it stops waiting and says so in For you.
It never installs outside the window.

The quiet condition is not about safety: no step stops an agent or touches its terminal. It is about
attention. A dispatcher restart delays wakes by a few seconds, and the skills-changed directive
should land when nobody is mid-task. So a manual `agentbus update` ignores both the window and the
quiet condition: the person chose the moment.

### 4.5 Rollback

`agentbus update --rollback [<id>] [--dry-run]` restores a snapshot. It never stops a running
agent.

Two ways, **one order**: the restorer (§4.5.2) and the manual README (§4.5.3) run the same
numbered steps, and each README step names its restorer step. The order holds one rule above all:
**no old service starts while a migrated broker is still in place**, so old binaries never run
against a migrated keyspace.

#### 4.5.1 Shared rules

- **Settings and skills: three cases,** on the plan's `before` and `after`. "current" is the
  file's hash, or `absent` when it does not exist.
  - **current = after:** the update's result is untouched, so it is undone. A file with `before:
    absent` is deleted. Any other file is copied back from the snapshot, which also recreates a
    file the update deleted.
  - **current = before:** nothing to do. This includes a file the update never reached before it
    was stopped.
  - **anything else:** the person changed it since (edited, created again or removed). It is kept
    as it is and listed with the path of its snapshot copy, if one exists. The person decides;
    nothing is overwritten silently.
  - A file not in the plan was never written by the update and is left alone.
- **The broker is never automatic.** It is restored only when the manifest declares a migration
  (§4.6), only for a project that is not live (below), and only with the person's `y`. Messages
  written since the update otherwise stay.
- **Not live** is restore's own test, `projectLive` (`cmd/agentbus/restorecmd.go`). It checks
  these four, and nothing else:
  - no dispatch or conductor lease held;
  - no service active;
  - **no terminal of the project on any holder build** (`termhost` `List` for the project, on every
    build whose holder answers);
  - no registered agent's verified pid alive.

  `agentbus suspend` ends the project's terminals, its services and its leases, so a suspended
  project passes with no extra step.

#### 4.5.2 Restorer

Rollback is run by the snapshot's **restorer**, the binary that wrote it:
`<snapshot>/restorer/agentbus update --restore <id>`. `agentbus update --rollback`, run with
whatever binary is installed, only finds the snapshot and execs its restorer. An older binary that
predates the feature cannot run `--rollback`; the person runs the restorer path directly, and
`--list` and the For you item print it. A manifest with an unknown format version, or marked
incomplete, is refused.

1. **Stop** the dispatch and missions services the update restarted (`service stop`, in the
   snapshot's service mode). Agents keep running.
2. **Binaries and tree.**
   - The from tree is checked against the recorded rev. A from tree that is missing or moved is
     recreated with `git worktree add` at that rev, **before** the binaries are swapped.
   - The snapshot's binaries go back with `InstallFile`, `busmon` first, then `agentbus` (§4.3
     step 4). They are stamped with the from tree, so each switches back to it as it lands.
3. **Settings and skills,** by the three cases (§4.5.1).
4. **Services' definitions.** Under systemd and launchd, the unit files are rewritten from the
   snapshot (`daemon-reload` under systemd); they are **not started yet**. Under `none` there is
   nothing to rewrite. The snapshot's holder build becomes current again, and its holder is
   started if it is not running. It serves terminals, not the broker. The old build may have been
   collected since the update (`service.GCHolders` removes a drained build's unit and its
   `<datadir>/agentbus/holder/<key>/` once its holder exits), so the restorer first puts it back:
   - its binary copy, from the snapshot's `agentbus`, through `service.InstallHolderBinary` (the
     directory at 0700, the binary, and `build-raw`), as `up`'s `startHolder` does;
   - its unit or job, from the snapshot (systemd, launchd);
   - then `StartHolder` for that build key (systemd, launchd), or its supervisor (`none`).

   The newer holder build then drains, by the same rule as an upgrade. No new command is needed:
   this is the restorer's own code.
5. **Broker, only when the manifest declares a migration,** and only as `agentbus update --rollback
   <id> --broker`.
   - Every migrated project must be not live (§4.5.1). `--broker` runs the test first and stops on
     the first reason, as `projectLive` words it, with the step that clears it (most often:
     `agentbus suspend <p>`). Nothing is restored until every migrated project passes.
   - It then restores the record keys from the snapshot's archive through the old binary's
     `agentbus restore --force`, with the person's `y`. The confirmation names the time span lost:
     what was written since the update.
   - Without `--broker`, this step does nothing, and a migrated project stays suspended.
6. **Start** the dispatch and missions services, one at a time, each checked by health as in
   §4.3 step 5. A migrated project's services are **not started** until its broker is restored and
   the person resumes it.
7. **Team:** every role in the snapshot's roster that is **not running now** is relaunched on its
   profile with its resume session, one at a time, through the existing relaunch path. A running
   agent is never touched. A role whose resume record no longer validates follows the herdr-exit
   rule: a fresh start with a notice for `up`, and no Start for self-repair.
8. **Record** the rollback in the history, and report to master and the person.

**Automatic rollback** after a red check (§4.3 step 5) is this procedure, limited to what the
update had already done, **without step 5**. For a release with no migration that is everything,
because the broker was not touched. For a migrating release (§4.6), it leaves the migrated
projects suspended and puts one For you item with the exact `agentbus update --rollback <id>
--broker` command. The person runs it, or resumes the projects on the old build if the old build
reads the migrated format (the `.migrate` file says whether it does).

#### 4.5.3 Manual README

**If the restorer itself cannot run** (a broken build), the snapshot is plain files, and its
`README` is a complete manual restore of every part, in the restorer's order.

- **Written once, with the snapshot** (§4.3 step 3). Every path, rev, hash, unit name, pid and
  project in it is the snapshot's own, and the plan already holds every file the update may
  write, so it never needs rewriting. Nothing in it needs the new binary.
- **Its first lines** state the guarantee's limit (§4.3 step 4) and, if a part is missing, that
  the README cannot be followed in full. The manifest is then marked incomplete, and the
  restorer refuses it too.
- **Commands for this host's service mode only,** taken from the snapshot: the README never lists
  a command the host cannot run.
- **Hash commands:** `sha256sum` on Linux, `shasum -a 256` on macOS; `test -e` for `absent`.
- **Portable commands only:** besides the service manager's own (`systemctl`, `launchctl`) and the
  old `agentbus`, the README uses only commands stock Linux and stock macOS both have: `cp`, `mv`,
  `rm`, `mkdir`, `chmod`, `printf`, `test`, `ps`, `kill`, `nohup`, `git`, and the hash command
  above. No `setsid`, no GNU-only flags.

The steps (R*n* is restorer step *n*):

1. **Stop** (R1) each dispatch and missions service the update restarted, listed by name:
   - systemd: `systemctl --user stop <unit>`;
   - launchd: `launchctl bootout gui/<uid>/<label>`;
   - `none`: `kill -TERM <pid>` of the new build's supervisor, after `ps -p <pid> -o command=`
     shows `agentbus service supervise <kind> <project> <buildkey>`.

   Agents keep running.
2. **Binaries and tree** (R2):
   - `git -C <src>/repo worktree add <from-tree> <rev>` if the from tree is missing;
   - then for `busmon`, then `agentbus`: `cp <snapshot>/builds/<name> <target>.tmp` and
     `mv <target>.tmp <target>`.
3. **Settings and skills** (R3): one line per file of the plan, with its `before`, its `after`,
   its snapshot copy and the three cases of §4.5.1 as commands:
   - current = after: `rm <path>` when `before` is `absent`, otherwise
     `cp <snapshot copy> <path>.tmp && mv <path>.tmp <path>`;
   - current = before: nothing;
   - anything else: compare with the snapshot copy and keep the one you want.
4. **Services' definitions** (R4):
   - systemd: copy each unit file back, then `systemctl --user daemon-reload`; do not start;
   - launchd: copy each plist back; do not load it;
   - `none`: nothing.

   The holder unit or plist of the old build is among the files copied back: the holder's gc
   may have removed it. Then, for each project, if the old holder build is not running:
   - if `<datadir>/agentbus/holder/<key>/agentbus` is missing (gc'd after the drain), put the
     build back by hand, as `service.InstallHolderBinary` does: `mkdir -p -m 700
     <datadir>/agentbus/holder/<key>`, `cp <snapshot>/builds/agentbus
     <datadir>/agentbus/holder/<key>/agentbus`, `chmod 700` it, and write the build's raw id
     (printed in the README) to `<datadir>/agentbus/holder/<key>/build-raw`;
   - start it: systemd `systemctl --user start <holder unit>`; launchd `launchctl bootstrap
     gui/<uid> <plist>`; `none` `nohup <datadir>/agentbus/holder/<key>/agentbus service supervise
     holder <p> <key> >> <log> 2>&1 < /dev/null &` (herdr exit §4.4b's supervisor). `none` runs on
     macOS by choice too, and stock macOS has no `setsid` command, so the README uses `nohup`, on
     both systems. The supervisor is then not a session leader, as `up` makes it, but it ignores
     the hangup when the person's terminal closes, and the shell's background job keeps it out of
     the terminal's Ctrl-C. It ends at the next `agentbus down`, like any supervisor.
   - **Requirement on the supervisor and the holder** (herdr exit §4.4b `service supervise`, and
     `agentbus holder`, today `cmd/agentbus/holdercmd.go`): `nohup` leaves SIGHUP **ignored**, and
     an ignored signal survives `exec`, so without a handler the holder and every agent it starts
     would inherit it. The holder's close (SIGHUP, then SIGKILL after the grace) and the PTY hangup
     of a dead holder would then no longer end an agent gracefully. So each of them installs a
     SIGHUP handler at start (`signal.Notify`) and keeps it for its whole life: the supervisor
     treats SIGHUP as nothing, the holder as nothing. A process with a handler execs its children
     with SIGHUP at its default, whatever the launching shell did. This makes the README's `nohup`
     start equivalent to `up`'s. It must be in every build that has a holder. No release has one
     yet (v0.28.0, the newest tag, has no `holdercmd.go`), and today's holder handles only
     SIGINT and SIGTERM, so the handler is added to the holder and to `service supervise` now,
     ahead of the rest of this spec: a small change, routed by master to coder.

   Every name, key, raw id and path is written into the README by the restorer; none comes from
   a command the old binary may lack.
5. **Broker** (R5), only when the manifest declares a migration; otherwise the README says the
   broker is not touched:
   - `agentbus suspend <p>` for each migrated project, if it is not suspended;
   - then `agentbus restore <snapshot>/broker.tar… --project <p> --force`, with the old binary,
     answering `y`. `restore` predates this feature (2026-11-01 backup spec) and refuses a live
     project by itself.
6. **Start** (R6), with the old binary, for each project that is not migrated, or whose broker
   was restored: `agentbus --project <p> service start dispatch`, then `missions`, then
   `agentbus --project <p> health`. The command is the same in the three modes: the unit or plist
   restored at step 4 is what it starts, and under `none` it starts a supervisor.
7. **Team** (R7): `agentbus up <project> --resume` for each project whose roster in the snapshot
   has roles that are not running. A running agent is never touched.
8. **Record** (R8): nothing to run. A manual restore writes no history line; tell master that
   the snapshot was restored by hand.

### 4.6 Broker migrations

- A release that changes the broker's record format says so in its release notes **and** in a file
  the updater reads: `docs/releases/<tag>.migrate`, listing the migration, its version, and whether
  rollback needs the archive.
- With no such file, the update never writes to the broker beyond what the new services write in
  normal running.
- **A migration's settings changes are pure:** their new content is computed from the current
  files at step 3, so they are in the write plan (§4.3 step 4).
- **A migrating release is never installed automatically.** With `updates.auto = install`, the
  scheduled updater notifies instead: "vX needs you: it migrates the broker", with the release
  notes' path.
- **A manual update of a migrating release** requires every project it migrates to be suspended
  (`agentbus suspend`; resume brings the agents back on their resume sessions), and not live by
  restore's test (§4.5.1). The migration and its rollback then share one precondition. Otherwise
  it refuses, names the projects with the reason the live test gives, and gives the step.
  - Suspension makes those projects not live, so both the migration and a later broker rollback
    run against a keyspace nothing else writes.
  - Step 5 restarts only the services of projects that are not suspended.
  - A migrated project's services start at `agentbus resume`, with the same health check. A red
    check there is reported, and the person rolls back with `--broker`, suspending the project
    again first.
- The subscribe JSON's additive-only rule (`"v"`) already keeps most releases migration-free.

### 4.7 The scheduled updater

- **It is not the dispatcher.** The dispatcher must never run repository code (CLAUDE.md), and an
  update builds code. It is `agentbus update --auto`, run hourly by the host's scheduler.
- **Written only when `updates.auto` is `stage` or `install`.** With `notify` or `off` there is no
  scheduler entry, and busmon's own daily check is the notice.
- **By service mode:**
  - systemd: a user timer plus a service, `agentbus-update`, hourly;
  - launchd: a job with `StartCalendarInterval`, hourly;
  - `none`: **agentbus writes no scheduler.** There is no unit to own the job, and a crontab is
    the person's file, not agentbus's. `updates.auto = stage` or `install` is accepted, but
    `agentbus service status`, About and a For you item state "no scheduler on this host" with the
    fallback: add `agentbus update --auto` hourly to your own crontab, or update by hand with
    `agentbus update`. `--auto` applies the window and the quiet rule whoever runs it.
- **`stage`** does steps 1 to 3 and stops. It tells the person a build is ready, and
  `agentbus update` then installs it without rebuilding.

### 4.8 Service modes (Linux without systemd)

The service mode is herdr exit §4.4b's `services.manager` (`auto | systemd | launchd | none`).
Everything this spec does to services goes through `agentbus service` and the holder, which work
in all three; the table states what differs.

| | systemd | launchd | `none` (Linux without systemd, or by choice) |
|---|---|---|---|
| Restart a service (§4.3 step 5) | `service restart` | `service restart` | `service restart`: stops the old build's supervisor (SIGTERM), starts the new build's |
| New holder build (§4.3 step 6) | a second unit | a second job | a second supervisor; the old one drains |
| Snapshot of services (§4.2) | unit files and states | plists and states | supervisor pidfile records and states; no file to restore |
| Restorer (§4.5.2) | units rewritten, `daemon-reload` | plists rewritten | nothing rewritten; `service start` starts a supervisor |
| README stop (§4.5.3 step 1) | `systemctl --user stop` | `launchctl bootout` | `kill -TERM` of the checked supervisor pid |
| README start (§4.5.3 step 6) | old `agentbus service start` | old `agentbus service start` | old `agentbus service start` |
| Scheduled updater (§4.7) | user timer | `StartCalendarInterval` | **unsupported:** no scheduler written; cron line or manual update, stated in For you |
| Services after logout or reboot | per `services.start_at_login` | per `services.start_at_login` | not restarted (herdr exit §4.4b); `agentbus up` brings them back |

Under `none` nothing is unsupported for a manual update or a rollback. Only the unattended
scheduler is, and its fallback is above.

### 4.9 Settings (in `config/keys.go`)

| Key | Default | Meaning |
|---|---|---|
| `updates.check` | on | exists: busmon's daily release check |
| `updates.auto` | `notify` | `off`, `notify`, `stage`, or `install` |
| `updates.window` | `03:00-05:00` | when `install` may run, local time |
| `updates.max_wait` | 7 d | when to say there has been no window |
| `updates.health_wait` | 60 s | how long a restarted service has to turn green |
| `updates.keep` | 3 | snapshots kept |

## 5. Testing

- **The seam is `agentbus update` with fakes:** a fake git remote with tags, fake
  `systemctl`/`launchctl` (as the service tests do), the scripted `none` supervisor (herdr exit
  §4.4b tests), a fake `termhost` for the holder, and a disposable Redis only.
- **Each service mode:** the update, the automatic rollback and the README harness run once under
  systemd, once under launchd and once under `none`, with the same end state.
- **Snapshot:**
  - every part is written and its hash is in the manifest, with the write plan and the README;
  - one part failing to write stops the update with nothing changed.
- **Two updates in a row:** v1 to v2 to v3. Then:
  - v1's and v2's holder builds drain while v3 serves;
  - a rollback goes to v2, with v2 current and v3 draining.
- **Migrating release:**
  - it is not installed by the scheduled updater (a notice instead);
  - a manual update refuses while a migrated project is live;
  - an automatic rollback after a red check restores builds, tree, settings and services, leaves
    the broker and the suspension as they are, and opens the For you item;
  - `--broker` refuses on a live project and without `y` (fakes for `Live` and `Confirm`). That
    includes a suspended project with a terminal still on a holder build: refused with that
    reason and the step. Once the fake holder lists none, the restore goes ahead.
- **Automatic rollback:** a service that stays red after its restart rolls everything back, and
  the binaries are v1's again.
- **Settings on rollback**, in both the restorer and the README harness, with the same results.
  Take a config the plan records as before h0, after h1:
  - left at h1: restored to h0;
  - edited by the person to h2: kept at h2 and listed;
  - a file not in the plan: untouched.

  Plus an update killed after the first of two planned files: rollback restores that one and
  finds the second at `before`. Plus the crash windows, each a kill at that point:
  - **after the plan is written, before the first write:** every file at `before`; nothing is
    restored;
  - **after a rename:** that file is at `after` and is restored to `before`.

  Plus created and deleted files:
  - a skill the release adds is removed by rollback;
  - a skill it drops is recreated from the snapshot;
  - a skill the person deleted after the update stays deleted and is listed;
  - a settings file a migration created and the person then edited is kept.

  Plus a file the person changes between the snapshot and its write: the update stops at that
  file and rolls back.

  These are process kills, the guarantee's scope; power loss is not simulated.
- **Team on rollback:** a role that died during the update is relaunched with its resume session.
  A running agent is not touched; the test checks that no Start or Close was sent for it.
- **Broker:** with no `.migrate` file, rollback leaves the keyspace as it is. With one, it refuses
  without `--force`.
- **Window:**
  - outside the window, nothing is installed;
  - for an automatic install, a busy agent or one with background work defers; a manual update
    does not wait;
  - after `max_wait` there is a For you item.
- **Scheduler under `none`:** setting `updates.auto = install` writes no scheduler entry and opens
  the "no scheduler on this host" item; `agentbus update --auto` run by hand still applies the
  window.
- **Developer build:** `agentbus update` notifies only, and the checkout's HEAD is unchanged. An
  empty `buildinfo.Repo` counts as a developer build.
- **Trees:**
  - After an update, the old binaries (the snapshot's copies) still resolve `roles.toml` and
    `scripts/bootstrap` in the old tree, and the new ones in the new tree.
  - After a rollback, the installed binaries read the old tree again.
  - A rollback with the from tree removed recreates it at the recorded rev.
- **Who runs:**
  - the old binary never calls `InstallFile` on the bin dir and never writes a snapshot (a fake
    runner records the calls);
  - `--apply` runs from the staged binary and writes the snapshot with its restorer;
  - `--restore` runs from the snapshot's restorer;
  - a manifest with an unknown format version is refused.
- **README fallback:**
  - Generated for a migrating and a non-migrating snapshot, in each service mode, its commands are
    run by a test harness with the fakes above and a disposable Redis.
  - The resulting state equals the restorer's on the same snapshot: binaries, tree, settings,
    services, broker keys and the roster brought back.
  - Its steps are in the restorer's order and each names its R step; no old service starts before
    the broker step.
  - An old holder build gc'd before the rollback (binary directory and unit removed): the README
    and the restorer both put the binary copy, `build-raw` and the unit back, and the old holder
    runs again, in each service mode.
  - Every command the README names is in the portable list (§4.5.3), checked for a Linux and a
    macOS snapshot; the `none` harness runs with a PATH that has no `setsid`.
  - Under that `nohup` start, an agent the holder starts has SIGHUP at its default (on Linux, bit 1
    of `SigIgn` in `/proc/<pid>/status` is clear), and the holder's close ends it within the
    grace on SIGHUP, not on the SIGKILL. A holder or supervisor built without the handler fails
    this test.
- **First upgrade from a pre-feature binary:**
  - a stub old `agentbus` with no `update` command;
  - `setup.sh update`, then the managed `install.sh` path, each produce a complete snapshot before
    any change;
  - the restorer rolls back to the stub's binaries, which run again (`version`).
- **Swap:** a reader process that runs `agentbus version` in a loop during the swap never fails;
  the update installs `agentbus` before `busmon`, and a rollback `busmon` before `agentbus`.

## 6. Out of scope

- Windows (the person, 2026-11-11).
- A scheduler under `none` (§4.7): the person's crontab or a manual update.
- Power loss or a kernel crash during an update (decision 8): the README is the fallback.
- Downgrading the Redis or Valkey image. The broker container is the person's or `setup`'s, and an
  image change stays a manual step with its own notes.
- Remote or fleet updates, and signed releases: no signing beyond what git and GitHub give.
- Updating the AI clients themselves (Claude Code, Codex…): sentinel's `update` proposal already
  covers that.
- The "agent automaton": it is the Self Repair Watcher's autonomy, not an update matter (the
  person, r11 review).

## 7. Open questions

None. The person answered r11's two (§2 decision 6; §6).

## Changelog

- **r12** (the person's review of r11, task `quiet-update-r12`):
  - herdr removed: the live test's workspace check is the holder's terminal check (§4.5.1); the
    README's "close its herdr workspace" and the `--broker` herdr reasons and tests are gone;
  - Linux without systemd: service mode table (§4.8), the `none` cases in the snapshot, the
    restart, the restorer, the README and the scheduler (unsupported under `none`, with its
    fallback);
  - §4.5 split into shared rules, Restorer and Manual README, one order, README steps naming R
    steps;
  - the two binary renames and their window (§4.3 step 4); rollback installs in reverse order;
  - history moved here;
  - **complexity cut:** the appended write-ahead journal (`written.jsonl`, an intent line then a
    `done` line per file) is replaced by one **write plan** in the manifest, written with the
    snapshot before any change. Cut: the journal file, the `done` lines, the per-write appends,
    and the README regenerated after step 4 and before each rollback (it is written once). Kept:
    `absent`, per-file skills, the three cases, and the process-crash guarantee. Added: a check
    before each write that the file is still at `before`;
  - §7 answered: release notes are only the version in For you; the agent automaton is dropped;
  - (foureyes on r12) README R4 no longer names a `service start holder` command that does not
    exist; R4 in both the restorer and the README puts back an old holder build gc'd after its
    drain (binary copy, `build-raw`, unit), with a test.
  - (reviewer-codex on r12) §4.5.1 states only the target live test, the four checks; the note
    on today's herdr workspace check is gone;
  - (reviewer-codex on r12) the README starts a `none` supervisor with `nohup`, not `setsid`, which
    stock macOS lacks; the README keeps to a list of portable commands, with a test;
  - (foureyes on r12) `nohup` passes SIGHUP ignored down to the agents: the supervisor and the
    holder must install a SIGHUP handler at start, a requirement on the herdr exit's holder step,
    with a test.
- **r11:** files and skill directories the update creates or deletes, journalled with `absent`
  (foureyes on r9).
- **r10:** the journal's guarantee is a process crash or kill; power loss is out of scope, with
  the manual README as the fallback (master's decision on reviewer-codex's r8 finding, replacing
  r9's fsyncs).
- **r9:** fsyncs on the journal (replaced by r10).
- **r8:** the journal is write-ahead, so a crash between a file write and its record is covered
  (reviewer-codex on r7).
- **r7:** each file's hash is recorded right after the update writes it, in a journal the restorer
  and the README read (reviewer-codex on r6).
- **r6:** the snapshot README is a complete, ordered manual restore of every part, written per
  snapshot (reviewer-codex on r4).
- **r5:** `--broker` needs restore's whole live test cleared, the herdr workspace included
  (reviewer-codex on r3; removed in r12 with herdr).
- **r4:** the new version writes the snapshot and keeps itself in it as the restorer, so the first
  update from a version without `agentbus update` works (reviewer-codex on r2).
- **r3:** a broker migration is manual and needs its projects suspended, and its broker rollback is
  always the person's step (reviewer-codex on r1).
- **r2:** one source tree per release; the new binary installs, the snapshot's binary rolls back
  (foureyes on r1).
