- **An install names the project services left on the previous build, and restarts them on your yes
  (update-other-projects, 2026-11-16).** Each dispatch and missions service records the build it runs
  (`build` in `{p}:services`, `holder.BuildID`). After `setup.sh install` or `update`, setup asks the installed
  `agentbus service stale` which of this host's running services recorded another build (a record from before
  builds were recorded, or of another process, counts as older), lists each with its
  `agentbus --project <p> service restart <kind>`, and asks once `Restart these N services now? [y/N]`. `--yes`
  and no terminal restart nothing. Holders and agents are never touched. Seen 2026-11-16: ai-tradex-solana,
  hyperliquid and demo kept the old binary after an install. The services started before this build list as
  `no build recorded` until restarted once.
