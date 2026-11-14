- **busmon's For you holds no plan task and draws no unread line** (home-inbox, spec
  `2026-11-07-home-inbox-drawer-design.md` §3): `rankPlan` and `planItems` are gone, so a plan task
  never counts in `decide`, the reminder or the walk; the plan's `♙ N` is the header's plan item.
  One muted pointer row, `plan-next:<task>` from `nextPlanTask` (the first person task Waiting,
  else Blocked, else Upcoming), ends the panel and the empty state alike; it is no `forYou` item.
  The panel draws `forYouPanelItems` (`forYou` minus `rankUnread`); the model keeps the unread lines
  for the strip, `u` and the walk, so For you expands nothing in place and `recapInPanel` no longer
  drops `unread:` recap rows.
