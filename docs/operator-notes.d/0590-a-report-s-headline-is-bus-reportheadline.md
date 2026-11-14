- **A report's headline is `bus.ReportHeadline`** (report-one-row §4): the first line of substance of `full` (else `message`),
  blank lines, Markdown headings, code fences and table lines skipped, a list marker stripped, cut after its first sentence;
  the first heading's text when nothing else, else "". The notice `report --to` writes carries it (else the first line; still
  sanitised and cut at 300 runes, the envelope unchanged so `reportSubject` reads old and new), and busmon, the recap, `i`'s
  quote and the `reports` table show it.
