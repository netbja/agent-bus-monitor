# The settings page shows the focus as a bar (settings-focus-bar)

Status: spec and implementation on one branch (subject `settings-focus-bar`), at the person's
request after a screenshot of Settings (Display). It extends
[settings-focus-line](2026-11-07-settings-focus-line-design.md) and keeps its text line.

## 1. The gap

The focus line (`focus: groups · → or tab: the keys`) is muted text. On the person's screen it
reads as a hint, not as a marker: nothing shows at a glance which side has the focus. The person
expected a visible bar, as a tab bar shows its active tab.

## 2. The change: a bar on the focused panel's top edge

A heavy accent bar (`━`, `GlyphFocusBar`, `RoleAccent`, bold) sits on the row just above the
focused panel. No row is added, so no group or key moves and `selLine` is unchanged.

| Focus | Wide page (sidebar) | Narrow page (tab bar) |
|---|---|---|
| groups / tabs | row 0, the sidebar's part: one cell in, the bar across the sidebar's width; the text line stays in the keys' column, without a bar | row 0: the text line, then the bar to the end of the row (the tab bar is below it) |
| keys | row 0, the keys' column: the text line, then the bar to the column's end; the sidebar's part stays blank | row 2 (the blank row between the tab bar and the keys): the bar across the full width; row 0 is the text line alone |

- **Why the top edge.** The bar is next to the panel it marks: above the sidebar or above the
  keys on a wide page, above the tabs or above the keys on a narrow one. The left/right split of
  the wide page becomes top/bottom when the panels stack.
- **The text line is kept, and it fits first.** Behind a bar, the text is cut with the same order
  as before (the hint first, then `focus:`; the focused word stays). The bar takes what is left,
  and never fewer than 2 cells. So on the narrowest frame, the word and a short bar are both
  shown.
- **NO_COLOR and 16 colours.** The bar is a glyph, not a colour: under `NO_COLOR` it is drawn in
  the terminal's own colour, bold, and with 16 colours in the palette's accent. It still shows by
  its place and by its heavy stroke, which differs from the light rules (`─`, `│`) busmon draws
  elsewhere. The words of the text line still name the panel. Nothing else is needed for these
  themes.
- **What it follows.** `s.keys` and the layout (`side`) at each render, as the text line and the
  lit marks do. It cannot disagree with them.

## 3. Code

- `cmd/busmon/settings_ui.go`: `settingsFocusLine(keys, side, bar bool, width)` appends the bar
  when `bar`; `settingsBody` draws the sidebar's bar and the narrow row 2.
- `GlyphFocusBar = "━"` beside the other glyphs.
- Tests (first): the bar's place in the four cases, its role and stroke, the text line still
  present behind it, at least 2 cells on a narrow column, row 0 and row 2 widths, and the rows
  and `selLine` unchanged. The settings goldens and overlays are regenerated; only row 0 (and
  row 2 on the narrow page with the keys focused) change.

## 4. Rejected

- **A bar on row 0 for both layouts.** On a narrow page row 0 is above the tabs. A bar there
  while the keys have the focus would sit next to the wrong panel.
- **A coloured background (a pill) behind the panel's name.** It is colour only. Under
  `NO_COLOR` it falls back to reverse video, which the selected tab already uses.
