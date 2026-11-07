# The settings page says where the focus is (settings-focus-line)

Status: spec for review (subject `settings-focus-line`), v0.29 chantier 3. No code on this branch.

## 1. The gap

busmon's settings page (`C`) has two panels: the groups and the selected group's keys. On a wide
page the groups are a sidebar; below 92 cells (`settingsSideMin`) they are a tab bar. The focus
(`settingsPage.keys`) is on one panel or the other, and today it shows only through colour and
weight (settings-focus):

- on the groups, the selected group is in the accent and bold, and on a narrow page its tab is a
  full pill;
- on the keys, the selected key's `▶` is in the accent, and the group is plain or its tab is
  dimmed by `frameFade`.

The arrows already move the focus. But nothing says in words which panel has it, so the person
has to guess what `↑↓` will move. With 16 colours, `NO_COLOR` or a faded frame, the cues are
hard to tell apart.

## 2. The change: the top-margin row carries one muted line

The page already starts with one blank row of top margin (about-trim). On a wide page the
sidebar's rule runs through that row. That row now carries the focus line. The page keeps the
same height, and no group or key moves.

| Focus | Wide page (sidebar) | Narrow page (tab bar) |
|---|---|---|
| groups | `focus: groups · → or tab: the keys` | `focus: tabs · ↓ or tab: the keys` |
| keys | `focus: keys · ← or tab: the groups` | `focus: keys · ← or tab: the tabs` |

- **Roles.** The whole line is `RoleMuted`, except the focused panel's word (`groups`, `tabs` or
  `keys`), which is `RoleText` and bold. The line stays discreet: the accent remains the cue on
  the selected row itself.
- **The words carry the meaning, not the colour.** The line reads the same under `NO_COLOR` and
  16 colours. This is the reason for the change.
- **Place.**
  - Wide: the line is in the keys panel's column, after the rule and one cell in, like the
    group's name row below it. The sidebar's part of the row stays blank, and the rule still runs
    through it.
  - Narrow: the line starts at the first cell after the frame, then the tab bar and its blank row
    follow as today.
- **Fit.** The line is cut to its column with `FitLine`. The hint after ` · ` goes first, then the
  word `focus:`. The focused word is the last thing kept. At the narrowest frame busmon draws,
  the line still says `groups`, `tabs` or `keys`.
- **What it follows.** The line is drawn from `s.keys` and the layout (`side`) at each render, the
  same two facts the lit marks use. So it can never disagree with them.
- **While a key is being edited, or a choice is open,** the focus is on the keys, and the line
  says `keys`. The editor keeps its own hints. This change adds none, and the line's hint stays
  the panel hint.
- **Scrolling.** The line is body row 0, as the margin is today. It scrolls with the body, like the
  margin and the narrow tab bar. When the groups have the focus, the selection is a group, the
  body is at its top, and the line is on screen. When the keys have it on a short page, the line
  can scroll away. The lit `▶` on the key is then on screen, and `p.top` brings the line back with
  the first key. Keeping the line pinned would need a fixed head that no other page has. That is
  more than this line needs, and it is left out.
- **What does not change:**
  - the key table (`settingsBindings`), the footer's `panel` and `focus` hints, the lit marks and
    `frameFade` on the tab;
  - the title (`settingsTitle`) and `y` (the page copies nothing);
  - `selLine`'s arithmetic: the margin row is already counted (`selLine++`).

## 3. Code (for the impl task, not this branch)

- `cmd/busmon/settings_ui.go`:
  - `settingsFocusLine(keys, side bool, width int) Line` builds the line.
  - `settingsBody` puts it in place of the blank margin: in the panel column on the wide branch;
    on the narrow branch, as the first of its three leading rows.
- **Tests (first):**
  - The four texts (groups or keys, wide or narrow) come from `settingsBody`'s first row.
  - Toggling with `tab`, `→`/`←` (wide) and `↓`/`↑` on the first key (narrow) changes the word.
  - The plain text is the same with the colour off.
  - At width 40 the focused word is still there.
  - The first group and key rows keep their line numbers, and `selLine` is unchanged.
- **Goldens:** the settings goldens (`settings-*-dark/light`) and the `settings`/`settings-narrow`
  overlays are regenerated. Only row 0 of the page changes.
- **Docs:** the settings paragraph in `docs/operator-notes.md`, at "two panels below one blank row
  of top margin", says the row is the focus line now.

## 4. Rejected

- **The focus in the frame title** (`Settings · … · focus: keys`). The title is the counts' line,
  and on a narrow page it already loses its counts to the width. A changing word there would push
  them out at each toggle.
- **A header over each panel** (`groups` over the sidebar, `keys` over the keys, the focused one
  lit). This works only on a wide page: an 18-cell sidebar header says the same as the selected
  group's accent, and again only in colour.
- **A pinned row** (see Scrolling). No page has one; the cost is out of proportion to the line.
