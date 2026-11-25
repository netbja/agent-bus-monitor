- **The wip limit in busmon (wip-limit §3.5, step 3).** busmon reads `board.wip_limit` for the shown
  project (`[project.<name>.board]`, then `[board]`; again on a project switch) and stamps each poll's
  agents with their in-flight requests: tracked, `accepted`, the agent the target (review, blocked and
  queued are not counted). The Agents row carries `wip n/m` among its flags, in the attention role at
  the limit, nothing at 0 or with none held; the agent's page an **In flight** row with each request's
  age, one past `board.no_reply_after` marked as the Board marks it; a queued request to an agent at
  its limit reads `waits: <agent> at wip limit` on the Board and in the Requests view. Saving the key
  in Shift+C restamps the rows at once and flashes `wip limit n: applies at each agent's next accept`.
  busmon only shows the limit: the bus enforces it at a first accept.
