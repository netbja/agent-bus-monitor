- **The verdict ledger is the audit source of truth, the gate is just a lock.** `verdict` writes
  `{p}:verdicts` unconditionally (self-approvals included — the roll-up ignores them, but the record
  stays). The roll-up rule: APPROVED iff the latest independent approve (reviewer ≠ author) is newer
  than any reject. Don't gate "was this approved?" on the `{p}:gate:{agent}` hash — that only holds
  *open* challenges; query `agentbus verdicts` instead. And never close a stale gate with a verdict
  of your own (it counts as an approve): `agentbus gate close` leaves a trace that is not a verdict.
  busmon's For you lists a gate only when it concerns the person (`personGateItems`: the person
  opened it, or it holds the person, read as `personGates` when the person is no agent); a review
  between agents never counts in the strip's `decide` chip.
