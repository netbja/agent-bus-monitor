- **busmon's Plan is a roadmap, and `4` opens it (views-redesign step 4a).** `4` (`6` at the narrow tier) opens the
  Plan as `p` and `P` do; it is no longer an alias to Work's NEXT. Sections: NOW (in progress, in review, answered,
  blocked), APPROVED (both reviewers approved, waiting for delivery or the merge; folded when empty), NEXT (not
  started and unblocked, the person's decisions among them, read `decide`), LATER (blocked by another task, then the
  low-priority tasks, blocked or not), DONE as a count. NOW and NEXT sort by priority, then file order. A plan task
  may carry `"priority": "high"` or `"low"` (`"normal"` and absent are the same); the row's first cell is `↑` or `↓`,
  else the state's glyph in NOW and `✓` in APPROVED. A task's dependencies read `after <first> +N`; the side panel
  lists them (`✓` when done), the tasks that wait on it (`unblocks`) and its priority. At the narrow tier LATER and
  DONE fold to a line Enter opens (`LATER 3 · 1 ↑ (enter)`). A grouped plan keeps its groups, each group's rows in
  the roadmap's order. Raising or lowering a plan task's priority from busmon (`+`/`-`) is step 4b, not built.
