- **The scope launch's pieces, not yet used (resource-limits step 4b-1).** `cgroot.ScopeArgv` builds §12.5's
  `systemd-run --user --scope --slice=agentbus.slice --unit=agentbus-<p>-<key>-<role>-<term>.scope -p Delegate=yes
  -p MemoryMax=… -p MemorySwapMax=… -p OOMPolicy=continue -- agentbus agent-enter -- <client>`; the build key's
  characters outside `[A-Za-z0-9._]` become `_` in the name. `agentbus agent-enter -- <client>` (internal, no
  project) moves its own pid into the scope's `client` leaf, enables `+memory +pids +cpu`, and execs the client
  under the same pid; outside a scope it says so on stderr and execs the client uncapped. The holder starts using
  them in step 4b-2.
