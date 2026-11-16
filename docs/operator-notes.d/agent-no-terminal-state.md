- **An agent whose terminal is gone shows `no terminal`, never its last state (agent-no-terminal-state, 2026-11-16).**
  busmon's Agents view (▼, error colour, with the escalated and blocked) and `agentbus agents` show `no terminal` for an
  agent that ran in a holder terminal (its snapshot names one) when a holder of the project answers on this host and
  holds no live terminal of it; its own state comes back with its terminal. It is a reader's projection
  (`bus.ShownState`): the snapshot is never rewritten. With no holder answering (another host, the holder down)
  nothing is judged and the declared state shows. `agentbus agents --json` prints the stored snapshots as before.
