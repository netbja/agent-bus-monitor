- **busmon shows each agent's memory and CPU** (agent-resource-limits step 3d, §5). On a full wide
  agents table (at least 110 cells, so not beside For you) the `mem` and `cpu` columns follow `ctx`:
  the agent's terminal tree's resident memory and its CPU over the dispatcher's window (`180%` is
  1.8 cores), from a fresh observation of the agent's current process, a dash otherwise. The agent
  detail adds a Resources line with the largest process's size and pid, and its name only when a
  service record names busmon's own host, so `/proc/<pid>/comm` is the agents' (the observation carries
  no name; busmon through a tunnel shows size and pid). The amber at
  `limits.warn_at` and master's notice come with step 3e.
