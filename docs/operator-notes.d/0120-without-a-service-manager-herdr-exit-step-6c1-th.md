- **Without a service manager (herdr exit step 6c1; the `none` manager is 6c2, not wired yet).**
  `services.manager` (`auto`, `systemd`, `launchd`, `none`; `MachineOnly`) is the registry key of the
  old `AGENTBUS_SERVICE_MANAGER`: `service.ManagerChoice` reads the environment, then config.toml,
  and `Detect`, `Local`, `ManagerBin` and `restore` go through it (today `none` still means herdr
  tabs). `service.Supervisor` is the units' policy for a supervisor process: restart after 2 s on a
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
