- **Request priority is the person's, and lives in the board entry** (spec
  `docs/superpowers/specs/2026-11-04-request-priority-design.md`, phase 1, `bus/priority.go`): `RequestInfo.Priority`
  `high`/`low` (absent = normal, `normal` never stored) with `priority_by` and `priority_at` (Redis `TIME`), written only
  by `Bus.SetRequestPriority` (one script `requestPriority`: unknown, untracked and done refused with
  `requestTransition`'s words; the same level writes nothing; `updated` untouched; no stream entry). Every other board
  writer is a Lua cjson round trip, so the field survives accept, block, delivery, the conductor and done; **never write a
  board entry from a Go struct** (`json.Marshal` + `HSET` drops unknown fields): `TestNoStructWriterOfTheBoard`
  type-checks the module (`go list -export`) for a `json.Marshal` of `BoardEntry`/`RequestInfo` or an `HSet`/`HSetNX`/
  `HMSet` whose key reaches `BoardKey` through assignments and production call sites, and the old `boardSet` is a test
  helper. `bus.RequestLess` (rank, created_at, id) and `bus.NextRequest` (an accepted request first, `finish first`; else
  the first requested with `Availability` retained; blocked does not hold; a done one in review neither holds nor is
  ready: no review barrier) are what `agentbus request next [<agent>] [--json]` prints (exit 64 when nothing is ready)
  and the skills tell agents to follow. `agentbus request priority <task> high|normal|low --as <person>` refuses an
  agent caller (`refuseAgentForPriority`: `AGENT_BUS_AGENT`, or a registered agent's verified pid among the ancestors, or
  a check that cannot be made), passes the identity guard only with `--as`, and every role denies the plain
  `Bash(agentbus request priority*)` (no `agentbus * …` form: it would deny messages). Plan-task priority is phase 2.
