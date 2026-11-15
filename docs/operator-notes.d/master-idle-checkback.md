- **`agentbus checkback <duration> "<what>"` (master-idle §4, part B).** An agent with work in flight off the bus
  (a test run, a build) says when to look again: one field per agent in `{p}:checkback` (a runtime kind), `{due_ms,
  what, set_id, count, first_set_id, norm}` written by a script on Redis `TIME` (`due_ms` = `TIME` + the duration,
  1m to 7 days). The same `what` again (trimmed, case folded: `norm`) adds one to `count` and keeps `first_set_id`;
  another `what`, or `--clear` then a new one, starts at 1. Plain one-line text (`health.SafeText`, no control
  character), at most 200 runes. An agent's write, no lease (as `status`); the identity guard checks set and
  `--clear`, the bare list (`agentbus checkback`, soonest due first, "due 3m ago" once past) is a read. It never
  expires by itself; `ForgetAgent` and `retire --forget`'s fenced script delete it. master's role and skill gain
  the rule: never end a turn with work in flight unless a tracked background task or a check-back covers it. The
  stall guard reading it (idle-master, and the person told about repeated postponements) is part C.
