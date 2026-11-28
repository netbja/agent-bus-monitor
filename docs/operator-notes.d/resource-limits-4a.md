- **`roles doctor` names the host's cgroup mode (resource-limits step 4a).** Its text ends with one line:
  `cgroup mode: scope, memory delegated, kill: cgroup.kill, linger on` when the systemd user manager delegates the
  memory controller (a holder could then cap each terminal in a scope, step 4b), or `soft: <reason>` (no user
  manager, memory not delegated, macOS), and `linger off: holder stops at logout` when linger is off. `--json` is
  unchanged. The new package `cgroot` finds an agent's cgroup (an `agentbus-*.scope` or `holder/agents/<…>`, the
  process in its `client` leaf) and the kill method (`cgroup.kill`, else freeze-and-kill, else none); nothing
  moves a process or starts a unit until step 4b.
