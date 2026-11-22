- **Each setting says when a change applies** (settings-read-when): `live` (busmon applies it at once,
  or a service re-reads it at its next pass or check), `at the next run` (a command reads it each time),
  `on busmon restart`, `on service restart` (read once when dispatch, missions or the holder starts)
  or `on agent restart`; the slowest reader decides. It is `config/applies.go`, beside the registry
  (not in `config/keys.go`, which every settings PR edits), shown last in `agentbus config` (`applies`,
  also in `--json`), in busmon's Shift+C detail, and in the save message, which no longer says "next
  start of busmon" for a key busmon does not read. A services env file is read when a service starts,
  whatever the key. A new key needs its entry (`TestEveryKeySaysWhenItApplies`), and when you move a
  read from a service's start into a pass, or back, change its entry; busmon's own switch is held
  equal to the registry by `TestBusmonAppliesAgreeWithTheRegistry`.
