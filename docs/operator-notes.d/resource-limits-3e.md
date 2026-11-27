- **An agent near its memory cap turns amber, and master is told once** (agent-resource-limits step 3e, §5).
  Each agent's observation gains `mem_cap`: its share of its holder unit's cap (the unit's `MemoryMax`
  divided by the unit's terminals; cap B, one terminal's own cap, replaces it in step 4); none when the
  unit has no cap or does not answer. At `limits.warn_at` (80 %) of it, busmon's `mem` cell turns amber
  (busmon reads the setting at start, on its own host) and the agent detail reads `6.0G of 8.0G (75 %)`;
  the dispatcher tells the escalation target once per episode, naming the agent, its use and share, and
  its largest process's size and pid, never its name. The episode is the agent's process: a fresh reading
  below ends it, a relaunch starts another, a dispatcher restart repeats nothing. Sentinel is not woken.
  `holder-memory`'s warn at the cap now names the build's agent using the most: `..., most: coder 21.0G`.
