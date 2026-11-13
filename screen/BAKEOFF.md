# Emulator bake-off (herdr exit step 4)

Spec: `docs/superpowers/specs/2026-11-10-herdr-exit-design.md` §6.2, §6.4, §9, decision Q3
(gostty for tests only). Measured on 2026-11-12, Linux x86_64, Intel Xeon E-2276M, Go 1.26.

**Choice: `github.com/charmbracelet/x/vt` v0.1.0**, behind the `screen` seam (`screen.New`).
gostty stays the test oracle behind the `gostty` build tag; production is pure Go.

## Candidates

| Candidate | What it is | Code we would own |
|---|---|---|
| `charmbracelet/x/vt` v0.1.0 | a full emulator on `x/ansi` (already a dependency) | the seam's adapter, renderer, mode tracker: 475 lines (`screen/*.go`) |
| `hinshun/vt10x` (commit 5011da428d02, 2022) | a small emulator, a rune per cell | the same adapter shape |
| `danielgatis/go-vte` v1.0.11 | a parser only | a whole grid: cursor, wrap, scroll regions, alternate screen, modes, styles, graphemes. x/vt's grid on its parser is 2 861 non-blank, non-comment lines: rejected by §6.4 ("lines of emulator code we would own count against a candidate"), not built |
| `ironpark/gostty` (Ghostty's VT core) | a native Zig library loaded through purego | none, but not Go: the oracle only (Q3) |

## Fidelity

Two measures, both in `screen/bakeoff_test.go` (`go test ./screen -tags bakeoff -run TestBakeoff -v`):

- **Corpus vs the oracle**: 20 raw byte streams for what the clients do (Ink's erase-and-redraw,
  a CR spinner, wide glyphs `❯ › 🌒 日本語` and box drawing, a wide char at the right edge,
  autowrap and the pending-wrap column, SGR dim and colors, the alternate screen, synchronized
  output, a scroll region, insert and delete lines and chars, erase display, DECSC/DECRC,
  reverse index, tabs, combining marks, cursor moves, OSC title and hyperlink). Expected
  screens are gostty's, committed in `testdata/oracle` (`-tags gostty -update-oracle`).
- **Captured screens**: the 38 screen fixtures of `agentstate/testdata/screens` (real herdr
  reads of Claude Code, Codex, Kimi, Grok and agy, and the derived ones), replayed as bytes;
  agentstate's judgement (ready, busy, dialog, blocked and what it names) must be the same on
  the replayed screen as on the capture. This is §6.4's pass condition.

| Candidate | Corpus = oracle | Captured screens judged the same |
|---|---|---|
| **x/vt** | **19/20** | **38/38** |
| vt10x | 19/20 | 29/38: no faint attribute, so every Claude Code and Codex empty input (a dim placeholder) reads not ready |
| gostty | 20/20 (it is the oracle) | n/a: its bake-off adapter reads plain text only |

The one x/vt miss, `wide-at-right-edge`: a wide cluster starting in the last column is dropped
instead of wrapped to the next line. Ink pads its lines to the width, so a client rarely puts a
wide char there; it is listed in `knownDifferences` (screen_test.go), which fails the day it is
fixed. One more difference, outside the corpus: leaving the alternate screen (`?1049l`) restores
the cursor visibility saved on entering it, where xterm keeps visibility apart from DECSC;
`TestModes` pins the order that matters.

The 3b recordings (real client output under `agentbus term record`) join the corpus when they
exist: none is committed yet.

## Speed and memory

`go test ./screen -tags 'bakeoff gostty' -run '^$' -bench Bakeoff -benchmem`; the input is every
corpus case and every captured screen, replayed, repeated to 1 MiB, written in 4 KiB pieces to a
120x40 screen (a scroll-heavy load: every screen ends in newlines).

| Candidate | Write throughput | Allocated per MiB | Snapshot of 120x40 (ANSI) | Heap kept by one screen after 1 MiB |
|---|---|---|---|---|
| **x/vt** (through the seam) | **2.55 MB/s** | 70 MB, 595 k allocs | **0.26 ms**, 214 KB | **5.7 MiB** (4 MiB of it x/vt's parser data buffer); 5.4 MiB with a 5 000-line scrollback |
| vt10x | 16.9 MB/s | 17 MB, 1.6 M allocs | 0.20 ms, 233 KB | 0.16 MiB |
| gostty (native) | 103 MB/s | 1 KB | 0.65 ms (plain) | native memory, not on the Go heap |

What the numbers mean for the holder:

- **x/vt's cost is scrolling**: 68 % of the write time is `Buffer.DeleteLineArea`, which copies
  the whole screen, cell by cell, on every line that scrolls. A client redrawing in place (Ink,
  ratatui) scrolls little; 2.5 MB/s is far above what an agent writes between turns, but a
  `cat` of a large file costs about 0.4 s of CPU per MiB. The holder always reads output (a slow
  viewer never blocks a program, §4.4), so the emulator only adds CPU, never back-pressure.
- **x/vt's scrollback would cost about 12 KiB a 120-column line** (a cell is a string, a style of
  three color interfaces and a link), and it trims a full scrollback by copying the slice on every
  push: a 5 000-line scrollback (`holder.scrollback`'s default) would be ~60 MiB per terminal.
  The seam does not use it: after each write the scrolled-off lines move into a ring of rendered
  ANSI strings (about the line's length in bytes), x/vt's scrollback is cleared, and writes reach
  x/vt in 4 KiB pieces because `Clear` keeps the backing array of what one write pushed.
  x/vt gives the **alternate screen its own 10 000-line scrollback** too, which its API cannot
  reach (`Scrollback`, `ClearScrollback` and `SetScrollbackSize` act on the main screen):
  a full-screen program that scrolls would fill it (reviewer-codex). The seam sets it to one line
  at construction, reaching the private field by reflection (`vtalt.go`) and calling x/vt's
  own `SetScrollbackSize`; `TestAltScreenScrollbackIsBounded` (20 000 scrolled alt-screen lines:
  at most one kept, under 16 MiB of heap growth) fails if a new x/vt moves the field. It is the
  one place the seam depends on x/vt's internals, and the first thing to drop if x/vt gains an
  option for it.
- **Queries**: x/vt answers DA/DSR/DECRQM on an unbuffered pipe, which blocks a write until read;
  the seam drains it into `Replies()` for the holder to forward to the PTY (capped at 64 KiB).
- **Modes**: x/vt exposes the alternate screen and the cursor's visibility only; bracketed paste
  (`?2004`) and synchronized output (`?2026`) come from a mode tracker on the same stream.

## Why not the faster ones

- vt10x is 6.6 times faster but fails 9 of 38 captured screens (no dim, which two clients' ready
  signatures need) and has no grapheme clusters and no scrollback; fixing it is owning a fork.
- gostty is 40 times faster and exact, but it is a native library: decided against for
  production (Q3). It remains the oracle every candidate is held to.

If x/vt's scrolling cost shows in the holder's measurements (step 5's end-to-end tests), the
seam allows a swap without touching the holder or agentstate; the corpus, the captured screens
and the oracle are the gate any replacement must pass.
