- **The dispatcher on a terminal host (herdr exit step 9, `dispatch/hostruntime.go`,
  `dispatch/hostevents.go`).** `dispatch.HostRuntime` is a `Runtime` on `termhost.Host` judged by
  `agentstate`; **nothing runs it yet**: the services' dispatcher keeps herdr's `Runtime` until the
  cutover (step 10), and on herdr none of what follows happens. A pane is a `termhost.TermID`; the
  client comes from the launch profile (`Client`), never the screen; a screen is settled when the
  program wrote nothing for 800 ms (and nothing while it was read); `Focused` is a person active
  (a draft, or a key within 3 s); the identity is the host's child and its process tree. A focused
  attached viewer is not in `Activity`: the holder's `Submit` refuses it (`wake-refused`). On a host
  the loop also: reports a **blocked** episode to master (enter: a waking directive `blocked: <agent>
  (<client>) since … — screen: "…"` after two settled looks of the `Blocked` signature in the
  observation pass; leave: `unblocked: <agent> after …`, `wake=no`; the escalation target itself
  blocked: a notice for the person), once per episode by `{p}:quota` marks `blocked:<agent>` /
  `unblocked:<agent>` (a restart inside a screen-only episode may send its enter again: the episode
  is in memory); tells the person when a line was pasted and **not seen** (`ErrNotLanded`: "wake
  line" or "/clear may be left unsent in <agent>'s input"); skips **auto-clear and pop-retire on
  background work** (`background work: process 'pytest'`; unknown counts as running, so Claude
  Code, whose subagent hooks are authoritative and not wired to the bus yet, is always skipped
  there); and writes `{p}:events` (`wake`, `wake-refused`, `wake-not-landed`, `blocked`,
  `unblocked`, `background-skip`) through `bus.FencedAppendEvent`, fenced by the dispatch lease.
