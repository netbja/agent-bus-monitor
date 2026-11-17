- **`dispatch holder-behind`: no holder of the dispatcher's build runs, an older one does.**
  After an update restarts the dispatch service on a new build, the agents on an older build's
  holder are still read and woken. But a `pop`, a relaunch or a `reload` starts on the
  dispatcher's own build and fails. The state's detail names the builds that answer and which
  of them host an agent ("answers but hosts no agent" otherwise). `agentbus up <project>`
  installs and starts the missing holder beside the old one, without touching the agents.
  `waiting-holder` now means that no holder of the project answers (agent-screen-blocked §1).
