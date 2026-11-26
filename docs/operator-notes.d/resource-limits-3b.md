- **`holder-draining` names the drain's age and fails when it lasts** (agent-resource-limits step 3b).
  The detail reads `build v0.33.0: 3 terminal(s), draining for 21h`, the age being the current
  build's holder's (the drain began when it came). Past `limits.holder_drain_stale_after` (6h, the
  person's, per host) the check fails and names the agents still in the old build:
  `agentbus holder drain <project>` relaunches them onto the current build, one at a time
  (`agentbus --project <project> reload <agent>` for one). Never `service restart holder`: it
  restarts the current build and kills its agents. Nothing stops the old build by itself.
