- **An agent whose terminal is gone shows its presence, never its last state (agent-no-terminal-state, 2026-11-16;
  cleaning-up-agents step 1a).** When a holder of the project answers on this host and holds no live terminal of an
  agent that ran in one (its snapshot names it), `agentbus agents` shows `stopped` (an optional role that owes
  nothing) or `lost` with its reason (`owns <task> (<state>)`, `required: …`, `<n> message(s) waiting`, `an open
  ask`, `a gate`, `a hand-off not taken`, `unknown: <item>`), and `--json` adds `presence` and `presence_reason`.
  Until the merge-queue pass reads unpushed branches and stashes (step 2), that item is unknown, so a terminal-less
  agent is `lost`, "unknown: losable work", never `stopped`. busmon (step 1b) shows the same: `lost`
  red in the first group with its reason as the row's first flag, `stopped` grey in the no-state group, the Board's
  alert `owner lost` on a request whose owner is lost; what each agent owes is read every 15 s, and until then an
  agent without a terminal reads lost, "unknown: owes". A reader's projection (`bus.Presence`): the snapshot is never rewritten. With no holder
  answering (another host, the holder down) nothing is judged and the declared state shows.
