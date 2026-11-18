# Braille sparklines: a graph style setting, with mock-ups

Status: r3, built (subject `braille-sparklines`). r3: the person's answers (Q1 one line, Q2 filled) and
their addition, that any non-zero value lights at least the bottom dot (§4); the locale helper is
`config.Locale`, importable (§4). Before: spec, r2, for review (subject
`braille-sparklines-spec`). r2: reviewer-codex (the tests pin
every pair of the locale's precedence), foureyes (the live change
needs its `applySettings` case; one locale helper shared with the language setting). Not built. Base: origin/dev
`d920cede`.
Author: coder, from master's directive of 2026-11-18 relaying the person's yes to braille
(sparklines only, not gauges) after the `braille-graphs-feasibility` check. The person's open
choices are §6; the build is §7.

## 1. What exists today

- **One sparkline in busmon:** the Tokens page (U), Projects zone, column `7 days`.
  - One cell per day, 7 cells, drawn by `sparkOf` in `cmd/busmon/tokens_view.go` from
    `sparkSteps` (same file), the eight lower blocks `▁▂▃▄▅▆▇█` (the `GlyphSpark*` constants in
    `theme.go`).
  - Each day is scaled against the zone's largest day (`maxDay`), in `RoleOK`. A day with no tokens
    is a space.
- **The data is daily.** `{p}:tokens` holds one row per day, agent and provider (`tokens.LastDays`,
  `tokens.ProjectDays`). There is nothing finer than a day to plot.
- **No graph setting.** Every block glyph is drawn whatever the terminal or the locale.

## 2. The setting

- `display.graphs` in the registry (`config/keys.go`): `auto | block | braille | ascii`,
  **default `auto`**.
  - Env `AGENT_BUS_BUSMON_GRAPHS`, written through `config.EnvName`, like `display.icons`.
  - Read by busmon only. Shown in the settings screen (Shift+C), in the Display group.
  - It governs the **sparklines only**. The gauges and bars (accounts, the per-agent bars, the
    `░█` total bar) keep their blocks whatever the value. That is the person's scope: sparklines,
    not gauges.
- **What `auto` resolves to:**
  - **`ascii`** when the locale is not UTF-8. The locale is the first non-empty of `LC_ALL`,
    `LC_CTYPE`, `LANG`. It is UTF-8 when that value names `UTF-8` or `utf8`, in any case.
    `C`, `POSIX`, and no variable set at all, are not UTF-8.
  - **`block`** when `TERM=linux`. The Linux console's fonts have the blocks, not braille.
  - **`block`** otherwise.

  `auto` never gives braille: braille is drawn only when the person chooses it. `NO_COLOR` plays
  no part, because it is about colour, not glyphs.
- **An explicit value wins:** `braille` with `TERM=linux` draws braille. The person asked for it,
  and the settings screen can set it back.
- **An unknown value** is `auto`, with the registry's usual settings warning.
- **Changed while busmon runs:** applied from the next frame on, as `display.icons` is. busmon
  applies a changed setting only through its own case in `applySettings` (`settings_ui.go`); a
  key without one answers "saved: next start of busmon". The build adds a `display.graphs` case
  (`cur.Graphs = ns.Graphs`, answering "graphs applied and saved").

## 3. The styles, side by side

Sample week, two projects, same scale as busmon (each day against the zone's largest day). The
agent-bus-monitor values, oldest first: 0, 1.2M, 3.4M, 0.4M, 5.1M, 2.2M, 4.0M. The myproject
values: 0.3M, 0, 0, 0.9M, 0.6M, 0.1M, 1.4M. Width 90 or more. The glyphs below were produced by
the encoder §4 describes, not drawn by hand.

**block** (today; 8 levels, one cell per day, 7 cells):

```
Projects                   in      out  7 days
  agent-bus-monitor        4.0M     360k   ▂▆▁█▄▇  ████████     16.3M
  myproject                1.4M     125k  ▁  ▂▁▁▃  ██░░░░░░      3.3M
```

**ascii** (8 levels, `_.-:=+*#`, one cell per day):

```
Projects                   in      out  7 days
  agent-bus-monitor        4.0M     360k   .+_#:*  ████████     16.3M
  myproject                1.4M     125k  _  .__-  ██░░░░░░      3.3M
```

**braille, 1 line, filled** (4 levels, two days per cell, 4 cells, padded to the column's 7):

```
Projects                   in      out  7 days
  agent-bus-monitor        4.0M     360k  ⢀⣆⣧⡇     ████████     16.3M
  myproject                1.4M     125k  ⡀⢀⣀⡄     ██░░░░░░      3.3M
```

**braille, 1 line, dots** (4 levels, only the top dot of each day):

```
Projects                   in      out  7 days
  agent-bus-monitor        4.0M     360k  ⢀⢂⠡⠁     ████████     16.3M
  myproject                1.4M     125k  ⡀⢀⣀⠄     ██░░░░░░      3.3M
```

**braille, 2 lines, filled** (8 levels, two days per cell, each project two rows high):

```
Projects                   in      out  7 days
                                          ⠀⡄⡇⡆
  agent-bus-monitor        4.0M     360k  ⢠⣇⣿⡇     ████████     16.3M
                                          ⠀⠀⠀⠀
  myproject                1.4M     125k  ⡀⢠⣀⡆     ██░░░░░░      3.3M
```

**braille, 2 lines, dots** (8 levels, top dot only):

```
Projects                   in      out  7 days
                                          ⠀⠄⠁⠂
  agent-bus-monitor        4.0M     360k  ⠠⢀⠈⠀     ████████     16.3M
                                          ⠀⠀⠀⠀
  myproject                1.4M     125k  ⡀⠠⣀⠂     ██░░░░░░      3.3M
```

What the mock-ups show:
- **1 line loses half the levels.** With 4 levels, 5.1M and 4.0M draw the same column, and so
  do 1.2M and 0.4M. Block tells all of them apart.
- **2 lines keep the 8 levels** but double the height of every Projects row. The numbers sit on
  one row and the graph's top half floats above them. A zone of 6 projects then takes 12 rows,
  which a small screen does not have.
- **Dots read poorly at 7 points.** A dotted line needs many points to look like a line. With 7
  it is a scatter, and a small day vanishes into a single dot. Filled columns keep each day's
  weight visible.
- **Braille is narrower:** 4 cells against 7. The column keeps its 7 cells (padded with blanks)
  so the bar and the total stay aligned whatever the style.

## 4. How it is drawn

- **A small in-house encoder, no new dependency** (the feasibility check's recommendation). A
  braille cell is `U+2800` plus a bitmask:

  | dot | left column | right column |
  |---|---|---|
  | top row | `0x01` | `0x08` |
  | 2nd row | `0x02` | `0x10` |
  | 3rd row | `0x04` | `0x20` |
  | bottom row | `0x40` | `0x80` |

  - Day `2i` is the left column of cell `i`, and day `2i+1` the right. An odd count leaves the
    last right column empty.
  - Level `k` (1 to 4 per row of cells) sets the bottom `k` dots, or only the `k`-th when dotted.
  - A day with no tokens has no dot. The empty cell is `U+2800`, the blank braille glyph, so the
    column keeps its width in every font.
- **One locale helper, shared:** `auto` reads the locale through `config.Locale(getenv, category)`
  (as built, in `config/locale.go`: an importable package, so busmon, agentbus's `--help` and the
  language setting's `i18n` package can all call it; reviewer-codex), which returns the first non-empty of `LC_ALL`, the category's variable, and `LANG`. Here the
  category is `LC_CTYPE`; the language setting (approved spec, not built) reads `LC_MESSAGES`
  through the same helper. Whichever of the two builds lands first adds it, the second reuses it,
  so the precedence cannot drift between them. No locale reading exists in busmon today.
- **The scale is unchanged:** a level is `int(v/max*n)` clamped to `n-1`, plus one, so any
  non-zero day shows at least its lowest step. `n` is 8 for block, ascii and 2-line braille, and
  4 for 1-line braille.
- **Any non-zero value lights at least the bottom dot** (the person's addition, with Q1 and Q2):
  however small a day is next to the zone's largest, it is level 1, never the blank of a day with
  no tokens, so a small day never looks like zero. `TestSparkLevel` pins it (a value of 1e-9 of
  the maximum, and 1 token against 5.1M, are level 1).
- **One function per page:** `sparkLine(vals, max, style) string` replaces `sparkOf` (as built: one
  line, Q1, so one string; the spec's `sparkLines` returned a row per line for 2-line braille). It
  returns one string per row (one row, or two for 2-line braille). The Projects loop prints each
  row, the upper ones with the name and number columns blank.
- **Width:** braille and the blocks are single-width in `Width` (uniseg), so no alignment
  code changes. A test pins `Width` of each braille glyph used to 1.
- **Colour:** unchanged, `RoleOK` for the whole cell. A cell holds two days, so per-day colour is
  not possible in braille, and today there is none to keep.

## 5. Tests

- **The encoder:** a table of levels to braille code points (each dot, filled and dotted, an
  odd count, zeros), and the round trip from a cell back to its two levels.
- **`auto`:** each case of §2:
  - no variable set, `C`, `POSIX`: ascii; `LANG=fr_FR.UTF-8`: block; `utf8` in lower case: block;
  - each conflicting pair, so the whole precedence is pinned:
    - `LC_ALL=C` with `LC_CTYPE=en_US.UTF-8`: ascii (`LC_ALL` over `LC_CTYPE`);
    - `LC_ALL=C` with `LANG=en_US.UTF-8`: ascii (`LC_ALL` over `LANG`);
    - `LC_CTYPE=C` with `LANG=fr_FR.UTF-8`: ascii (`LC_CTYPE` over `LANG`);
    - `LC_CTYPE=en_US.UTF-8` with `LANG=C`: block;
  - `TERM=linux` with `LANG=en_US.UTF-8`, so the case reaches the terminal rule rather than the
    locale's: block. An explicit `braille` under the same pair stays braille.
- **Goldens:** every existing golden stays as it is. The harness pins `display.graphs = block`, as
  `pinLocal` pins the time zone, so a test machine's locale changes nothing. New goldens: the
  Tokens page in `braille` and in `ascii`, at 80 and 140 columns.
- **An unknown value:** `display.graphs = dots` resolves as `auto` and the settings screen shows
  the registry's warning for it.
- **Live change:** on the settings page (the `selectSetting` pattern of `settings_page_test.go`,
  as for `display.icons`), set `display.graphs` from `block` to `braille`: the answer is "graphs
  applied and saved" and the next Tokens frame draws braille, without a restart.
- **Gauges untouched:** a test renders the Tokens page in `ascii` and checks that the account
  bars still draw their blocks.

## 6. Questions for the person (answered)

The person answered on 2026-11-18: **Q1 one line**, **Q2 filled**, and added that any non-zero value
lights at least the bottom dot (§4).

- **Q1. One line or two?** Recommended: **one line**, the same height as today. The cost is 4
  levels instead of 8: close days look the same (§3). Two lines keep 8 levels but double every
  Projects row.
- **Q2. Filled or dots?** Recommended: **filled**. With 7 daily points a dotted line reads as
  scattered dots. Filled columns keep each day's weight, as the blocks do.

One consequence of the scope, for the person to see: on a locale that is not UTF-8, `auto` draws
the sparklines in ASCII, while the gauges keep their blocks, which such a terminal may not draw
either. Sparklines only was the person's choice; the gauges could follow later if wanted.

## 7. Steps

One PR, from origin/dev, base dev, after the person's answers:
- the key in `config/keys.go`, a hot file: master schedules the slot;
- the encoder and `auto`'s resolution in a new `cmd/busmon/graphs.go`;
- `tokens_view.go` calling `sparkLines`;
- the settings screen entry and its `display.graphs` case in `applySettings`;
- `config.Locale`, unless the language setting's build has added it already;
- the harness pin and the goldens;
- an operator-notes fragment.

About a day.

**Overlap with coder2.** The views redesign (#601) does not change the Tokens page. The change in
`tokens_view.go` is a few lines in the Projects loop. If the redesign moves that loop, this PR
rebases onto it: master orders the two.
