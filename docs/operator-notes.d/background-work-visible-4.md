- **A background skip that lasts is told to master, and a declaration holds the skip (background-work-visible step 4).**
  When the dispatcher has skipped an agent's auto-clear or pop-retire for its background work for
  `dispatch.background_notice_after` (default 2h, `0` off, Health group), the escalation target gets one notice per
  episode, naming the agent, the action and the source (hooks, process, screen, declared, unknown); wake=no when the
  agent is the target itself. The episode is two `{p}:quota` marks, `bgskip:<agent>:<action>` and
  `bgskip-notice:<agent>:<action>`, ended by the first pass that does not skip or by a new process. The agent's declared
  text (`agentbus background`) is now a judgement source, always running while it lasts, read by the dispatcher and by
  `holder drain` (`--ignore-background` still overrides): it holds the pop-retire and the drain, and the auto-clear
  until the agent's context episode has escalated (the `context-escalate` mark), when observed work alone still holds it.
