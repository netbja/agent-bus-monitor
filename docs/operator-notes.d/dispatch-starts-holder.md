- **The dispatch service starts its own build's holder when none answers.** After an update
  restarts dispatch on a new build, the first time the service holds the lease it installs and
  starts that build's holder, as `agentbus up` does. The older holders keep their agents until
  `agentbus holder drain` moves them; it never stops or restarts a holder.
  - A failure is a line in `dispatch.log`, tried again every 5 minutes while the lease is held.
  - If the installed agentbus was replaced since the service started, it refuses ("replaced
    since the dispatcher started"): restart the dispatcher. `agentbus up <project>` does the same
    by hand (agent-screen-blocked Q1).
