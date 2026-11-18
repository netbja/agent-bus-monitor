- **busmon's sparklines follow `display.graphs` (braille-sparklines, 2026-11-18).** The Tokens page's
  7-day column is drawn in `block` (eight blocks), `braille` (two days per cell, 4 levels, filled; padded
  to 7 cells) or `ascii` (`_.-:=+*#`); `auto`, the default, is `ascii` when the locale (`LC_ALL`, then
  `LC_CTYPE`, then `LANG`) does not name UTF-8, else `block`, and never braille. Env
  `AGENT_BUS_BUSMON_GRAPHS`; set on the `C` page, applied from the next frame. Any non-zero day lights at
  least the lowest step (the bottom dot in braille). Gauges and bars keep their blocks in every style.
