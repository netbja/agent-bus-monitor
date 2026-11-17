- **Before a release, run the holder's live check: `agentbus check holder-restart`** (or
  `scripts/holder-live-check.sh`). It brings up a disposable project of its own (`lc` + 6 hex:
  master and coder, never your team), kills its holder with SIGKILL, and times the restart, the
  relaunches (master first, one every 2 minutes, each on its session), the wakes and health
  `holder`; then it removes the project, also on a failure or Ctrl-C (`--keep` leaves it). About
  six minutes a round. `--clients fake` (the default) runs `agentbus fake-client` as `claude`, no
  quota; `--clients real` runs Claude Code, once its folder
  (`$XDG_STATE_HOME/agentbus/.livecheck/work`) is trusted: `cd` there, run `claude`, accept, quit.
  On a real machine its relaunches pass only with the holder's terminal numbering kept across a
  restart (step 1a). The soak run waits for its criterion (holder hardening spec §5).
