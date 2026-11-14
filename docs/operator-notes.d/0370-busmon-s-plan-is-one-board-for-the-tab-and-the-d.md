- **busmon's plan is one board for the tab and the drawer** (plan-board, same spec §4):
  `planBoard` (`planboard.go`) gives the lines, the task items and the side panel for both; it
  replaced `planOverlay` and `planTabItems`, and the done group's `toggle` row is gone (`d`,
  `ActShowDone`, on the tab and the page). The drawer is the `ovPlan` page with `drawer` set:
  `readerRect` reads the bottom page's flag, so a page opened from the drawer stays in its frame,
  and at the wide tier `grid` does not draw Agents under it. The side panel is a frame of its own
  (`drawPlanSide`) inside the tab's frame or the page's, drawn over the narrower rows.
  The header's clickable items carry a `headerHit`; `headerLayout` returns their spans, and `click`
  hit-tests the header after closing an open reader. `tourVersion` is 5 (3: the header page; 4: the welcome page's For you line, home-inbox; 5: Useful keys with `p`, plan-board).
