package main

import (
	"math"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// The account gauges (spec docs/superpowers/specs/2026-10-29-busmon-bars-design.md):
// in true colour a bar has eight steps per cell, full-height blocks on a
// track, and slides to a new value in barSlideFor; the 16-colour and
// no-colour palettes keep whole steps and never move.

// barSlideFor is how long a bar takes to reach a new value.
const barSlideFor = 500 * time.Millisecond

// thinTrackFade is the thin bar's track: its fill's colour at this intensity,
// between the line colour and the fill (Seg.Fade).
const thinTrackFade = 30

// barLift is how far a sliding bar's leading cell goes towards the text colour.
const barLift = 60

// gaugeSegs is a bar of cells at pct in role (finedraw.Bar, by the
// rendering's bar mode): eighths on a track in fine from 256 colours up,
// the leading cell tinted when lead (the bar is sliding); whole steps in
// basic, # and - in ascii.
func gaugeSegs(pct float64, cells int, role Role, lead bool) Line {
	var l Line
	for _, s := range finedraw.Bar(pct, cells, drawCaps().BarMode()) {
		seg := Seg{Text: s.Text, Role: role}
		switch s.Part {
		case finedraw.PartEdge:
			seg.Track = true
		case finedraw.PartTrack:
			seg.Role, seg.Track = RoleDim, true
		case finedraw.PartEmpty:
			seg.Role = RoleDim
		}
		if lead && s.Lead {
			seg.Lift = barLift
		}
		l = append(l, seg)
	}
	return l
}

// thinGauge is the header's account gauge (usage-bars, the person's A4,
// finedraw.ThinCells): the filled cells in role, the rest the track (in
// fine, ━ in the fill's colour faded in true colour, in the dim role at 256;
// a light ─ in the dim role in basic, so filled and empty differ without
// colour; - in ascii), and the pace mark (the share
// of the window gone; < 0 none): │ in the text colour on the track, ╋ in the
// fill's role inside the fill. lead lifts the leading filled cell (the bar
// is sliding).
func thinGauge(pct, pace float64, cells int, role Role, lead bool) Line {
	mode := drawCaps().BarMode()
	var l Line
	add := func(s Seg) {
		if n := len(l); n > 0 && l[n-1].Role == s.Role && l[n-1].Lift == s.Lift && l[n-1].Fade == s.Fade {
			l[n-1].Text += s.Text
			return
		}
		l = append(l, s)
	}
	for _, c := range finedraw.ThinCells(pct, pace, cells, mode) {
		s := Seg{Text: c.Text, Role: role}
		switch c.Part {
		case finedraw.PartPace:
			s.Role = RoleText
		case finedraw.PartEmpty: // the track: the bar's own colour faded (usage-cards), or dim
			if mode == finedraw.ModeFine && th.Animated() {
				s.Fade = thinTrackFade
			} else {
				s.Role = RoleDim
			}
		}
		if lead && c.Lead {
			s.Lift = barLift
		}
		add(s)
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

// rampSpec is a bar's colours (tui-rendering §2, §4): its gradient, drawn
// cell by cell where the caps allow RGB (fine, 256 colours up), and the
// thresholds whose band picks the whole fill's role otherwise (roles has one
// more than bands).
type rampSpec struct {
	g     finedraw.Gradient
	bands []float64
	roles []Role
}

// role is the role of a fill at pct: its band's.
func (r rampSpec) role(pct float64) Role {
	n := 0
	for _, b := range r.bands {
		if pct >= b {
			n++
		}
	}
	return r.roles[n]
}

// thresholdRamp is green, amber from amber, red from red: a threshold of 0
// or less is off, and red counts only above amber (the context escalation's
// rule for context_urgent).
func thresholdRamp(amber, red float64) rampSpec {
	r := rampSpec{g: finedraw.Stops{{Pct: 0, C: th.Hue(RoleGauge, 100)}}, roles: []Role{RoleGauge}}
	add := func(at float64, role Role) {
		r.g = append(r.g.(finedraw.Stops), finedraw.Stop{Pct: at, C: th.Hue(role, 100)})
		r.bands, r.roles = append(r.bands, at), append(r.roles, role)
	}
	if amber > 0 {
		add(amber, RoleAttention)
	}
	if red > 0 && red > amber {
		add(red, RoleError)
	}
	return r
}

// usageRamp is an account window's: budgetRole's thresholds.
func usageRamp() rampSpec { return thresholdRamp(80, 95) }

// volumeRamp is a volume's (tokens): role's hue from faint to whole, one
// role in 16 colours.
func volumeRamp(role Role) rampSpec {
	return rampSpec{g: finedraw.Ramp{th.Hue(role, volumeFaint), th.Hue(role, 100)}, roles: []Role{role}}
}

// volumeFaint is where a volume ramp starts: its hue this far from the line
// colour.
const volumeFaint = 45

// ink is the colour of a ramp's cell at place pct, none where the caps draw
// roles (basic, below 256 colours, no colour).
func (r rampSpec) ink(pct float64) (Color, bool) {
	s := drawCaps().Shade(r.g, pct, r.bands)
	if !s.UseRGB {
		return colorDefault, false
	}
	return newRGBColor(int32(s.RGB.R), int32(s.RGB.G), int32(s.RGB.B)), true
}

// inked colours l's filled cells (role, not faded) along r, each at its
// place in a bar of cells: a gradient, so a bar near its limit runs green to
// red. Without RGB, l is returned as it is (its fill in its band's role).
func inked(l Line, cells int, role Role, r rampSpec) Line {
	if _, ok := r.ink(0); !ok {
		return l
	}
	var out Line
	x := 0
	for _, s := range l {
		if s.Role != role || s.Fade > 0 {
			out = append(out, s)
			x += Width(s.Text)
			continue
		}
		for _, g := range s.Text {
			c := s
			c.Text = string(g)
			c.Ink, _ = r.ink((float64(x) + 0.5) * 100 / float64(cells))
			out = append(out, c)
			x++
		}
	}
	return out
}

// rampGauge is gaugeSegs coloured by r: the fill in the band role of pct,
// and cell by cell along the ramp where RGB is drawn.
func rampGauge(pct float64, cells int, r rampSpec, lead bool) Line {
	role := r.role(pct)
	return inked(gaugeSegs(pct, cells, role, lead), cells, role, r)
}

// thinRamp is the thin gauge for key at pct coloured by r (the account
// cards, tui-rendering §4.5).
func (v barView) thinRamp(key string, pct, pace float64, cells int, r rampSpec) Line {
	shown, moving := v.at(key, pct)
	role := r.role(pct)
	return inked(thinGauge(shown, pace, cells, role, moving), cells, role, r)
}
