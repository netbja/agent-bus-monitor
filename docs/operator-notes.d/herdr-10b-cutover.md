- **herdr is gone (herdr exit, step 10b).** Every agent runs in a terminal of the project's holder; nothing
  execs herdr (`internal/noherdr` fails the suite on a Go `exec` of `herdr`, a read of `HERDR_BIN*` or
  `HERDR_SOCKET_PATH`, or a shell line running it; no allowlist). An agent's terminal is `AGENTBUS_TERM_ID`
  (the identity guard, `idea set`, `deliver`'s role check and the launcher's bind read it; `env_refs` may not
  set `AGENTBUS_*`), never `HERDR_PANE_ID`; these person-only checks read the caller's own environment, so
  they stop mistakes, not an agent that unsets the variables: only the holder's in-tree peer check is enforced
  (security S10b-1). Inside a terminal, `up`, `pop`, `reload`, `swap`, `retire`,
  `suspend`, `resume` (with no words) and `holder drain` go through the dispatcher (`relayFromTerminal`): only
  the words the closed set carries (`pop --profile`, `swap --profile|--back`, `retire --forget --episode <ms>
  --reason <text>`, the end of a done pop the dispatcher asks master for; a reason is one line of at most 120
  characters, recorded, never run), anything else (`--fresh`,
  `--now`, a `--reason` outside retire…) refused with exit 2, never dropped; the service runs the op in-process with the
  sender as the caller (`hostRunner`), so each command keeps its own authorisation: the person's commands
  (suspend, a suspended project's resume, holder drain) see the sender as `AGENT_BUS_AGENT` and refuse it;
  an op on the sender itself, `--ignore-background` from anyone (security S10a-3) and `relaunch` (no such
  command) are refused before anything runs; exit 0 is `done`, 2 `refused`, else `failed`, with the command's
  last line. The dispatcher prunes finished `{p}:hostops` records older than 24 h (`bus.HostOpsKeep`) at its
  first pass and hourly (security S10a-4). `agent-spawn` is gone: master pops with `agentbus pop <role>`
  (its rules `Bash(agentbus pop <role>)` and `Bash(agentbus pop <role> *)`, never a prefix), `setup` removes
  an `agent-spawn` link it made, recoveries name `agentbus --project <p> pop <role>`. The daily review's cron
  sends sentinel a bus directive (the dispatcher wakes it); its crontab line and the services' env file carry
  no `HERDR_*`; the suspend snapshot has no herdr session; `up` has no `--session`; self-repair's hold kind is
  `holder`. Rollback: reinstall the release tagged as the last with herdr.
