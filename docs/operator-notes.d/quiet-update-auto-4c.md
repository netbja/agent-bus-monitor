- **The automatic update tells master, step 4c** (quiet-update-auto §4.6): each `agentbus update --auto`
  pass sends one report from `updater` to each touched project's escalation target (the project's
  `AGENT_BUS_ESCALATE_TO`, master by default), each kind once per tag or snapshot (`auto.json`'s
  `told`): an install without a wake, to the projects restarted (from, to, the services restarted and
  those left on the old build, the skills changed with their `skill-users` command, the undo); with a
  wake, to every project with services or a holder here, a rollback (the tag is not retried), a failed
  rollback (the manual commands), an interrupted update (`agentbus update --rollback <id>`), and a
  release waiting longer than `updates.max_wait` with the reason of the last pass (`auto.json`'s
  `last_pass.why`: the project and agent in work, or no pass since it was found); without a wake, a
  release that needs a migration. The check pass tells the long wait and the migration too, only with
  `updates.auto = install`. A broker that cannot be reached loses that notice (said on stderr), never
  the update; the history line and `auto.json` keep it. A failure before the snapshot (not staged)
  sends no notice: nothing changed, and the next pass tries again.
