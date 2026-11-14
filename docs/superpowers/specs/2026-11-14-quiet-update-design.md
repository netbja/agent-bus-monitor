# One-command setup and quiet update, with a snapshot to roll back to

Status: r1, spec only. Base: origin/main `c7279bea`. Author: architect, from the ruflo analysis the
person approved on 2026-11-14 (task `quiet-update-spec`). Low priority: it comes after the herdr
exit work.

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

## 3. Solution, from the person's side

- **Install:** download `install.sh` from the repository, read it, then run `sh install.sh`.
  1. It checks git, Go and Docker, and clones the latest release tag into the **managed clone**,
     `~/.local/share/agentbus/src`.
  2. It runs `./setup.sh install`, which asks only what it must, one question at a time, each with
     a recommended answer.
  3. It never asks for a secret in a chat.
- **Notice:** the header's `↑ vX.Y.Z` stays, with About giving the details. Once the palette's
  class C lands, the palette gains `/update` (argv shown, `y` runs `agentbus update`). There is
  never a pop-up or a bell.
- **Update:** `agentbus update`, or the scheduled updater when `updates.auto` is `install`.
  1. It records a snapshot.
  2. It builds the new release beside the current one.
  3. It swaps the commands, then restarts the project services one at a time, each checked by
     health.
  4. Agents keep running. They move to the new build only when they are next relaunched.
  5. If any check fails, the update rolls itself back and says why in For you.
- **Rollback:** `agentbus update --rollback` restores the last snapshot. `--list` shows the
  snapshots, and `--rollback <id>` restores an older one.

## 4. Design

### 4.1 Where an update comes from

- **The managed clone** is the one `install.sh` made. Only it is moved to a release tag. The person
  never edits it.
- **A developer checkout** (the dogfood repository, any clone the person works in) is never moved
  by `agentbus update`. There, `agentbus update` only notifies, and `./setup.sh update` keeps its
  developer behaviour of pulling the branch. The installed binaries record which kind they came
  from (`buildinfo.Repo`, already stamped), so `update` knows.
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
| **settings** | `config.toml`, each project's env file, `notify.toml`, the local `roles.toml`, the installed role skills (one directory each); each with its SHA-256 | the paths `config` and `setup` already know |
| **service units** | each unit file (dispatch, missions, holder per build) with its enabled and active state, per project | `service list`, the unit directory (systemd) or LaunchAgents (launchd) |
| **team** | per project: the roster from `{p}:agents` (role, profile, client, holder build), the `{p}:resume` records (`{session, profile, at}`), pause and suspend state, and the current holder build | the broker, read only |
| **broker** | a `agentbus backup` archive of the record keys of every project, made by the existing backup code; secrets left out as backup does | `backup` (2026-11-01 spec) |
| **manifest** | `manifest.json`: the parts, their hashes, the from and to tags, and whether the release declares a **broker migration** (§4.6) | written last; a snapshot with no manifest is incomplete and never restored |

Retention: the last `updates.keep` snapshots (default 3). An older one is removed only after a newer
update has succeeded.

### 4.3 The update, step by step

1. **Check.** Find the release (§4.1). Already there: done. Developer checkout: notify only.
2. **Snapshot** (§4.2).
3. **Stage.**
   - Check out the tag in a scratch worktree of the managed clone, and build the stamped binaries
     into `<datadir>/agentbus/builds/<buildkey>/`.
   - Run the new `agentbus version` and `busmon --version`.
   - Any failure: nothing has changed. The snapshot is dropped, and For you says why.
4. **Swap the commands.**
   - Move the managed clone to the tag, and install the new binaries with `InstallFile`, which
     copies beside the target and then renames (verified: `setup/build.go`). A running agent keeps
     the file it started; the next call runs the new one.
   - Install the role skills.
   - `config.toml` and the env files are touched only by a migration the release declares, and
     that is recorded in the manifest.
5. **Services,** one project at a time and one service at a time (dispatch, then missions).
   - `service restart`, then wait for `agentbus health` to show that service green within
     `updates.health_wait` (default 60 s).
   - The dispatcher is fenced by its lease and reads from the stream, so agents are not touched.
     To verify by test: a wake for an entry written during the restart is delivered after it.
   - A red check is an automatic rollback (§4.5) of everything done so far, reported in For you.
6. **Holder: never restarted** (herdr exit §4.4, §4.4a).
   - The new build's unit is written and started beside the old one, and becomes the current build.
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
It never installs outside the window. A manual `agentbus update` ignores the window: the person
chose the moment. It still refuses while any agent has background work, unless the person passes
`--now`.

### 4.5 Rollback

`agentbus update --rollback [<id>] [--dry-run]` restores a snapshot, step by step. It never stops a
running agent.

1. **Builds:** the snapshot's binaries go back in place with `InstallFile` (copy beside, then
   rename).
2. **Settings:** each file goes back **only if it still has the hash the update left**. A file the
   person changed after the update is kept as it is, and listed with the path of the snapshot copy.
   The person decides; nothing is overwritten silently. Skills go back the same way.
3. **Units:**
   - The dispatch and missions units are rewritten from the snapshot, then restarted one at a time
     with the same health check.
   - The snapshot's holder build becomes current again. Its unit is started if it is not active,
     using the build copy in the snapshot if its binary directory was removed.
   - The newer build then drains, by the same rule as an upgrade.
4. **Team:** every role in the snapshot's roster that is **not running now** is relaunched on its
   profile with its resume session, one at a time, through the existing relaunch path. A running
   agent is never touched. A role whose resume record no longer validates follows the herdr-exit
   rule: a fresh start with a notice for `up`, and no Start for self-repair.
5. **Broker:**
   - **Untouched** unless the manifest says the release migrated it (§4.6). Messages written since
     the update stay.
   - With a migration, rollback restores the record keys from the snapshot's archive through
     `agentbus restore`. That needs `--force` and the person's `y`, because it drops what was
     written since the update; the confirmation names the time span.
6. **Record** the rollback in the history, and report to master and the person.

An automatic rollback after a red check (§4.3 step 5) is the same procedure, limited to what the
update had already done.

### 4.6 Broker migrations

- A release that changes the broker's record format says so in its release notes **and** in a file
  the updater reads: `docs/releases/<tag>.migrate`, listing the migration, its version, and whether
  rollback needs the archive.
- With no such file, the update never writes to the broker beyond what the new services write in
  normal running.
- The subscribe JSON's additive-only rule (`"v"`) already keeps most releases migration-free.

### 4.7 The scheduled updater

- **A host unit, `agentbus-update`:**
  - systemd: a user timer plus a service, hourly, `agentbus update --auto`;
  - launchd: `StartCalendarInterval`.
  - It is written only when `updates.auto` is `stage` or `install`. With `notify` or `off` there is
    no unit, and busmon's own daily check is the notice.
- **It is not the dispatcher.** The dispatcher must never run repository code (CLAUDE.md), and an
  update builds code.
- **`stage`** does steps 1 to 3 and stops. It tells the person a build is ready, and
  `agentbus update` then installs it without rebuilding.

### 4.8 Settings (in `config/keys.go`)

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
  `systemctl`/`launchctl` (as the service tests do), and a disposable Redis only.
- **Snapshot:**
  - every part is written and its hash is in the manifest;
  - one part failing to write stops the update with nothing changed.
- **Two updates in a row:** v1 to v2 to v3. Then:
  - v1's and v2's holder builds drain while v3 serves;
  - a rollback goes to v2, with v2 current and v3 draining.
- **Automatic rollback:** a service that stays red after its restart rolls everything back, and
  the binaries are v1's again.
- **Settings on rollback:** a file the person changed after the update is kept and listed; an
  unchanged one is restored.
- **Team on rollback:** a role that died during the update is relaunched with its resume session.
  A running agent is not touched; the test checks that no Start or Close was sent for it.
- **Broker:** with no `.migrate` file, rollback leaves the keyspace as it is. With one, it refuses
  without `--force`.
- **Window:**
  - outside the window, nothing is installed;
  - a busy agent, or one with background work, defers;
  - after `max_wait` there is a For you item.
- **Developer checkout:** `agentbus update` notifies only, and the checkout's HEAD is unchanged.
- **Atomic swap:** a reader process that runs `agentbus version` in a loop during the swap never
  fails.

## 6. Out of scope

- Windows (the person, 2026-11-11).
- Downgrading the Redis or Valkey image. The broker container is the person's or `setup`'s, and an
  image change stays a manual step with its own notes.
- Remote or fleet updates, and signed releases: no signing beyond what git and GitHub give.
- Updating the AI clients themselves (Claude Code, Codex…): sentinel's `update` proposal already
  covers that.

## 7. Open questions

1. **Release notes in For you:** should an available update show its release notes' first lines, or
   only the version? *Recommended:* only the version, with the notes one key away in About.
2. **The agent automaton.** The person keeps the idea of an "agent automaton": quick, robust,
   reliable, unbreakable. The person gave no further detail, so this spec gives it no scope. It
   stays an open question for the person to describe: what it automates, what triggers it, and
   whether it relates to updates at all.
