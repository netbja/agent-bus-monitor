package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// usage-bars (Bernard's choice A4): the header's account gauge is a thin line,
// ━ in the level colour on ━ in the track colour, a pace mark │ at the share
// of the window already gone; 16 colours and no colour keep whole steps with
// a light ─ track, so filled and empty differ without colour.
func TestThinGaugeTable(t *testing.T) {
	rows := []struct {
		pct, pace   float64
		true, plain string
	}{
		{0, -1, "━━━━━━━━━━", "──────────"},
		{0.4, -1, "━━━━━━━━━━", "━─────────"},
		{23, -1, "━━━━━━━━━━", "━━────────"},
		{23, 40, "━━━━│━━━━━", "━━──│─────"},
		{79, 86, "━━━━━━━━━│", "━━━━━━━━─│"},
		{99.6, -1, "━━━━━━━━━━", "━━━━━━━━━─"},
		// Inside the fill the mark crosses it (foureyes): ╋ keeps the cell filled.
		{100, 100, "━━━━━━━━━╋", "━━━━━━━━━╋"},
		{10, 2, "╋━━━━━━━━━", "╋─────────"},
	}
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
		thin    bool
	}{{"dark", false, 1 << 24, true}, {"light", false, 1 << 24, true}, {"dark", false, 16, false}, {"dark", true, 1 << 24, false}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, r := range rows {
			want := r.plain
			if pal.thin {
				want = r.true
			}
			if got := lineText(thinGauge(r.pct, r.pace, 10, RoleOK, false)); got != want {
				t.Errorf("%s %d colours: %.1f%% pace %.0f = %q, want %q", pal.name, pal.colors, r.pct, r.pace, got, want)
			}
		}
	}
}

// The mark never hides the fill (foureyes on 26ef5d1): early in a window a
// low value still shows a filled cell, whatever the pace, in every palette.
func TestThinGaugeMarkNeverHidesTheFill(t *testing.T) {
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
	}{{"dark", false, 1 << 24}, {"dark", false, 16}, {"dark", true, 1 << 24}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, pct := range []float64{1, 10, 20} {
			for pace := 0.0; pace <= 20; pace++ {
				for _, cells := range []int{5, 10} {
					l := thinGauge(pct, pace, cells, RoleOK, false)
					filled := 0
					for _, s := range l {
						if s.Role == RoleOK {
							filled += strings.Count(s.Text, GlyphThinBar) + strings.Count(s.Text, GlyphThinMark)
						}
					}
					if filled == 0 || !strings.ContainsAny(lineText(l), GlyphThinMark+glyphVertical) {
						t.Errorf("%s %d: %.0f%% pace %.0f in %d cells = %q: no fill or no mark", pal.name, pal.colors, pct, pace, cells, lineText(l))
					}
				}
			}
		}
	}
}

// In true colour the fill keeps its level role and the rest the track's; the
// pace mark is text; a moving bar lifts its leading filled cell only.
func TestThinGaugeRoles(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	cells := func(l Line) []Seg {
		var out []Seg
		for _, s := range l {
			for _, r := range s.Text {
				out = append(out, Seg{Text: string(r), Role: s.Role, Lift: s.Lift})
			}
		}
		return out
	}
	c := cells(thinGauge(23, 40, 10, RoleError, false))
	for i, s := range c {
		switch {
		case i == 4:
			if s.Text != glyphVertical || s.Role != RoleText {
				t.Errorf("pace cell = %+v", s)
			}
		case i < 2:
			if s.Role != RoleError {
				t.Errorf("filled cell %d = %+v", i, s)
			}
		default:
			if s.Role != RoleLine {
				t.Errorf("track cell %d = %+v", i, s)
			}
		}
		if s.Lift != 0 {
			t.Errorf("a still bar lifts cell %d", i)
		}
	}
	lifted := 0
	for i, s := range cells(thinGauge(23, -1, 10, RoleError, true)) {
		if s.Lift > 0 {
			lifted++
			if i != 1 {
				t.Errorf("lift on cell %d, want the leading filled cell 1", i)
			}
		}
	}
	if lifted != 1 {
		t.Errorf("%d lifted cells", lifted)
	}
}

// The pace share: how much of the window is gone, from its reset and length;
// none when either is unknown.
func TestWindowElapsedPct(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	w := bus.BudgetWindow{Name: "5h", Minutes: 300, ResetsAt: now.Add(3 * time.Hour).Unix()}
	if got := windowElapsedPct(w, now); got < 39.9 || got > 40.1 {
		t.Fatalf("5h with 3h left = %.1f, want 40", got)
	}
	w7 := bus.BudgetWindow{Name: "7d", ResetsAt: now.Add(24 * time.Hour).Unix()} // length from its name
	if got := windowElapsedPct(w7, now); got < 85.6 || got > 85.8 {
		t.Fatalf("7d with 1d left = %.1f, want 85.7", got)
	}
	for name, w := range map[string]bus.BudgetWindow{
		"no reset":      {Name: "5h", Minutes: 300},
		"no length":     {Name: "billing", ResetsAt: now.Add(time.Hour).Unix()},
		"reset passed":  {Name: "5h", Minutes: 300, ResetsAt: now.Add(-time.Minute).Unix()},
		"reset too far": {Name: "5h", Minutes: 300, ResetsAt: now.Add(6 * time.Hour).Unix()},
	} {
		if got := windowElapsedPct(w, now); got != -1 {
			t.Errorf("%s = %.1f, want none (-1)", name, got)
		}
	}
	_ = strings.TrimSpace
}
