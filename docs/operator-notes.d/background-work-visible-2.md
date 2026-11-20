- **The process source in the observation (background-work-visible step 2).** Each verified agent's observation in
  `{p}:observed` gains `bg_procs` (its active non-helper descendants, the background judgement's own filter) and
  `bg_oldest` (the oldest one's start, Redis clock); no process name or argument is published. A process's age counts
  from the dispatcher's first sight of it, so a dispatcher restart restarts the ages; the skip's reason stays without
  an age (its once-per-change event compares it). busmon reads them in step 3.
