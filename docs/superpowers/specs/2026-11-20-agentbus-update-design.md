# `agentbus update`, step 1: a manual update to the latest release, with a snapshot to roll back to

Status: r4, the person's answers recorded (§9, 2026-11-20); step 1 built from it. Base: origin/dev `b2ccf229`. Author: architect, from the person's decision of
2026-11-20 (bus thread `1795182404503-0`, relayed by master as cmd `1795182448227-0`).

## 1. The decision

The person split the update work in two:
- **Step 1, now (this spec):** a manual terminal command, `agentbus update`. It fetches the latest
  release tag, takes a snapshot, replaces the binaries and restarts the services. When busmon
  shows `↑ vX.Y.Z`, it only shows the command to run. It never runs it.
- **Later, in the quiet-update spec** ([2026-11-14](2026-11-14-quiet-update-design.md)): the
  automatic mode, the install window and the `updater` service that replaces cron.
- **The rule stays:** nothing is reinstalled without the person's yes.

Today the only path is `./setup.sh update` (`setup/update.go`). It pulls the clone's branch rather
than a release, reinstalls, and then offers to restart every project's stale services
(`RestartStale`, `setup/services.go:147`). It has no way back.

## 2. From the person's side

```
$ agentbus --project agent-bus-monitor update
agentbus v0.31.0 -> v0.32.0  (release notes: ~/.local/share/agentbus/src/v0.32.0/docs/releases/v0.32.0.md)
  build from   ~/.local/share/agentbus/src/v0.32.0  (your clone is not touched)
  snapshot     ~/.local/state/agentbus/updates/20261120-1415-v0.31.0-v0.32.0
  restart      agent-bus-monitor dispatch, agent-bus-monitor missions
  not touched  demo dispatch (v0.31.0): agentbus --project demo service restart dispatch
  skills changed: agent-bus  (agents to tell: agentbus roles skill-users agent-bus)
Install v0.32.0? [y/N]
```

- **`agentbus update`**: finds the release, shows the plan above, and asks once.
- **`--yes`** answers that question. It is the person's flag. An agent caller is refused with or
  without it (§5).
- **`--check`** stops after the plan and changes nothing. busmon may show its output.
- **`agentbus update --rollback [<id>]`** restores a snapshot: by default the last update that
  succeeded and is not rolled back yet, or an unfinished one (killed after its snapshot), which is
  the one step 0 names. A failed update rolled itself back already.
  **`--list`** lists the snapshots.
- **`--project <p>`** names the project whose services are restarted. Without it, nothing is
  restarted, and every stale service is listed with its command. It is read from the command line
  only, as `backup` reads it: an `AGENT_BUS_PROJECT` exported in the shell never restarts a project.

## 3. Design

### 3.1 Where the release comes from

- **The repository** is the one the running binary was built from: `buildinfo.Repo`, through
  `git rev-parse --git-common-dir`, the same read as `internal/dispatchsvc/repocheck.go:56`.
  - An empty `Repo` (a `go install` build) is refused, naming `./setup.sh update`.
  - A `Repo` that is gone is refused the same way.
- **The fetch:** `git fetch --tags origin` in that repository. It moves remote refs and tags
  only, never a working tree or a branch.
- **The release** is the newest `vX.Y.Z` tag that is an ancestor of `origin/main`. The test is
  `mission.Git.IsAncestor` (`mission/git.go:87`), the same rule as quiet-update §4.1. A stray tag
  on another branch is never installed.
- **Updating is forward only.** The running version is `buildinfo.Version`, which setup stamps
  from `git describe` (`BuildVersion`, `setup/build.go`).
  - Up to date (the tag is not `buildinfo.Newer` than the version): it says so and stops.
  - A version that is not a plain tag:
    - `vX.Y.Z-N-g<sha>`: allowed only when `<sha>` is an ancestor of the target tag, so no commit
      of the running build is lost.
    - `dev`, or `-dirty`: refused, naming `./setup.sh update`.
  - Going back is `--rollback`, never `update`.

### 3.2 One tree per release; the clone is not touched

- **Each tree is a worktree of the person's clone** (the person's answer 1). Its history stays in the
  clone's `.git`, so every later update depends on that clone: the binaries record the tree, and the
  tree points back to the clone.
  - **If the clone was moved or deleted**, the tree's `.git` points to nothing. The update then
    refuses and says it plainly: the tree is a worktree of the clone at `<path>`, later updates need
    that clone's `.git`, put the clone back there or update with `./setup.sh update` from the clone.

- **The tree:** the tag is checked out as its own detached worktree,
  `${XDG_DATA_HOME:-~/.local/share}/agentbus/src/<tag>`, with `mission.Git.AddWorktree`
  (`mission/git.go:114`). This is quiet-update §4.1's layout, so the later automatic mode finds
  the same trees.
- **Why not check the tag out in the clone:** binaries read their tree while they run
  (quiet-update §4.1): `scripts/bootstrap`, `roles.toml` through `rolecfg.ManifestPath`, and About.
  Moving the person's clone would change what running agents read, and it would discard a
  developer's branch.
- **A binary and its tree switch together.** The new binaries are stamped with the new tree as
  `buildinfo.Repo`. From then on they read that tree's `roles.toml` and scripts, not the clone's
  (question 1).
- **A tree that already exists** must be clean (`mission.Git.Dirty`) and at the tag
  (`mission.Git.Head`). Otherwise the update refuses and names the tree.
- **Step 1 never removes a tree.** Removing them is quiet-update's lifetime rule (§4.1), for later.

### 3.3 The snapshot

It is written **before any change**, under `<state>/agentbus/updates/<id>/`:
- `<state>` is setup's `state()`: `XDG_STATE_HOME`, else `~/.local/state`, on Linux and macOS alike.
- `<id>` is the host's local time, then the from version, then the to version.

| Part | What | From |
|---|---|---|
| `bin/agentbus`, `bin/busmon` | copies of the bin dir's two binaries, mode and times kept | setup's `backup` copy (`setup/services.go`) |
| `manifest.json` | format version; from version and build key (`service.BuildKey`); to tag; bin dir; from tree and its `HEAD`; the `agent-launch` link's target; the external skills clone's path, `HEAD` and whether setup made it (its marker), or `absent` when there is none yet; every symlink in each skill directory the new tree's roles use (`skillDestination`, `rolecfg/skills.go:106`), name and target; the project; the services to restart, with their build before | read in §3.4 step 1 |

- **The manifest is written last,** with `files.WriteAtomic` (`internal/files/atomic.go`). A
  snapshot without one is incomplete and is never restored.
- **If any part cannot be written,** the update stops with nothing changed.
- **What it does not hold:** the broker, `config.toml`, the env files or `notify.toml`. Step 1
  never writes them (§4).
- **One contract across versions:** `<snapshot>/bin/agentbus` and its `update --restore <id>`. A
  rollback runs the binary that wrote the snapshot, so a newer binary never has to read an older
  manifest (§3.5).
- **Retention:** the last 3 snapshots. An older one is removed only after an update succeeds
  (question 5).

### 3.4 The update, step by step

The running binary does every step, and it is the **from** version. Its own file is replaced in
step 5, but its process keeps running from the old inode. So the same process that changed things
can also undo them.

0. **Guards:**
   - The agent refusal (§5).
   - One update at a time: a non-blocking `flock` on `<state>/agentbus/updates/lock`, as
     `internal/dispatchsvc/redislatest.go:126` takes its own.
   - **An interrupted update:** if the last snapshot has no history line, the command refuses
     and names `agentbus update --rollback <id>`.
1. **Find** (§3.1), and read what the manifest needs.
   - The services: the stale list of `service stale` (`staleServices`,
     `cmd/agentbus/servicestale.go`), split into this project's and the others'.
   - Only `dispatch` and `missions` are counted. Holders are not units there, and are never listed.
2. **Plan and yes** (§2):
   - **Release notes:** `docs/releases/<tag>.md`, if the tag has it.
   - **Skills changed:** `setup/update.go`'s skills diff (`git diff --name-only <from> <tag> --
     skills/`) and its `agentbus roles skill-users` hint, reused as is.
   - **The question:** setup's `Ask` with `Prompter` and `OpenTTY` (`setup/ask.go`), default no.
     - With no terminal and no `--yes`, the update refuses. It never installs unattended,
       which is `RestartStale`'s rule.
   - `--check` stops here.
3. **Stage:** the tree (§3.2), then both builds into the temp dir. Then run the new
   `agentbus version` and `busmon --version`.
   - **One change to setup:** `BuildAndInstall` (`setup/build.go`) builds and installs in one call.
     It is split into a build into `in.Tmp` and an install by `InstallFile`. `setup install` calls
     the two in order, so its behaviour is unchanged.
   - The build is stamped with `Ldflags(tag, Tree(rev), <tree>, <bindir>)`, with `in.Repo` set to
     the tree.
   - **A failure** changes nothing outside the tree, and the tree stays for the next try.
4. **Snapshot** (§3.3). A failure here changes nothing.
5. **Swap.** From here on, any failure is an automatic rollback (§3.5).
   - **Binaries:** `agentbus` first, then `busmon`, each by `InstallFile` (copy beside, then
     rename), into the bin dir and the tree. This is quiet-update §4.3 step 4's order and reason:
     an old busmon runs its commands through the `agentbus` beside it (`agentbusBeside`).
   - **`agent-launch`:** when the link is this setup's link to the from tree
     (`OurScriptLink(target, fromTree)`), it is replaced by a link to the new tree, by a symlink
     beside it and then a rename. Any other link is left alone, as `RoleScriptLinks` leaves it
     today.
   - **Skills,** run from the new tree with the new binary, and **strict**.
     - Today `ExternalSkills` and `RoleSkills` (`setup/skills.go:41-111`) warn and return on a
       failed fetch, checkout or link. `setup install` keeps that.
     - The update needs forms of the two that return their error. Install calls them and keeps
       its warning, so its behaviour is unchanged.
     - **A check follows:** the external clone's `HEAD` is the pin's commit, and every role skill
       link resolves to the source rolecfg's `skillSources` gives it with the new tree as the
       repository root:
       - the new tree's `skills/` for the repository's skills;
       - the external clone for the others (`skillRoots`).
     - An error or a failed check is a failure of step 5, so it rolls the update back.
     - A skills folder setup did not make (no marker) is the person's. It is left alone and named,
       as install does, and that is not a failure.
6. **Services** (only with `--project <p>`): each of `<p>`'s stale `dispatch` and `missions`, one
   at a time.
   - The restart is `agentbus --project <p> service restart <kind>` with the new binary. That is
     `Stop`, a budget reset, then `Start` (`cmd/agentbus/servicecmd.go:285`). Unit files are not
     rewritten.
   - **Green** means, within 60 s: the unit is active, its record was written by the unit's own PID,
     and the new binary's `service stale` no longer lists it (it recorded the new build). `service
     stale` alone is not enough: it drops an inactive unit, so a service that crashed, exited or
     went `failed` after its restart would read as green.
   - A service that is not green rolls the update back.
   - **Holders are never restarted.** The restarted dispatcher installs and starts its own build's
     holder through the existing path (`ensureOwnHolder`, `cmd/agentbus/up.go:1297`). New
     terminals open there, and the old holder drains as its agents end (herdr exit §4.4a).
7. **Tell:**
   - one summary line;
   - the skills hint;
   - the other projects' stale services, each with its command;
   - a line in `<state>/agentbus/updates/history`: from, to, at, ok, snapshot id.
8. **Retention** (§3.3).

### 3.5 Failure and rollback

| When it fails | What happens |
|---|---|
| Steps 0 to 4 | Nothing outside the tree has changed. It says why, and exits 1. |
| Steps 5 to 7 (including a red service, and a history line that cannot be written: unrecorded, the snapshot would read as an interrupted update) | **Automatic rollback by the same process,** then it says why. The history line has `ok=false`. |
| The rollback itself fails | It stops and prints the exact manual commands from the manifest: `cp` of each snapshot binary, `ln -sfn` of the `agent-launch` link and of each recorded skill link, the skills clone's `git checkout`, each `service restart`. |
| The process is killed | The lock goes with it. The snapshot has no history line, so the next `agentbus update` refuses and names `--rollback <id>` (step 0). |

**The rollback** (automatic, or `--restore`):
1. The binaries come back from the snapshot, `busmon` first and then `agentbus`, by `InstallFile`.
   This is the reverse of the update's order, for the same reason.
2. The `agent-launch` link goes back to its recorded target.
3. The skills go back, link by link. Re-running `roles install` would not be enough:
   `rolecfg.InstallSkills` creates and replaces links, but never removes one.
   - Each recorded link is set back to its recorded target.
   - A link that was not recorded, and points into the new tree, is removed: the release added it.
   - Any other link is the person's, and is left alone.
   - The external clone gets a `git checkout` of its recorded `HEAD`, only when setup made it.
     - Recorded `absent`: the clone the update made (setup's marker) is removed.
   - Then the same check as step 5, against the snapshot's links and `HEAD`.
4. The services the update restarted are restarted again, and each must report the from build key
   (the same 60 s test).
5. Never touched:
   - holders: a build's holder started meanwhile keeps its agents and drains;
   - agents;
   - the trees;
   - the broker.

**`agentbus update --rollback [<id>]`:**
- It asks the same yes (§2) and applies the same agent refusal.
- Then it runs `<snapshot>/bin/agentbus update --restore <id>`. The binary that wrote the
  manifest is the one that reads it.
- **It holds the update lock across the whole sequence,** so no update runs between two restores.
  Each restore it starts carries `AGENTBUS_UPDATE_LOCK_HELD=1` and runs under that lock; with the
  marker and a free lock, a restore refuses. A `--restore` run on its own takes the lock itself.
- `--restore` runs the steps above.
- Rolling back to an older snapshot than the last one undoes each later update in turn, newest
  first. It stops at the first that fails.

### 3.6 busmon

Built after step 1's PR: the About row below sits in busmon's files, which the views redesign holds
at the time of the build.

- **busmon never runs it.** The header's `↑ vX.Y.Z` stays as it is (`cmd/busmon/update.go`).
- **About's `updates` row** (`updateRow`, `cmd/busmon/about.go:236`) shows the exact command,
  `agentbus --project <p> update`, while a newer release is known.
- **Copying the command** uses busmon's existing `y` copy (`CopyText`, `cmd/busmon/cells.go`), not
  a new helper.
- busmon gains no exec path.

## 4. What it must never do

- **Touch another project's units.** It restarts only `--project <p>`'s `dispatch` and `missions`.
  The other projects' stale services are listed with their own command, never restarted.
  - The binaries are per host, so the other projects' next `agentbus` call runs the new build. That
    is today's state after `./setup.sh update` too.
- **Run under an agent.** Updating agentbus is the person's act (§5).
- **Install without the person's yes,** or unattended.
- **Restart a holder, or stop or relaunch an agent.**
- **Touch the person's clone,** beyond `git fetch` and a worktree's entry under its `.git`.
- **Install a tag that is not on `origin/main`, or an older one.**
- **Write to the broker, `config.toml`, an env file or `notify.toml`.**
  - A release that needs a broker migration or new settings is installed with `./setup.sh` and its
    notes. Step 1 has no migration step.
- **Rebuild the broker's container or change its image.** That stays setup's job, and its notes'.

## 5. Who may run it

- **The rule** is the one `agentbus resume` and busmon's `--delete` apply:
  `agentcaller.Refuse(ctx, getenv, ancestor, "updating agentbus", "agentbus update")`
  (`internal/agentcaller/agentcaller.go:73`).
  - It refuses when `AGENT_BUS_AGENT` is set, or when a registered agent is among the caller's
    ancestors (`Ancestor`).
  - It also refuses when the ancestry cannot be read, except on a host that has none.
- **What it applies to:** `update` (with or without `--yes`), `--rollback` and `--restore`.
- **`--check` is read-only** and is not refused: an agent may report what an update would do.
- **Defence in depth, not a boundary.** The package says so itself: the bus is no boundary
  against the local user.

## 6. macOS

- **Services:** under launchd, `service restart` is the same command (`Launchd.Stop` and `Start`).
  No plist is rewritten.
- **Linux without systemd:** under `services.manager = none`, `service restart` stops the old
  supervisor and starts a new one, again the same command.
- **The binary swap is a rename,** so the new file is a new inode.
  - A running process keeps its old file.
  - The kernel's code-signature check never sees a signed binary rewritten in place. On macOS,
    rewriting one in place can get the next run killed.
  - `InstallFile` already renames, on both systems.
- **The ad-hoc signature** the Go linker gives arm64 binaries is inside the file. So the snapshot
  copy and the restore keep it. This must be checked on a Mac before the first release (§7).
- **The agent refusal works there:** `dispatch.Ancestors` reads the macOS process table
  (`dispatch/ident_darwin.go`).
- **Paths:** `state()`'s `~/.local/state` and `~/.local/share/agentbus/src` are the same paths as
  on Linux. `flock` exists on both.

## 7. Testing

- **The seam:** `agentbus update` with a temporary git repository holding tags on `main` and a
  stray tag on another branch, a fake `go build` through setup's `Runner`, and a fake service
  manager (as `service` tests fake `systemctl` and `launchctl`).
- **Release choice:** newest tag on `main`; the stray tag is never chosen; an older tag is never
  chosen; `-N-g<sha>` is allowed only as an ancestor; `dev` and `-dirty` are refused.
- **The clone is untouched:** its `HEAD`, branch and status are the same after an update.
- **Order:** the snapshot (with its manifest) exists before the first write to the bin dir.
- **Skills failures roll back:** a failed external fetch or checkout, a failed link, and a link
  that does not resolve to its `skillSources` source each roll back, with history `ok=false`.
- **The check passes** on a normal update with both kinds of skill: one from the repository, one
  external.
- **A release that adds a skill** leaves no link to it after a rollback.
- **Each failure point** (build, snapshot, each binary, link, skills, each service) leaves the
  bin dir, link and skills byte-equal to the snapshot after the automatic rollback.
- **A kill between the two binary renames,** then `--rollback`, restores both.
- **Scope:** with `--project a`, project `b`'s units get no `Stop` or `Start`, and are listed.
  No holder is ever stopped or started.
- **Refusals:** `AGENT_BUS_AGENT` set, and a registered agent among the ancestors, refuse
  `update`, `--rollback` and `--restore`. `--check` passes. No terminal and no `--yes` refuses.
- **The lock:** a second update refuses while the first holds it.
- **macOS:** a manual check on a Mac that an updated, then rolled-back `agentbus` still runs
  (signature).

## 8. Out of scope (later, in the quiet-update spec)

- the automatic mode (`updates.auto`), the window and the `updater` service;
- removing old trees and holder builds;
- broker migrations;
- a managed clone made by `install.sh`;
- busmon running the update;
- Windows.

## 9. The person's answers (2026-11-20)

1. **The build tree:** yes, one tree per release, the clone left alone. Each tree is a worktree of the
   clone, so later updates depend on the clone's `.git`; the refusal explains it when the clone was
   moved or deleted (§3.2).
2. **Services:** restart only `--project <p>`'s `dispatch` and `missions`, and list the other
   projects' stale services with their commands.
3. **A build that is not a release:** refuse, and point to `./setup.sh update`.
4. **Telling master:** not in step 1. A bus message is a broker write, which §4 forbids: the terminal
   output and the history line are enough. A notice to master, the skills hint included, comes with
   quiet-update.
5. **Snapshots kept:** 3.

The macOS signature check (§6, §7) stays a blocker before the first release that ships this command.

## Changelog

- **r2** (reviewer-codex on r1): the skill steps are strict in an update. The forms of
  `ExternalSkills` and `RoleSkills` it uses return their error, followed by a check, so a failure
  rolls back. A rollback restores each recorded skill link and removes the links the release
  added, because `InstallSkills` never removes one (reviewer-codex and foureyes on r1).
- **r2** (foureyes on r1): an external skills clone that is absent before the update is recorded
  `absent`, and the rollback removes the clone the update made. Question 1 says the skill links
  follow the tree too.
- **r3** (foureyes on r2): the strict check compares each link with its `skillSources` source,
  so an external skill, which links into the mattpocock clone, passes.
- **r4** (the person's answers, relayed by master on 2026-11-20): §9 answered; §3.2 says each tree is a
  worktree of the clone and how the refusal explains a moved or deleted clone; `--project` from the
  command line only (§2); busmon's About row after step 1's PR (§3.6).
- **r5** (reviewer-codex on the step 1 build): `--rollback` holds the lock across every restore it runs
  (§3.5); a history line that cannot be written after a success rolls the update back (§3.5's table).
- **r6** (foureyes on the step 1 build): green is an active unit with its own PID's record of the new
  build, not only "not stale" (§3.4 step 6); `--rollback` defaults to the last installed update (§2).
- **r7** (reviewer-codex and foureyes on r6's build): `--rollback` also takes the unfinished snapshot
  step 0 names, so an update killed between the binary renames is recoverable by the named command.
