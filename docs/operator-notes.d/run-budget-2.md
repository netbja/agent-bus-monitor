- **The run drains and stops (run-budget §3, §4 first part, step 2).** On each merge-queue pass, after the run's
  write: `run.max_duration` or `run.max_deliveries` reached moves `{p}:run` to `draining` (`bus.FencedRunDrain`:
  `drains` + 1, the episode `<started_at>:<drains>`), and master gets one waking directive (mark `run-drain`) and
  the person one notice (mark `run-drain-person`), each by its own once-mark, told again on each pass of that state until written (none twice). While it drains, `request send` is
  refused to an agent (`bus.ErrRunDraining`, in the publish script) and allowed to the person (no
  `AGENT_BUS_AGENT`, as `deliver` recognises them: defence in depth); team-stall reads `ok` (`run draining: ready
  plan tasks wait`); idle-master still wakes master for undelivered heads. A run draining for a limit stops
  (`bus.FencedRunStop`) when nothing is in flight (a requested or accepted task, an undelivered head, an open gate (any gate key, a reviewer offline included),
  any gate key, a reviewer offline included; an agent working) or `run.drain_max` (default 2h, `0` = until nothing is in flight) after the drain began: state
  `stopped`, a kind-`run` pause a year ahead (any pause but a suspend replaced), one notice (mark
  `run-stop-person`) listing what was still in flight if `drain_max` cut it short. A pass that could not read the
  queue stops nothing. No quota release lifts the run's pause; `agentbus run reset` (the person) lifts it with the
  record. Quota, for a client that reports it: at `dispatch.quota_warn` each agent on that provider holding work
  (an accepted task, or working) is told once per window (mark `warn:<p>:<window>:<agent>`) to finish its step,
  commit its work on its branch, report what is left and start nothing new; master's provider drains the run
  with `why = quota:<p>` (announced like any drain, never stopped by the pass: the quota's own pause holds the
  project), and the run goes on when that provider is clear (every window under the warn threshold, no hold) or
  its hold is released; a limit reached meanwhile replaces `why`, and the run then stays draining. Not in step 2:
  `{p}:held`, the held directives and the silent client's screen hold (`run.screen_hold_max`).
