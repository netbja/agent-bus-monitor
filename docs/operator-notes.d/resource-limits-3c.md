- **Each agent's memory and CPU in the observation** (agent-resource-limits step 3c, §5). Each verified
  agent's observation in `{p}:observed` gains `mem_bytes` (the resident memory of its terminal's whole
  tree, the agent's own process included, each process once), `top_bytes` and `top_pid` (its largest
  process's size and pid) and `cpu_pct` (the tree's CPU over the dispatcher's 10 s window, a
  percentage of one core: `180` is 1.8 cores; absent until the window's first sample, so after a dispatcher restart). A host that reads
  no RSS (no `/proc`) publishes no memory. No process name is published, as for `bg_procs` (spec §5);
  a reader on the same host may look `top_pid` up. busmon's columns and master's notice come next.
