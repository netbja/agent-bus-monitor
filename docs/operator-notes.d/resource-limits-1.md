- **The holder unit is capped (cap A) and survives an OOM kill inside it** (agent-resource-limits
  step 1). `agentbus up` now writes `OOMPolicy=continue`, `MemoryHigh=60%`, `MemoryMax=70%` and
  `MemorySwapMax=4G` into a new build's holder unit: a kill takes one process, never every agent
  (on 2026-11-25 systemd's default `OOMPolicy=stop` stopped the whole unit). The values are the
  person's keys `limits.holder_memory_max`, `limits.holder_memory_high` and `limits.holder_swap_max`
  in `config.toml` (systemd's syntax, `off` for no line), never an env variable. A running holder
  keeps its unit until the next build's `up`; launchd and the supervised holder get no cap.
