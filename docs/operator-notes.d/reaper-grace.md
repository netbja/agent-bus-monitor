- **The holder's reaper waits 2 seconds before taking an exited child it did not start.** It
  reaps the orphans a holder inherits as a subreaper. Taking any zombie at first sight raced the
  process's own commands (os/exec), whose `Wait` then failed with `waitid: no child processes`.
  In the test suite, where the release test runs a holder in the same process, that was the
  intermittent failure of git-running tests (`TestPlanDriftRealGit`, `TestMissionCLIRoundTrip`
  and others). An orphan is now reaped once it has stayed a zombie for the grace, keyed by pid
  and start time; a sweep runs every second as well as on each SIGCHLD.
