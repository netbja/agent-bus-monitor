- **One approval in busmon** (single-approval step 3b, spec 2026-11-21-single-approval-design §4.5,
  §4.8): Shift+C's Services group shows the five `delivery.*` single-approval rows with the values
  `deliver` reads for busmon's project (`[project.<p>.delivery]`, then `[delivery]` except for
  `same_provider_ok`, then the default), read-only: edit `config.toml` yourself; `same_provider_ok`
  says "no effect" while `single_approval` is off. When no independent verdict of the last 7 days was
  by a reviewer of another provider (both providers known and different, deliver's own rule), a line
  under the delivery rows and on each merge item's page says so: `... one approval needs
  delivery.same_provider_ok (now off)`, or, with `docs` or `small`, the same fact as information.
  busmon reads that ledger at most once a minute (`bus.VerdictsSince`). A merge item's page also shows
  deliver's `delivered on one approval` trace for its head, taken only from a report by master or
  docs (the deliverers).
