- **Each agent's memory and CPU in the observation** (agent-resource-limits step 3c, §5). Each verified
  agent's observation in `{p}:observed` gains `mem_bytes` (the resident memory of its terminal's whole
  tree, the agent's own process included, each process once), `top_bytes` (its largest process's) and
  `cpu_pct` (the tree's CPU over the dispatcher's 10 s window, a percentage of one core: `180` is
  1.8 cores; absent until the window's first sample, so after a dispatcher restart). A host that reads
  no RSS (no `/proc`) publishes no memory. No process name is published yet (§5's largest process is
  named by size only). busmon's columns and master's notice come next.
