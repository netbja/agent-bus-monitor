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

// thinGauge is the header's account gauge (usage-bars, the person's A4): one
// GlyphThinBar per cell, the filled ones in role, the rest the track (the
// line colour in true colour; a light ─ without it, so filled and empty
// differ without colour), and a pace mark at pace % of the cells (the share
// of the window gone; < 0 none): │ in the text colour on the track, ╋ in the
// fill's role inside the fill, so the mark never hides a filled cell. Whole
// cells: a non-zero value fills one, a value under 100 never fills all. lead
// lifts the leading filled cell (the bar is sliding).
func thinGauge(pct, pace float64, cells int, role Role, lead bool) Line {
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
	track, trackRole := GlyphThinBar, RoleLine
	if !th.Animated() {
		track, trackRole = glyphHorizontal, RoleDim
	}
	var l Line
	add := func(s Seg) {
		if n := len(l); n > 0 && l[n-1].Role == s.Role && l[n-1].Lift == s.Lift {
			l[n-1].Text += s.Text
			return
		}
		l = append(l, s)
	}
	for i := 0; i < cells; i++ {
		switch {
		case i == mark && i >= filled:
			add(Seg{Text: glyphVertical, Role: RoleText})
		case i < filled:
			s := Seg{Text: GlyphThinBar, Role: role}
			if i == mark {
				s.Text = GlyphThinMark
			}
			if lead && i == filled-1 {
				s.Lift = barLift
			}
			add(s)
		default:
			add(Seg{Text: track, Role: trackRole})
		}
	}
	return l
}

// windowMinutes is a window's length: its own, else the one its name says.
func windowMinutes(w bus.BudgetWindow) int {
	if w.Minutes > 0 {
		return w.Minutes
	}
	switch windowLabel(w) {
	case "5h":
		return 300
	case "7d", "7d-opus", "7d-sonnet", "7d-overage":
		return 7 * 24 * 60
	}
	return 0
}

// windowElapsedPct is how much of the window is gone at now, from its reset
// and its length; -1 when either is unknown or the reset is not within one
// length ahead (then the reading is not this window's).
func windowElapsedPct(w bus.BudgetWindow, now time.Time) float64 {
	minutes := windowMinutes(w)
	if minutes == 0 || w.ResetsAt == 0 {
		return -1
	}
	left := time.Unix(w.ResetsAt, 0).Sub(now)
	length := time.Duration(minutes) * time.Minute
	if left <= 0 || left > length {
		return -1
	}
	return 100 * float64(length-left) / float64(length)
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
// or a legacy snapshot's session and week. Unknown, unlimited and a window
// never observed have none.
func barTargets(budgets map[string]bus.BudgetSnapshot) map[string]float64 {
	out := map[string]float64{}
	for p, b := range budgets {
		switch {
		case b.Unknown || b.Unlimited:
		case len(b.Windows) == 0:
			out[barKey(p, "session")], out[barKey(p, "week")] = b.SessionPct, b.WeeklyPct
		default:
			for _, w := range b.Windows {
				if windowUnobserved(w) {
					continue
				}
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

// thin is the header's thin bar for key at pct, as it is drawn now, with
// its pace mark.
func (v barView) thin(key string, pct, pace float64, cells int, role Role) Line {
	shown, moving := v.at(key, pct)
	return thinGauge(shown, pace, cells, role, moving)
}

// gauge is the bar for key at pct, as it is drawn now.
func (v barView) gauge(key string, pct float64, cells int, role Role) Line {
	shown, moving := v.at(key, pct)
	return gaugeSegs(shown, cells, role, moving)
}
