- **Activity's unread marks can be cleared in one go (triage-unread step 1).** In Activity, `N` marks read every
  unread line that no triage item holds, after a question (`mark 12 lines read?`); a line arriving meanwhile stays
  unread. `m` toggles the selected line, `z` undoes the last `N` or `m` once. On an agent's page `N` marks that
  agent's lines. A line a triage item holds (its own report, an ask's line, a waiting agent's newest report, a
  `--needs` line naming one item) is never marked: open it from `T`. The filter takes `is:unread` (`@master
  is:unread`). Marks stay this busmon's cache, as opening a line: nothing is written on the bus.
