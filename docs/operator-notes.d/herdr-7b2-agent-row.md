- **The agent row's terminal facts (herdr exit step 7b2, `cmd/busmon/hostfacts.go`).** Every 2 s
  busmon reads the newest 500 `{p}:events` and the project's holders (List and Activity only) and
  puts on the agent's row: `blocked` (its latest blocked/unblocked event is a blocked one: the
  dispatcher saw its client's prompt; group 1, the page says on what), `draft` (its terminal that
  `agentbus attach` picks holds a typed, unsent line; the page says since when) and `bg work` (background work
  made the dispatcher skip an action in the last 10 min; the page quotes it). Nothing shows while
  herdr hosts the agents: no holder terminal, no such event. A failed events read keeps the last
  facts; a holder that fails only loses the drafts.
