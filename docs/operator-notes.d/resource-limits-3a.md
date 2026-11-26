- **`holder-memory` watches the holder unit's memory** (agent-resource-limits step 3a). The holder
  reads its own unit cgroup when described (`memory.current`, `memory.max`, `memory.swap.current`,
  the `oom_kill` count; four small files, no process) and the dispatcher's check warns at
  `limits.warn_at` (80, a percentage like `dispatch.context_alert`; the person's, per host) of the
  unit's cap, or when the kernel OOM-killed a process in it since the last pass; master is told once
  per episode, as for every warn. Unknown: no cgroup (launchd, the supervised holder), "no memory cap
  on this host". The terminal of a kill is unknown until per-agent caps (step 4). Each process of a
  terminal's tree now carries `rss_kb`.
