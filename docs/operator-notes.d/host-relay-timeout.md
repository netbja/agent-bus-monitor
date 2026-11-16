- **Host commands from an agent's terminal reach the dispatch service (host-relay-timeout, 2026-11-16).** Since herdr
  exit 10b, `pop`, `reload`, `retire`, `swap`, `suspend`, `resume` and `holder drain` run inside an agent's terminal
  are relayed to the dispatcher, but only the foreground `agentbus dispatch` turned the relay on: the dispatch
  service (`service run dispatch`) never read a host directive, so each one ended `the dispatcher did not answer
  within 5 s ... Nothing was done` (seen with `reload sentinel` and `pop coder2` from master, on both the old and the
  new build). The service's dispatcher now runs the same relay (`serviceDispatchConfig`, `hostOpsConfig`). It takes
  effect when the dispatch service restarts on the new build.
