- **`agentbus deliver --dry-run` prints the change's size class** (single-approval step 1, spec
  2026-11-21-single-approval-design §3.2): from `git diff --no-renames --numstat -z` against the
  merge-base with the base, `size: small: N lines, M files`, `small (docs only)` (every path
  `docs/**` or `*.md`; within the same limits, the person's answer to §6 Q2), or `not small (<reason>)`: over the project's `small_max_lines` or `small_max_files` (150, 5), a binary
  or unreadable row, or a never-small path (`bus/**`, setup, broker and ACL files, secrets, CI,
  `skills/**`, and the gate itself: `deliver.go`, the classifier `deliversmall.go`, the rule `deliversingle.go`, its glob matcher
  in `filepick/`, `mergequeue/**`, `config/**`). The single-approval rule reads it
  (`single-approval-2.md`).
