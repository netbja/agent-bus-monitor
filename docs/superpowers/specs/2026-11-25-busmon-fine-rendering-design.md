# Finer rendering in busmon (tui-rendering)

Status: spec r1, for review (subject `tui-rendering-spec`). Not built. Base: origin/dev `04c1755f`.
Author: architect, from the person's request relayed by master (task `tui-rendering-spec`, cmd
`1795608801015-0`). **No new dependency:** everything below uses what `go.mod` already holds
(bubbletea v2, lipgloss v2, `charmbracelet/colorprofile`, `go-colorful` indirect). No image and no
graphics protocol. The questions for the person are last (§7).

## 1. What busmon draws today

One theme (`cmd/busmon/theme.go`) gives every colour; the glyphs are constants there
(`theme.go:319-395`). Colour depth comes from `colorprofile.Detect` (`main.go:936`). **Any depth
below truecolor, 256 included, falls to the 16-colour palette** (`ResolveTheme`, `theme.go:96`),
and the finer effects (eighth cells, fades, track backgrounds) exist only in truecolor
(`Theme.Animated`, `theme.go:198`).

| # | Visual | Where | Today | Code |
|---|---|---|---|---|
| V1 | Freshness gauge, "updated" | Agents, wide tier, rows ≥ 110 columns | 10 dots ●/○, `full = 10 - age*10/10m`; then the age | `agents.go:358` `updatedCell` |
| V2 | 7-day sparkline | Tokens | 1 row, 14 cells (7 when tight), one colour (RoleOK), each row scaled to itself; block steps ▁…█ by default, braille only if `display.graphs = braille` | `tokens_view.go:229`, `graphs.go:63-124` |
| V3 | Total bars | Tokens | 20 cells (8 below 100 columns); truecolor: █ plus one eighth on a track; else ▇ and ░ | `tokens_view.go:104`, `bars.go:45` `gaugeSegs` |
| V4 | Account cards' window gauges | Tokens | thin gauge with pace mark | `account_cards.go:65` |
| V5 | Header quota gauges | header, wide tier | thin ━ on a faded ━ (truecolor) or ─, pace mark │/╋, 500 ms slide; 5 or 10 cells; green < 80 %, attention, error ≥ 95 % | `bars.go:79` `thinGauge`, `quota.go:79`, `header.go:347` |
| V6 | Progress line | Work list, Requests, Plan | 10 × ━, whole cells only, "~N%", "ends ~HH:MM", capped 95 % | `progress.go:77` `progressLine` |
| V7 | Plan bars | Plan | ━ summary bar; █/░ group bars; one eighth-cell bar | `planboard.go:236`, `plan_r2.go:258, 375` |
| V8 | Context fill | Agents (wide), agent detail | text only: tokens and "N%", attention colour from the alert threshold | `agents.go:445` `ctxCell`, `detail_agent.go:215` |
| V9 | Loading skeleton | Tokens | 24 × ░ with a fade | `tokens_view.go:95` |
| V10 | Idle screensaver, About | idle, About | ━ threads | `idleview_draw.go:418`, `about.go:333` |

**Not drawn anywhere:** a "watcher budget" gauge. The budgets busmon shows are the quota windows
(V4, V5). This spec reads the request's item 5 as those, plus the context fill (V8).

**The "In progress" kanban column** (`work_kanban.go:113`) shows no progress bar. The progress line
(V6) appears in the Work list, Requests and Plan only.

**Settings today:** `display.graphs` (auto, block, braille, ascii; `config/keys.go:152`; auto never
picks braille), `display.icons`, `display.theme`. Golden tests run at 60x20, 80x24, 140x40 and
140x60 (`tea_golden_test.go:26`); there is no 80x25.

## 2. One drawing package

**`internal/finedraw`**, the only code that turns a value into cells. The views call it; none draws
its own glyphs. It absorbs what exists, so nothing is drawn twice: `gaugeSegs` and `thinGauge`
(`bars.go`), the braille and block sparklines (`graphs.go`), `planBar` (`plan_r2.go:375`) and the
progress bar of `progressLine`.

- **`Caps`**, resolved once at start and on a settings change: colour depth (from
  `colorprofile`, as today), the glyph level (§3), and whether the locale is UTF-8.
- **`Bar(value, max, width, ramp)`**: whole cells █ and one eighth cell ▏▎▍▌▋▊▉, on a track. A value
  that is not zero never draws as empty: the first eighth shows.
- **`Thin(value, width, ramp, pace)`**: today's thin gauge, unchanged in look.
- **`Spark(series, width, rows, ramp)`**: braille, 2 samples per cell across and 4 dots per row,
  so 2 rows give 8 levels. One row stays available.
- **`Strip(segments, width, colours)`**: a timeline. A terminal cell takes one foreground and one
  background colour, so a cell carries at most two states: ▌ in the first state's colour on the
  second's background. 20 cells hold 40 samples.
- **`Ramp`**: a gradient, computed in RGB (green to amber to red for usage; a single hue from dim to
  strong for volumes). In truecolor it is drawn as is. In 256 colours, colorprofile's own
  downsampling maps it to the nearest colour. In 16 colours it is the three roles busmon uses
  today (RoleGauge, RoleAttention, RoleError) at the same thresholds. With `NO_COLOR`, there is no
  colour; the glyphs carry the value.

Every glyph the package uses has a width test, like `TestBrailleWidth` today (one column each).

## 3. Rendering level, and fallbacks

A new setting on the Shift+C page, **`display.rendering`**: `auto`, `fine` or `basic`
(PersonOnly is not needed: a display choice).

| | fine | basic | no UTF-8 locale |
|---|---|---|---|
| bars and gauges | █ + eighths, gradient | ▇ and ░, role colours (today's non-truecolor look) | `#` and `-` |
| sparklines | braille, 1 or 2 rows | block steps ▁…█, 1 row | `_.-:=+*#` (today's ascii) |
| timeline strip | ▌ half cells, 2 states per cell | █, 1 state per cell | one letter per cell: `w`, `b`, `.` |
| colour | as deep as the terminal: truecolor, 256 via downsampling, then 16 roles | 16 roles | as the terminal |

`auto` is `fine` when the locale is UTF-8, and `basic` otherwise (§7 Q1). Fonts cannot be
detected from Go: a person whose font lacks braille sets `basic`.

**`display.graphs` is folded in** (§7 Q4): `block` becomes `basic`, `braille` becomes `fine`, and
`ascii` stays what a non-UTF-8 locale forces. The old key keeps working for one release, mapped,
and its help text on the settings page says so.

**Octants and sextants** (U+1CD00, U+1FB00) are left out of the first build (§7 Q2): few fonts
carry them, octants least, and braille and eighths already give sub-cell detail.

## 4. Screen by screen

1. **Agents, "updated" (V1) → a state timeline.** Per agent, the last hour as a `Strip`: working
   (RoleOK), blocked (RoleError), waiting on the person (RoleAttention), idle (RoleDim), unknown
   (blank). The age stays beside it as text ("2m"). The window is a setting,
   `display.timeline_window` (15m to 4h, default 1h). Same column and tiers as today: ≥ 110
   columns; below that, the age alone.
   **Data:** the strip needs each agent's state changes over the window. The `{p}:status` stream
   is capped at about 1000 entries for all agents (`streamMaxLen`, `bus/stream.go:22`), so it may
   not hold an hour (§7 Q3). Step 4 measures it first.
2. **Tokens (V2, V3).**
   - The 7-day sparkline becomes 2 rows of braille, 28 cells wide when the row has room (so 2
     samples per day across), 14 otherwise, in a stronger colour (RoleAccent, a hue ramp in
     fine). Each row stays scaled to itself, and the row's maximum is printed beside it.
   - The total bars use `Bar` with a light gradient in every depth from 256 up, not only truecolor.
   - A project with no tokens in the 7 days is dimmed, and when there are more than three, they
     collapse into one line: "4 projects idle this week".
3. **Header (V5).** Unchanged in look. It moves onto `Thin`, so it shares the ramp and the
   thresholds with everything else.
4. **Work, "In progress".** Each card in the kanban column gets the progress line's bar (V6), short
   (6 cells), and the "~N%". The bar itself moves onto `Bar`, so it gains eighths: 10 cells give 80
   steps instead of 10. The progress-line rules stay: time-based, capped at 95 %, "estimate ~"
   with fewer than 5 samples, "longer than usual" past the usual length.
5. **Gauges for budgets and context (V4, V5, V8).**
   - Context fill becomes a 6-cell `Bar` with the green-to-red ramp, then "N%". The ramp's amber
     starts at the context alert threshold (`dispatch.context_alert`, 80), red at the urgent one
     (`dispatch.context_urgent`, 90).
   - The account cards' window gauges (V4) take the same ramp.
6. **Work, age heat (optional).** The waiting requests' age text is coloured by age: neutral under
   10 minutes, warm to an hour, hot past it. In 16 colours: dim, attention, error. Under `NO_COLOR`
   nothing changes. Setting `display.age_heat`, on by default (§7 Q5).
7. **Agents, sparkline per agent (optional).** A 7-cell braille sparkline of the agent's tokens
   per day over 7 days, from `{p}:tokens` (day, agent, provider rows, `bus/tokens.go:22`), which
   already holds it. PR throughput is left out: no per-agent merge series exists. Setting
   `display.agent_spark` (off, tokens), off by default.

**80x25 and width.** A golden size 80x25 is added for every screen, in fine and basic. Each visual
names its width at each tier (wide ≥ 120, medium ≥ 80, narrow), and below its minimum it gives way
to its text, as V1 does today.

## 5. Build steps

Each step is one PR into `dev`, with both reviews.

1. **`internal/finedraw`, behaviour unchanged.** Move `gaugeSegs`, `thinGauge`, the sparklines,
   `planBar` and the progress bar into it. Every golden file stays byte-identical.
2. **`display.rendering`,** `Caps`, the fallback table, the ramp in 256 colours, `display.graphs`
   mapped, and the 80x25 golden size.
3. **Bars and gauges:** Tokens (2-row sparkline, bars, idle projects), context fill, account cards,
   the progress bar with eighths, and the In progress cards.
4. **The Agents timeline:** the measurement of `{p}:status`, then the data source the person
   chooses (§7 Q3), `Strip`, and `display.timeline_window`.
5. **Optional:** age heat, and the per-agent sparkline.

## 6. Tests, and what this does not do

**Tests:** goldens for every screen at 60x20, 80x24, 80x25, 140x40 and 140x60, in fine and basic,
dark, light, 16 colours and `NO_COLOR`. `finedraw` unit tests: `Bar` at 0, at one eighth, at the
maximum and past it; `Spark` with 1 and 2 rows, an empty and a flat series; `Strip` with two states
in one cell; `Ramp` at each depth gives the expected colour or role. Width tests for every glyph
used. Step 1's PR changes no golden file.

**Not doing:** no image or graphics protocol, no new dependency, no animation beyond today's slide,
no octants or sextants in the first build, and no change to what the numbers mean.

## 7. Questions for the person

**Q1. The default rendering.**
- **(A) recommended: `auto`:** fine on a UTF-8 terminal, basic otherwise.
- (B) `fine` always; you switch to basic if a glyph is missing.
- (C) `basic` by default; fine is opt-in.
- Other.

**Q2. Octants and sextants.**
- **(A) recommended: not in the first build.** Braille and eighths give the detail; the fonts
  that carry octants are few.
- (B) sextants behind a third level, `dense`, falling back to braille.
- (C) octants and sextants, both behind `dense`.
- Other.

**Q3. Where the Agents timeline reads its history.**
- **(A) recommended: a small per-agent history,** written at each state change, keeping 4 hours
  (a new key kind, a few hundred entries per agent at most).
- (B) the `{p}:status` stream as it is; the strip shows "—" for the part it no longer holds.
- Other.

**Q4. The existing `display.graphs` setting.**
- **(A) recommended: folded into `display.rendering`,** mapped for one release.
- (B) kept separate: sparklines by `display.graphs`, the rest by `display.rendering`.
- Other.

**Q5. The two optional visuals.**
- **(A) recommended: age heat on by default, the per-agent sparkline off.**
- (B) both off by default.
- (C) both on.
- Other.
