- **For you holds one item per target (triage-dedupe step 2, `foldLines` in `cmd/busmon/tea_foryou.go`).**
  A merge item is keyed by its PR (`merge:pr:<n>`, else `merge:branch:<b>`), so a rebase keeps it; its
  page still opens by head (`attentionItem.Head`). A line naming exactly one live target (`#<n>`,
  `PR <n>`, a merge item's branch, an ask's id) attaches to that item (its mark, and "needs your answer"
  or "to read" when the line is a `--needs` report); a waiting agent's own newest report folds into its
  wait row. A report is an item only with `--needs`: naming no single target, its own `report:<id>`
  (rank `rankReport`, verb answer or read), replaced by the same sender's newer one naming the same
  targets; `answer` stays until the person writes to its sender, `read` until it is read. A plain
  report is Activity only. The header's unread badge counts Activity's unread marks
  (`activityUnread`), apart from For you's items.
