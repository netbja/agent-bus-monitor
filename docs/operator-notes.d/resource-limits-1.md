- **The holder unit is capped (cap A) and survives an OOM kill inside it** (agent-resource-limits
  step 1). `agentbus up` writes `OOMPolicy=continue`, `MemoryHigh=60%`, `MemoryMax=70%` and
  `MemorySwapMax=4G` into the holder unit: a kill takes one process, never every agent (on
  2026-11-25 systemd's default `OOMPolicy=stop` stopped the whole unit). The values are the
  person's keys `limits.holder_memory_high`, `limits.holder_memory_max` and
  `limits.holder_swap_max` in `config.toml` (systemd's syntax, `off` for no line), never an env
  variable. They apply live: `up` rewrites the current build's unit when its text changed and
  reloads systemd, which caps the running holder at once, no restart. A limit set below the
  holder's current use makes the kernel reclaim or kill right away. launchd and the supervised
  holder get no cap.
