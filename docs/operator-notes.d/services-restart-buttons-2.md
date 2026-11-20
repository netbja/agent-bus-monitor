- **Restart a service from busmon (services-restart-buttons step 2).** F3, on the dispatch or missions card: `X` asks
  `Restart <kind> of <p>? agentbus --project <p> service restart <kind>. y / n`; `y` runs that command as Z and the
  Projects page's `x` run agentbus (one run at a time, the output in the project's log, its last line the notice),
  and the card is read again when it ends. busmon refuses before asking in an agent's environment
  (`AGENT_BUS_AGENT`) or under a registered agent; the `agentbus service restart` command itself still accepts every
  caller (the sentinel's repair). Refused too: a service on another host (`restart it on <host>`), one not installed
  here, a run already going, and the Redis card (setup's job). Holders stay read-only.
