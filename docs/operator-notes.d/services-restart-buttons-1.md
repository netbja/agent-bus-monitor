- **A service's log page reads (services-restart-buttons step 1).** F3, Enter on a card: each line has a level (a
  dispatch `escalate`, `blocked` or `unblocked` is attention, `none` muted; a text line saying `failed`, `error` or
  `panic` is an error), a decision reads `action  agent  reason  (rule N)` (the raw file keeps `high_id` and
  `latency_ms`), and the routine `none` decisions are folded (`a` shows them; the title counts them). `f` keeps the
  lines holding a word, Esc clears it. The page reads the log back, `.1` after it, until 200 lines that are not
  routine are found, within 8 MiB. The dispatcher now writes `"ts"` (RFC 3339, local zone) on every line it logs;
  older lines show no time.
