- **`agentbus deliver --dry-run` prints the change's size class** (single-approval step 1, spec
  2026-11-21-single-approval-design §3.2): from `git diff --no-renames --numstat -z` against the
  merge-base with the base, `size: small: N lines, M files`, `small (docs only)` (every path
  `docs/**` or `*.md`, at any size), or `not small (<reason>)`: over 150 lines or 5 files, a binary
  or unreadable row, or a never-small path (`bus/**`, setup, broker and ACL files, secrets, CI,
  `skills/**`, and the gate itself: `deliver.go`, the classifier `deliversmall.go`, its glob matcher
  in `filepick/`, `mergequeue/**`, `config/keys.go`). No rule reads it yet: delivery still needs
  two approvals or `--single-approval`.
