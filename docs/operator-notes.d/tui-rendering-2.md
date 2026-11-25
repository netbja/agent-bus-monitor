- **busmon has one rendering setting, `display.rendering` (`auto`, `fine`, `basic`; tui-rendering step 2).** `auto`
  is `fine` on a UTF-8 locale: bars and gauges draw eighths on a track from 256 colours up (16 colours and
  `NO_COLOR` keep whole cells), and the Tokens sparklines are braille by default now (they were blocks). `basic`
  is the old look. `display.graphs` still works this release, mapped (`block` basic, `braille` fine, `ascii`
  ascii) when `display.rendering` is not set; the settings page says which one decided. A locale without UTF-8
  draws every bar, gauge and sparkline in ASCII, whatever is set.
