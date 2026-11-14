- **`report` keeps a bounded preview AND the full text.** The write path stores `message` (the
  ≤500 one-line `SanitizeReportMessage` preview, for `listen`/busmon) and — only when it carries
  more — a `full` field (`SanitizeReportFull`: newlines/tabs kept, ≤8000). Don't raise the preview
  cap to "see more" — that floods the flat viewers; use `agentbus reports <id>` (or the busmon
  ` (+N)` marker) instead. The `full` field lives in the entry and is trimmed with the ~1000-cap
  report stream — `report` is an activity channel, not the durable audit (that's the verdict ledger).
