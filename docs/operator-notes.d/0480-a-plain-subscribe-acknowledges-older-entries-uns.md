- **A plain `subscribe` acknowledges older entries unseen; `--drain` does not.** With no
  `--since`, `subscribe` floors at the server's "now", so everything that waited for an unarmed
  agent is ACKed without output. That is why a woken agent must run `subscribe --drain` — keep the
  wake text and the skills pointing there, never at a plain `subscribe`. One exception (v0.14.2,
  `Bus.WatchCmdDeliveryFromNow`, `keptForPerson`): a person's waking entry to that agent
  (`human=yes`, not `wake=no`, retained, within `DefaultHorizon`) is delivered, so an answer that
  arrives while the agent re-arms without its cursor is never lost. An explicit `--since` keeps
  the strict floor.
