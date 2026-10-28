package main

import (
	"math"
	"strings"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// The account gauges (spec docs/superpowers/specs/2026-10-29-busmon-bars-design.md):
// in true colour a bar has eight steps per cell, full-height blocks on a
// track, and slides to a new value in barSlideFor; the 16-colour and
// no-colour palettes keep whole steps and never move.

// barSlideFor is how long a bar takes to reach a new value.
const barSlideFor = 500 * time.Millisecond

// barLift is how far a sliding bar's leading cell goes towards the text colour.
const barLift = 60

var eighthGlyphs = [8]string{"", GlyphEighth1, GlyphEighth2, GlyphEighth3, GlyphEighth4, GlyphEighth5, GlyphEighth6, GlyphEighth7}

// eighths is pct of cells×8 steps: a non-zero value never reads empty and a
// value under 100 never reads full (a full bar means the window is spent).
func eighths(pct float64, cells int) int {
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

// gaugeSegs is a bar of cells at pct in role: eighths on a track in true
// colour, the leading cell tinted when lead (the bar is sliding); today's
// whole steps otherwise.
func gaugeSegs(pct float64, cells int, role Role, lead bool) Line {
	if !th.Animated() {
		n := clamp(int(pct*float64(cells)/100+0.5), 0, cells)
		return Line{{Text: strings.Repeat(GlyphGauge, n), Role: role}, {Text: strings.Repeat(GlyphBarEmpty, cells-n), Role: RoleDim}}
	}
	e := eighths(pct, cells)
	full, part := e/8, e%8
	var l Line
	switch {
	case part > 0:
		l = append(l, Seg{Text: strings.Repeat(GlyphBarFull, full), Role: role},
			Seg{Text: eighthGlyphs[part], Role: role, Track: true})
	case full > 0:
		l = append(l, Seg{Text: strings.Repeat(GlyphBarFull, full-1), Role: role},
			Seg{Text: GlyphBarFull, Role: role})
	}
	if lead && len(l) > 0 {
		l[len(l)-1].Lift = barLift
	}
	used := full
	if part > 0 {
		used++
	}
	return append(l, Seg{Text: strings.Repeat(" ", cells-used), Role: RoleDim, Track: true})
}

// barSlide is one bar's way from one value to the next, started at at.
type barSlide struct {
	from, to float64
	at       time.Time
}

// barKey names a bar: provider and window, without the reset, so a window
// that resets slides down.
func barKey(provider, window string) string { return provider + "|" + window }

// barTargets is every bar's value in a snapshot: the dispatcher's windows,
// or a legacy snapshot's session and week. Unknown and unlimited have none.
func barTargets(budgets map[string]bus.BudgetSnapshot) map[string]float64 {
	out := map[string]float64{}
	for p, b := range budgets {
		switch {
		case b.Unknown || b.Unlimited:
		case len(b.Windows) == 0:
			out[barKey(p, "session")], out[barKey(p, "week")] = b.SessionPct, b.WeeklyPct
		default:
			for _, w := range b.Windows {
				out[barKey(p, w.Name)] = w.UsedPct
			}
		}
	}
	return out
}

// updateSlides is the bars after a poll at now: a changed value slides from
// where its bar is drawn when animate, a new one starts still, a missing one
// is dropped.
func updateSlides(old map[string]barSlide, targets map[string]float64, now time.Time, animate bool) map[string]barSlide {
	out := make(map[string]barSlide, len(targets))
	for k, v := range targets {
		prev, ok := old[k]
		switch {
		case !ok || !animate:
			out[k] = barSlide{from: v, to: v}
		case prev.to == v:
			out[k] = prev
		default:
			shown, _ := barView{slides: old, now: now}.at(k, prev.to)
			out[k] = barSlide{from: shown, to: v, at: now}
		}
	}
	return out
}

// barView is what View reads: the slides and the Model's now.
type barView struct {
	slides map[string]barSlide
	now    time.Time
}

// at is the value to draw for key, whose reading is pct, and whether its bar
// moves: ease-out from the slide's start; a slide that does not end on pct
// is not this reading's, so pct is drawn still.
func (v barView) at(key string, pct float64) (float64, bool) {
	s, ok := v.slides[key]
	if !ok || s.to != pct || s.at.IsZero() {
		return pct, false
	}
	p := float64(v.now.Sub(s.at)) / float64(barSlideFor)
	if p >= 1 {
		return pct, false
	}
	p = max(p, 0)
	return s.from + (s.to-s.from)*(1-math.Pow(1-p, 3)), true
}

// moving: a bar slides now.
func (v barView) moving() bool {
	for _, s := range v.slides {
		if !s.at.IsZero() && v.now.Sub(s.at) < barSlideFor {
			return true
		}
	}
	return false
}

// gauge is the bar for key at pct, as it is drawn now.
func (v barView) gauge(key string, pct float64, cells int, role Role) Line {
	shown, moving := v.at(key, pct)
	return gaugeSegs(shown, cells, role, moving)
}
