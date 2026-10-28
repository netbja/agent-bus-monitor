# busmon bars: eighth-cell gauges that slide (design, step 1: mock-up)

Status: mock-up for Bernard's validation (by ask). No code until he picks.
Task: busmon-bars (master's directive 1793222842434-0). Bernard chose "B, maquette d'abord";
his reference was bubbles/progress (an animated bar with a colour blend).

## What changes

Three horizontal gauges, all drawn the same way today (`GlyphGauge` `▇` filled, `GlyphBarEmpty`
`░` empty, whole cells, `n = round(pct × cells / 100)`):

| Where | Code | Cells |
|---|---|---|
| header, quota windows (dispatcher readings) | `quotaSeg`, `quota.go:126` | 10, or 5 for a window ≤ 6 h (`quotaBarCells`) |
| header, legacy budget (no dispatcher) | `budgetSeg`, `header.go:276` | 10 wide, 5 medium, none narrow |
| U page, account windows | `tokensBody`, `tokens_view.go:161` | 10 |

The U page's 24 h sparkline and 7-day chart are vertical (`sparkSteps`) and stay as they are.
No per-agent row draws a horizontal gauge today (the ctx cell is text: `88k 85%`), so none is
added here.

After:

1. **Eighth-cell precision** in true colour: `█` for a whole cell, then one of the seven left
   blocks `▏▎▍▌▋▊▉` (U+258F..U+2589) for the remainder. A 10-cell bar has 80 steps instead
   of 10. The seven glyphs join the closed set (`UIGlyphs`, `TestNoStrayNonASCIILiterals`).
2. **The 16-colour and no-colour palettes keep today's whole steps**, glyphs and all
   (`th.Animated()` is already the "true colour" test; the same test gates both 1 and 3).
3. **Colours are unchanged**: the fill keeps `budgetRole` (85/95 % alert roles), `RoleMuted`
   when aged, `RoleError` when held. No gradient across the bar.
4. **A slide**: when a value changes, the bar moves to it in 0.5 s (ease-out), true colour and
   `fade = true` only. The first reading and a busmon start never slide.
5. **No new dependency** (no bubbles/progress).

## Challenge: the height of `▇` (decision for Bernard)

`▇` is a *lower seven-eighths* block: today's filled cells stop short of the cell's top, which
keeps two bars on adjacent rows (the U page's account windows) apart. The left-eighth blocks are
*full height*: next to `▇` the partial cell is one eighth taller. And the empty `░` cannot fill
the rest of a partial cell, so a blank gap opens between the fill and the track. Two ways out:

**A — full-height bar on a track (recommended).** Whole cells become `█`; the empty part is a
background colour (the track: `RoleDim` faded, drawn as spaces), and the partial cell is its
left block in the fill colour **on the track colour**, so fill and track meet exactly. This
needs one new cell attribute: a background role on a `Seg` (`Seg.On Role`, painted by
`tea_grid.go`'s painter like `Pill`, which already sets a background). Cost: bars on adjacent
rows of the U page touch (one solid band per bar, like bubbles/progress), and the `░` texture
goes away in true colour.

**B — keep `▇` and `░`, eighths only at the edge.** No new attribute. The partial cell is one
eighth taller than its neighbours and leaves a gap of up to 7/8 cell before the first `░`.

Mock-ups below use these notations, since text cannot show a background:
`·` = a track cell in A (a space on the track colour); in a partial cell, the part after the
block is track too. B is shown with its real glyphs.

## The leading-edge tint (master's design, challenged)

Master proposed a lighter tint on the last filled cell. With eighth precision the leading edge
is already visible, so a static tint only restates it. Proposal: **no static tint**; while a bar
slides, its leading cell is drawn lighter (a blend of the fill towards `RoleText`, 60 %), and the
tint goes with the motion. That needs a second attribute next to `Fade` (`Seg.Lift int`, 1-100,
the blend towards the text colour; `Fade` blends towards `RoleLine`, which is darker on the dark
palette). If Bernard prefers no tint at all, nothing of it is built.

## Mock-up: 0..100 % at width 10

`pct → eighths = round(pct × 80 / 100)`, then clamped so that **a non-zero value never reads
empty and a value under 100 never reads full** (`1 ≤ e ≤ 79` for `0 < pct < 100`): a bar that
looks full means the window is spent, as `held` does.

| pct | eighths | A (true colour) | B (true colour) | whole steps (16-col, no colour; = today) |
|---:|---:|---|---|---|
| 0 | 0 | `··········` | `░░░░░░░░░░` | `░░░░░░░░░░` |
| 0.4 | 1 (clamp) | `▏·········` | `▏░░░░░░░░░` | `░░░░░░░░░░` |
| 5 | 4 | `▌·········` | `▌░░░░░░░░░` | `▇░░░░░░░░░` |
| 12 | 10 | `█▎········` | `▇▎░░░░░░░░` | `▇░░░░░░░░░` |
| 23 | 18 | `██▎·······` | `▇▇▎░░░░░░░` | `▇▇░░░░░░░░` |
| 41 | 33 | `████▏·····` | `▇▇▇▇▏░░░░░` | `▇▇▇▇░░░░░░` |
| 50 | 40 | `█████·····` | `▇▇▇▇▇░░░░░` | `▇▇▇▇▇░░░░░` |
| 62 | 50 | `██████▎···` | `▇▇▇▇▇▇▎░░░` | `▇▇▇▇▇▇░░░░` |
| 88 | 70 | `████████▊·` | `▇▇▇▇▇▇▇▇▊░` | `▇▇▇▇▇▇▇▇▇░` |
| 95 | 76 | `█████████▌` | `▇▇▇▇▇▇▇▇▇▌` | `▇▇▇▇▇▇▇▇▇▇` |
| 99.6 | 79 (clamp) | `█████████▉` | `▇▇▇▇▇▇▇▇▇▉` | `▇▇▇▇▇▇▇▇▇▇` |
| 100 | 80 | `██████████` | `▇▇▇▇▇▇▇▇▇▇` | `▇▇▇▇▇▇▇▇▇▇` |

At width 5 (a 5 h window) there are 40 steps: 62 % = 25 eighths = `███▏·` (A), against
`▇▇▇░░` today. The percentage after the bar is unchanged (`%.0f`), so 99.6 % still prints
`100%` next to a bar one eighth short: the bar is the stricter of the two, on purpose.

## Mock-up: a slide from 23 % to 41 % at width 10

The poll that reads 41 % starts the slide at `t = 0`; frames come from the existing `fadeMsg`
schedule (`fadeFrame` = 100 ms). Shown value = `from + (to − from) × (1 − (1 − p)³)`,
`p = elapsed / 500 ms`. `*` marks the leading cell drawn lighter (only if the tint is kept).

```
t (ms)  shown    eighths  A                        B
   0    23.0 %   18       session ██▎·······  23%  session ▇▇▎░░░░░░░  23%
 100    31.8 %   25       session ███▏*·····  41%  session ▇▇▇▏*░░░░░  41%
 200    37.1 %   30       session ███▊*·····  41%  session ▇▇▇▊*░░░░░  41%
 300    39.9 %   32       session ████*·····  41%  session ▇▇▇▇*░░░░░  41%
 500    41.0 %   33       session ████▏·····  41%  session ▇▇▇▇▏░░░░░  41%
```

(`*` stands on the leading block itself, not an extra cell; the 400 ms frame equals the 500 ms
one at 33 eighths and is omitted.) The number jumps to the new value at once; only the bar
moves, so the figure a person reads is never an interpolation. A fall (a window reset, 88 % →
3 %) slides down the same way. The role switches at once (a bar that crosses 85 % turns to the
alert colour on the first frame), as does held/aged.

Never animated: the first reading of a window (no previous value), every bar at busmon start
(the first poll seeds the values), an unknown reading (`?`), a window that changes key (new
provider or window name), the 16-colour and no-colour palettes, and `fade = false` in
`busmon.toml` (the existing setting: one switch for every motion).

## How it fits the Elm shell (step 2, if A or B is accepted)

- **State in the Model, set in Update.** `m.bars map[string]barSlide` keyed
  `provider|window` (the header and the U page share a key, so they move together):
  `{from, to float64; at time.Time}`. On each `pollMsg`, a key whose target differs from its
  `to` gets `from = shown(now)` (a slide interrupted by a new reading continues from where it is
  drawn), `to = new`, `at = m.now`; a key seen for the first time gets `from = to`. Keys no
  longer in the snapshot are dropped.
- **View stays pure:** the drawn value is `shown(slide, m.now)`, a function of Model state and
  the `m.now` that Update set. No clock in View.
- **Frames only while moving:** `animated()` gains "a slide has `m.now − at < 500 ms`"; the
  `fadeMsg` case already sets `m.now`, refreshes and reschedules, so nothing else is added.
- **One bar function** replaces the three copies: `gaugeSegs(pct float64, cells int, role Role,
  lead bool) []Seg`, eighths when `th.Animated()`, today's whole steps otherwise.

## What the tests see

- **Goldens:** every golden that draws a gauge renders in true colour, so all of these change
  (their `.ansi` twins too): `dashboard-{140x40,140x60}-{dark,light}`,
  `expanded-{140x40,140x60}-{dark,light}`, `reader-{140x40,140x60}-{dark,light}`,
  `compose-{140x40,140x60}-{dark,light}`, `requests-{120x40,190x44}`, `tour{1..5}-120x30`,
  `tokens-{80x44,120x44,190x50}` (26 text goldens). The fixtures' values (62 %, 41 %, 100 %)
  become `███▏·` / `████▏·····` / `█████` in A. Regenerated with `-update`, then read by eye.
- **Unit tests that spell today's bar:** `quota_test.go:51` and `:195` (`▇▇▇▇░`), and
  `header_test.go:128` (finds a gauge by `GlyphGauge`). They will check the eighths in true
  colour and keep today's string under `Palette16` (one case each).
- **New tests:** the 0..100 table above (both clamps); the slide's frames at 0/100/200/300/500 ms
  from a fixed `m.now`; no slide on the first reading, at start, on `?`, under `Palette16`, with
  `fade = false`; `animated()` false once every slide is over (no frame after the last).
- **Alignment:** the seven blocks are single-cell and in the same Unicode block as `▇` and `░`
  (already drawn by `TestShellAlignmentSuite`); `agreeWidths` is checked for each, and the suite
  runs every size × palette with a bar mid-slide.

## Cost in redraws

A slide is 5 extra frames (100 ms apart). The dispatcher replaces `{p}:budget` once a minute
and a window's value moves at most then, so with every bar moving at once it is still 5 frames
per minute, shared: all bars ride the one schedule (`m.sig.fading`). While an agent waits (the
waiting fade) or the U page shimmers, frames run anyway and a slide adds none. Each frame is
the same full `View` as a fade frame today (markdown bodies are cached, so a frame renders
none). Idle: zero extra frames.

## Questions for Bernard

1. A (full-height bar on a track, recommended) or B (keep `▇`/`░`, eighths at the edge)?
2. The leading-edge tint: only while sliding (proposed), always, or none?
3. The clamps (non-zero never empty, under 100 never full): keep?
