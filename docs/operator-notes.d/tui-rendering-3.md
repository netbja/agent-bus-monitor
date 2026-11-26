- **busmon's bars and gauges take colour ramps (tui-rendering step 3).** In `fine` from 256 colours up, each
  filled cell has its own colour along a ramp; in 16 colours the fill keeps one role colour, and `basic`,
  `NO_COLOR` and ASCII are unchanged. The Agents `ctx` column shows a known context fill as a 6-cell bar and
  its percent: amber from `dispatch.context_alert`, red from `dispatch.context_urgent` (busmon reads both
  at start; it shows the raw tokens only when no fresh fill is known). The account cards' window gauges take
  the usage ramp (amber 80 %, red 95 %). On Tokens the 7-day sparkline is two braille rows (28 cells when the
  row has room), the largest day printed beside it, and more than three projects with no tokens in the week
  fold into one line. The progress bar moves by eighths, and each In progress card in the kanban shows a
  short one.
