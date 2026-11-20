- **After a reboot, the dispatch service judges once (reboot-resume A, with holder hardening H1 and H1b).**
  - **The judgement:** once it holds the lease, before any other loop (`dispatch.Config.Reboot`,
    `internal/dispatchsvc/reboot.go`), the dispatch service reads `run.json` (`lifecycle.Judge`) and records
    the verdict in `{p}:reboot` (fenced, at from Redis TIME). Only then does it write `running`, so a
    stop in between judges again. The verdicts are `intentional`, `suspended`, `restarting`,
    `unplanned` (with `why`) and `stopped` (with `why`).
  - **An unplanned stop** restarts the team only when, in order:
    - `services.unplanned_restart` is on (default on);
    - self-repair is `on`;
    - the holder answers within 2 minutes of the dispatcher's start (H1b: polled every 2 s;
      nothing written, counted or relaunched meanwhile; never for `intentional` or `suspended`);
    - the repository is intact (`repoIntegrity`: no `index.lock` in the git directory or a
      worktree's, `git fsck` exits 0 within 5 min, run with `core.fsmonitor=false` and
      `core.hooksPath=/dev/null`; a directory with no `.git` passes);
    - the count, kept in `unplanned.json` and written before anything is relaunched, stays within
      `services.unplanned_max` (3) in `services.unplanned_window` (24 h).
  - **A failed check or the limit:** the project is stopped by a fenced suspend (`by` dispatch;
    the services keep running). A lost or garbled counter reads `unplanned` (`counter missing`,
    `counter not written`), never empty.
  - **Who is told:** the person, once per boot (mark `reboot`); for a restart, master too.
  - **Self-repair** holds every agent (`HoldReboot`) while this boot's record stands with any verdict
    but `restarting`. It relaunches a reboot death as `BeginRebootRelaunch` (an `r`-prefixed attempt:
    the project's pause, never the role's own budget), and deletes the record once every required
    agent is alive (another boot's at once).
  - **Resume:** `agentbus resume` empties the counter.
  - **H1:** with `services.start_at_login` on, `up` and `service login` enable the current build's
    holder with the other services and disable every other build's (`service.HolderLoginStart`).
  - **Not in A:** busmon's popup (B), `services.start_at_boot` (C), and the retirement of
    `suspend.auto_resume` (with B, which replaces it).
