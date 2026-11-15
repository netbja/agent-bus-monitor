- **The run, counted (run-budget §2 and §5, step 1).** `{p}:run` (a hash, runtime kind) is written only by the
  merge-queue pass, after its own write, through `bus.FencedRunPass` (fenced, Redis `TIME`): `started_at`,
  `paused_ms`, `seen_at`, `deliveries`, `state` (`running`; draining and stopped come with step 2), `since`, `why`,
  `drains`, and one `d:<head>` per head counted. Each pass judges every approved deciding head within 72 h
  (`mergequeue.RecentApprovedHeads`; the window on the broker's clock, `bus.ServerTime`, as the approvals' times
  are, `undelivered`'s too, and an unreadable clock fails the pass) with the same git test as master-idle (`Delivery`, its cache shared), in every
  mode, local included, after `undelivered`. No record: the first pass that judged them writes one with every
  delivered head as its baseline, counting none; a pass that could not judge them starts nothing and counts
  nothing (the queue's record is unaffected). Each later pass adds `now − seen_at` to `paused_ms` when `{p}:pause`
  held at the interval's start (a suspend, or a pause whose `until` is past `seen_at`) or when the interval is
  longer than three passes (15 min, a host asleep), and counts each head not yet counted once (`HSETNX`).
  `agentbus run` prints the state, the elapsed time and the deliveries against `run.max_duration` and
  `run.max_deliveries` (registry, `0` = no limit, default) and names a limit reached; nothing acts on it before
  step 2. `agentbus run reset` deletes the record, refused when `AGENT_BUS_AGENT` is set (the person only, as
  `deliver` recognises them: defence in depth); the identity guard reads the bare command as read-only.
