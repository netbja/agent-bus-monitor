- **Without a service manager (herdr exit steps 6c1, 6c2 and 6c3: detached supervisors).**
  `services.manager` (`auto`, `systemd`, `launchd`, `none`; `MachineOnly`) is the registry key of the
  old `AGENTBUS_SERVICE_MANAGER`: `service.ManagerChoice` reads the environment, then config.toml,
  and `Detect`, `Local`, `ManagerBin` and `restore` go through it (since 6c3 `none` means detached
  supervisors, below). `service.Supervisor` is the units' policy for a supervisor process: restart after 2 s on a
  non-zero exit, at most 5 starts in 300 s (judged when the next start really happens, after the
  wait: a wait that overran may have freed the window; reviewer-codex), an exit 0 is final, a stop sends the service SIGTERM; `AfterExit` runs after each exit.
  A holder with `Config.Pgids` set rewrites `<rundir>/agentbus/<p>/holder-<key>.pgids`
  (`PgidsPath`, 0600, atomic) with each live terminal's leader pid and start time at every start
  and end; `KillRecordedGroups` takes one cutoff (`NowTicks`, captured by the caller when the holder's exit
  is seen, the same for every group, and short of the tick being read, so nothing born after the
  capture is reached; a survivor started in the last 10-20 ms before the death is missed;
  reviewer-codex) and hands each line to `KillGroup`, which never signals the group number (after a group empties its number may be
  reused; reviewer-codex): it kills, each pinned by a pidfd and re-read through /proc after the
  pin, the processes of that group that started between the recorded leader and the cutoff, so the
  survivors of a gone leader die (foureyes) while a group made after a reuse is spared, and touches
  nothing when a live process holds the leader's pid with another start time. pidfd needs Linux
  5.3: ENOSYS ends the kill with an error; a member that refuses (EPERM, a root child of `sudo`)
  is recorded and skipped, the others still killed (foureyes), and the refusal returned; on any
  error `KillRecordedGroups` keeps the record, the only trace of what is left (reviewer-codex). Linux only
  (`groupkill_linux.go`): without /proc there is no start time, the holder records nothing and
  nothing is killed. It reads the record only from a checked run directory and never through a symlink
  (`O_NOFOLLOW`, regular, ours, private), and removes it.
  6c2: `holder.Supervised` is the holder's `service.HolderManager` without a manager. Its "unit" is a
  record, `<rundir>/agentbus/<p>/holder-<key>.pid` (0600, read like the group record): `InstallHolder`
  writes project, build key, raw id, env file, working directory and binary copy (another raw id for a
  key is refused, S7); `StartHolder`, under a `flock` on `holder-<key>.lock` (two `up` start one; the lock file is never
  removed, even by `RemoveHolder`, so every waiter shares one inode: reviewer-codex),
  spawns `<binary copy> --project <p> service supervise holder <p> <key>` in its own session (`setsid`),
  output appended to `<state>/agentbus/logs/<p>-holder.log`, and records its pid and start time. A
  supervisor is alive only while that pid holds that start time (a reused pid, a zombie: gone);
  `StopHolder` sends SIGTERM pinned by a pidfd and waits (15 s); `RemoveHolder` refuses a live one, so
  `service.GCHolders` works on it unchanged. Linux only for now: elsewhere `ProcStart` must be given
  (no start time, nothing judged). `agentbus service supervise` (`supervisecmd.go`) runs
  `service.Supervisor` on `holder --env <file> --record-groups` (the holder then sets `Config.Pgids`)
  and, after each exit, `KillRecordedGroups` with the cutoff taken first and the run directory the
  holder's env file gives it; it exits 1 when it gave up.
  6c3 wires it: `holder.Supervised` is also a whole `service.Manager` (`Name` `supervisor`), which
  package `holder` registers as `service.Unmanaged` at init (`service` cannot import `holder`; a binary
  that does not link `holder` keeps the old answer, no manager). `Detect` returns it for
  `services.manager = none`, and for `auto` on a system with neither: a Linux not booted with systemd
  (`/run/systemd/system` absent) or another system. A systemd that runs but whose user instance does not
  answer (an ssh session without its bus) stays an error: never a second dispatcher beside the units.
  `up` then installs dispatch and missions as records, one per project like their units
  (`<rundir>/agentbus/<p>/<kind>.pid`, same checks and lock, one `arg` line per argument), and `Start`
  spawns `<exe> --project <p> service supervise <kind> <p>`, which runs the unit's own command
  (`Spec.Argv`, never `--stderr`) under the same policy, killing no group. `down`, `service`,
  `suspend`, `restore` and the health checks reach it through `Detect`; `--tabs` still forces herdr tabs.
  `service.Detached` (`Limits`) states what does not exist: `services.start_at_login` is refused with it
  (`LoginStartOf`), and `service status` prints it with each service's supervisor log, which `service
  logs` shows too. `service supervise` calls `holder.UnignoreTermSignals` first (spec revision 26): under
  `nohup`, nothing below inherits an ignored SIGHUP (`TestSuperviseUnderNohup`). busmon does not link
  `holder` yet (see the PR).
