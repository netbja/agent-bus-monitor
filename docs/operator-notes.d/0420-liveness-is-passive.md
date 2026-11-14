- **Liveness is passive** — there is no heartbeat stream. busmon shows the age of each agent's
  last declared status as a freshness bar (empty after `staleAfter` 10m, atop
  `cmd/busmon/main.go`) and never infers `offline`. Agents are one-shot CLI calls, so each
  status/report publish *is* the heartbeat; don't add a heartbeat unless agents become long-running
  daemons.
