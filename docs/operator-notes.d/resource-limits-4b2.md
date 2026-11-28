- **Each terminal in its own scope, cap B (resource-limits step 4b-2, spec §12.5).** On Linux with `systemd-run` and
  `systemctl`, a holder starts each pane as `agentbus-<p>-<key>-<role>-<term>.scope` in `agentbus.slice`, its
  `MemoryMax` and `MemorySwapMax` from the new `limits.agent_memory_max` (`30%`) and `limits.agent_swap_max` (`2G`) in
  `config.toml`, read at each launch (`off`: none). When `systemd-run` ends before its exec (no user manager), the
  pane starts uncapped and the holder's log says `no scope`. The holder stops a terminal's scope when it ends, every
  scope of its build when it stops, and at start sweeps the project's scopes: its own build's, and another build's
  only when that holder neither answers nor runs (a manager that cannot say keeps it, logged `stale?`). This applies
  to a holder of this build, at its next start.
