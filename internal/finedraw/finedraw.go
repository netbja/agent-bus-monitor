// Package finedraw turns a value into cells: busmon's bars, gauges and
// sparklines (tui-rendering spec §2, docs/superpowers/specs/2026-11-25-busmon-fine-rendering-design.md).
// It owns their glyphs and their geometry, and knows nothing of busmon's
// theme: it returns segments tagged with a Part, which the caller maps to
// its own roles, and the caller keeps any animation (it asks for the frame's
// value). Step 1 moved the existing drawing here unchanged.
package finedraw

import (
	"math"
	"strings"
)

// The glyphs, each one terminal cell (TestGlyphWidths).
const (
	Full     = "█" // a whole cell
	Empty    = "░" // an empty cell of a whole-cell bar
	Gauge    = "▇" // a whole-cell gauge's filled cell
	Thin     = "━" // the thin gauge, its fill and (in fine) its track
	ThinMark = "╋" // the thin gauge's pace mark inside the fill
	Line     = "─" // the thin gauge's track without colour
	Pace     = "│" // the thin gauge's pace mark on the track

	Eighth1 = "▏" // one to seven eighths of a cell, filled from the left
	Eighth2 = "▎"
	Eighth3 = "▍"
	Eighth4 = "▌"
	Eighth5 = "▋"
	Eighth6 = "▊"
	Eighth7 = "▉"

	Spark1 = "▁" // a sparkline's lower steps, Gauge and Full its two highest
	Spark2 = "▂"
	Spark3 = "▃"
	Spark4 = "▄"
	Spark5 = "▅"
	Spark6 = "▆"
)

// Eighths are the partial cells by eighths filled, "" for none.
var Eighths = [8]string{"", Eighth1, Eighth2, Eighth3, Eighth4, Eighth5, Eighth6, Eighth7}

// Part is what a segment is, for the caller's colours.
type Part int

const (
	PartFill   Part = iota // filled cells
	PartEdge               // the partial cell, drawn on the track
	PartTrack              // the empty part of a fine bar (the track)
	PartEmpty              // the empty part of a whole-cell bar (Empty, or ─)
	PartPace               // the pace mark on the track
	PartFillMk             // the pace mark inside the fill
)

// Seg is a run of cells of one Part. Lead marks the leading filled segment
// (the one a sliding bar lifts).
type Seg struct {
	Text string
	Part Part
	Lead bool
}

func clamp(v, lo, hi int) int { return max(lo, min(v, hi)) }

// BarEighths is pct of cells×8 steps: a non-zero value never reads empty and
// a value under 100 never reads full (a full bar means the window is spent).
func BarEighths(pct float64, cells int) int {
	steps := cells * 8
	e := clamp(int(math.Round(pct*float64(steps)/100)), 0, steps)
	if pct > 0 && e == 0 {
		e = 1
	}
	if pct < 100 && e == steps {
		e = steps - 1
	}
	return e
}

// Bar is a gauge of cells at pct. fine: whole cells and one eighth on a
// track (spaces), the leading filled segment marked Lead; else whole Gauge
// cells, rounded, then Empty.
func Bar(pct float64, cells int, fine bool) []Seg {
	if !fine {
		n := clamp(int(pct*float64(cells)/100+0.5), 0, cells)
		return []Seg{{Text: strings.Repeat(Gauge, n), Part: PartFill}, {Text: strings.Repeat(Empty, cells-n), Part: PartEmpty}}
	}
	e := BarEighths(pct, cells)
	full, part := e/8, e%8
	var l []Seg
	switch {
	case part > 0:
		l = append(l, Seg{Text: strings.Repeat(Full, full), Part: PartFill}, Seg{Text: Eighths[part], Part: PartEdge})
	case full > 0:
		l = append(l, Seg{Text: strings.Repeat(Full, full-1), Part: PartFill}, Seg{Text: Full, Part: PartFill})
	}
	if len(l) > 0 {
		l[len(l)-1].Lead = true
	}
	used := full
	if part > 0 {
		used++
	}
	return append(l, Seg{Text: strings.Repeat(" ", cells-used), Part: PartTrack})
}

// ThinCells is the thin gauge, one segment per cell: Thin filled cells (the
// leading one Lead), the rest the track (Thin in fine, Line without), and a
// pace mark at pace % of the cells (< 0 none): Pace on the track, ThinMark
// inside the fill. Whole cells: a non-zero value fills one, a value under
// 100 never fills all.
func ThinCells(pct, pace float64, cells int, fine bool) []Seg {
	filled := clamp(int(math.Round(pct*float64(cells)/100)), 0, cells)
	if pct > 0 && filled == 0 {
		filled = 1
	}
	if pct < 100 && filled == cells {
		filled = cells - 1
	}
	mark := -1
	if pace >= 0 {
		mark = clamp(int(math.Round(pace*float64(cells)/100)), 0, cells-1)
	}
	track := Thin
	if !fine {
		track = Line
	}
	out := make([]Seg, 0, cells)
	for i := 0; i < cells; i++ {
		switch {
		case i == mark && i >= filled:
			out = append(out, Seg{Text: Pace, Part: PartPace})
		case i < filled:
			s := Seg{Text: Thin, Part: PartFill, Lead: i == filled-1}
			if i == mark {
				s.Text, s.Part = ThinMark, PartFillMk
			}
			out = append(out, s)
		default:
			out = append(out, Seg{Text: track, Part: PartEmpty})
		}
	}
	return out
}

// Blocks is the plan's bar: Full cells and one eighth (rounded, no floor or
// ceiling), then Empty.
func Blocks(pct float64, cells int) []Seg {
	e := clamp(int(pct*float64(cells)*8/100+0.5), 0, cells*8)
	full, part := e/8, e%8
	empty := cells - full
	if part > 0 {
		empty--
	}
	return []Seg{{Text: strings.Repeat(Full, full) + Eighths[part], Part: PartFill}, {Text: strings.Repeat(Empty, max(0, empty)), Part: PartEmpty}}
}

// Rule is a line of cells Thin, fill of them filled: the progress bar.
func Rule(fill, cells int) []Seg {
	fill = clamp(fill, 0, cells)
	return []Seg{{Text: strings.Repeat(Thin, fill), Part: PartFill}, {Text: strings.Repeat(Thin, cells-fill), Part: PartEmpty}}
}
