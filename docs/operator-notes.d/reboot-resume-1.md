- **Reboot-resume, part 1: the run record (reboot-resume spec §2, `lifecycle/`).** Nothing is
  judged yet, so behaviour is unchanged. Each project's state directory now holds two local
  files, written 0600 and replaced atomically (a temp file, fsync, rename, then fsync of the
  directory):
  - `run.json` (`{"boot","state","cause","at"}`):
    - the dispatch service writes `running` at its first lease, and `stopped` (cause `signal`) when
      SIGTERM or SIGINT ends it, but only over `running` of this boot;
    - `agentbus up` writes `running` before it starts the services;
    - `agentbus down` writes `stopped` (`down`) before it stops them;
    - `agentbus suspend` writes `stopped` (`suspend`) after its pause record and before the stop,
      and puts `running` back when the suspend is cancelled.
  - `unplanned.json` is the automatic-restart counter, `[]` for now. It is created before the first
    `run.json`. The dispatch service never recreates a lost one; `agentbus up` does.
  The boot id is `dispatch.BootID` (Linux `boot_id`, macOS `kern.bootsessionuuid`). Where it is
  unknown (the BSDs), nothing is written. A failed write is said and never stops the command.
