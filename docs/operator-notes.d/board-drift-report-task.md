- **A report names its request (board-drift step 3, spec §4).** `agentbus report <self> --task <t>` writes
  `task` on the report and notes it on the tracked request (`request.reported_at`, `request.report_id`), with
  no state change, no stream entry and no CHANGED bump; the Board's "no reply" then counts from it. The note
  refuses only an unknown task, an untracked one, a request whose target is not the reporter, and a done one
  (never the WIP limit, an expiry or a lost body), and is checked before anything is published. `--done` (with
  `--task` only) also completes the request as `request done`, the report's first line plus `(report <id>)` as
  the reply; it is checked first too (not accepted, blocked: nothing published). A `done` refused after the
  report was published (a change in between) prints the report's id, then the refusal, and exits 1: the report
  stands. A report without `--task` is as before. An older `agentbus` has no `--task`: it publishes the flag as
  message text, so install the release before telling agents to use it.
