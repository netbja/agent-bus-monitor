- **The agent row's terminal facts (herdr exit step 7b2, `cmd/busmon/hostfacts.go`).** Every 2 s
  busmon reads `{p}:events` (the whole stream at its start and after a gap of more than 500
  entries between two reads, else the newest 500) and the project's holders (List and Activity
  only), and puts on the agent's row: `blocked` (its latest blocked/unblocked event is a blocked
  one: the dispatcher saw its client's prompt; group 1, the page says on what), `draft` (its
  terminal that `agentbus attach` picks holds a typed, unsent line; the page says since when) and
  `bg work` (background work made the dispatcher skip an action less than 10 min ago on the
  broker's clock, the clock the events carry; the page quotes it). The events are a timeline: busmon
  keeps each agent's latest blocked/unblocked as it saw it, so `blocked` stays until the agent's own
  unblocked event, however many others follow; a busmon started after a `busmon --reset` trimmed the
  events sees none. Nothing shows while herdr hosts the agents. A failed events or clock read keeps
  the last facts; a holder that fails only loses the drafts.
