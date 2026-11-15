- **Ideas (ideas slice 1): the project's ideas live on the broker, not in notes.** `bus/ideas.go`:
  `{p}:ideas`, one flat hash (`seq`, `<id>` the definition {title, text, by, at, source}, `<id>|s` the
  state, `<id>|log` the capped audit {from, to, by, at}), every write one Lua script with Redis `TIME`.
  Four states, none final: `to discuss`, `decided`, `doing`, `dropped`. `agentbus idea add` is open to
  agents and always starts at `to discuss`; `idea set` is the person's: refused in an agent's
  environment (`AGENT_BUS_AGENT`) or a herdr pane (`HERDR_PANE_ID`), named by `--as` or `identity.as`
  (never `pilot`), and the script refuses a name registered in `{p}:agents` or a reserved one. A set is
  a compare-and-set on the state the setter saw (`--from`, or the state read just before; busmon's
  picker passes the state shown), so two windows never overwrite each other. Each set writes a `reply`
  to `people` (`wake=no`, `human=yes`): Activity shows it, nobody is woken. busmon: Work's IDEAS lists
  the ideas still `to discuss` (a plan task naming an idea's id or title takes its place); F6 lists
  them all by state (tab: one state, enter: the idea, a: add one in the compose window, its first line
  the title and the rest its text, s: the state picker, ←→ choose, enter sets it) and flags a state set by someone
  other than the configured person. busmon refuses a set or an add when it runs in an agent's
  environment (`AGENT_BUS_AGENT`: it would inherit the person's name; an agent adds with `idea add`
  under its own); a failed add keeps the window's text for the next `a`. An add is idempotent by
  its request id (`IdeaDef.Request`, a `req|<id>` field holding the idea and its Redis time, dropped by the next add
  after a day: no field TTL, the broker floor is 7.2): busmon reuses the id while
  the text sent is unchanged, and `idea add` always sends one and prints it on a failure (`--request <id>`
  retries), so a reply lost after the script committed never yields a second idea; it does not refuse a herdr pane, since the
  person's own busmon runs in one (the plugin). **The limit, said plainly: this is an
  audit, not a proof.** Agents run under the person's uid with the same broker URL, so nothing stops a
  deliberate `idea set --as <person>` from a plain shell, or a raw `HSET`: the guards catch accidents
  and every set records who and when. Real prevention would need a separate uid for the agents (out of
  scope). The ideas once kept as open-question notes are moved by master with `idea add --source`
  (data, not code).
