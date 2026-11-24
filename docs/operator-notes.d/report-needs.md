- **`report --needs answer|read` (triage-dedupe step 1).** A report says it needs the person with the
  field `needs` (`bus.NeedsAnswer`, `bus.NeedsRead`, `Bus.ReportNeeds`); each `--to` notice carries it too
  (`Bus.ReportNotice`), so `subscribe`'s JSON shows `"needs"` (additive in v1) and `agentbus history` prints
  `report (needs answer)`. Any other value is refused before anything is written, and a value a hand
  write put on an entry reads as none. busmon reads it (triage-dedupe step 2, `triage-by-target.md`): a
  report is a triage item only with `needs`.
