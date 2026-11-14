- **busmon's idle view** (spec `docs/superpowers/specs/2026-11-08-busmon-idle-view-design.md`, layout A, the
  person's choice): `display.idle_view_after` (0 = off, the default; 0 or 1m..24h, `fileIdleView`/`envIdleView`:
  a file value outside is refused, an env value skipped). `Model.ui.lastInput` is the poll time of the last input
  (`isInput`: key, paste, click, wheel, motion, focus; never a tick, a tail batch, a resize, a fade frame or a blur),
  the first poll's until then; `enterIdle` runs on each poll and never over `busyForModal()`, which also holds while
  the view is shown, so a due reminder, recap or tour opens once it is left; notices wait (`noticeHeld`) and no fade
  frame is scheduled. The waking input is dropped in `update` before any dispatch (`q` never quits).
  A terminal sends the current focus as soon as reports are on (Ghostty), so while shown a focus wakes
  only after a blur (`idleFocus`, `ui.idleBlurred`, reset on each entry). `View` asks
  `MouseModeAllMotion` and focus reports only while it is shown. `idleview_draw.go` draws you (the decide + merge
  count in 5-row digits), team (agents not idle, then the idle count) and next (the plan's task in hand, then the
  first upcoming) side by side, else stacked, else one line each; the block drifts one cell a minute
  (`idleDrift`, a diagonal bounce), the strip up to 4 cells.
