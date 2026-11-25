- **The Plan view's bars are thin (plan-progress-bars).** The release bar (30 cells) and each group's bar (12) draw
  a heavy `━` fill over a dim `─` track, the remainder's eighth (`▏`–`▉`) at the leading edge
  (`finedraw.ThinBar`); every bar ends in one column, its `n/m` under the release's, while the line holds it
  (a long tail shortens the release bar first, as before). The fill is green for a done group, else the group's
  state colour (accent in progress, orange waiting for the person, red blocked, green approved); a group not
  started is the track alone. `display.rendering` applies: basic keeps whole `━` cells, a locale without UTF-8
  draws `=` and `-`. A group with tasks done and the rest not started now reads `in progress`, not `not started`.
