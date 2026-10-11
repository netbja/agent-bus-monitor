package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

var hdrNow = time.Date(2026, 10, 10, 18, 26, 20, 0, time.UTC)

func fullHeader() headerData {
	return headerData{
		project:  "uidemo",
		health:   health{ok: true, since: hdrNow.Add(-time.Hour)},
		dispatch: "up",
		pilot:    "master",
		waiting:  "1 waiting · oldest 17s",
		budgets: map[string]bus.BudgetSnapshot{
			"anthropic": {SessionPct: 25, WeeklyPct: 44},
			"openai":    {SessionPct: 80, WeeklyPct: 91},
		},
		now: hdrNow,
	}
}

func joined(ls []Line) string {
	var parts []string
	for _, l := range ls {
		parts = append(parts, l.text())
	}
	return strings.Join(parts, "\n")
}

func TestHeaderLineWidths(t *testing.T) {
	for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
		for w := 1; w <= 200; w++ {
			d := fullHeader()
			d.project = strings.Repeat("p", 32)
			ls := headerLines(d, w, tier)
			want := 2
			if tier == TierWide {
				want = 1
			}
			if len(ls) != want {
				t.Fatalf("tier %d: %d lines", tier, len(ls))
			}
			for i, l := range ls {
				if l.Width() != w {
					t.Fatalf("tier %d width %d line %d: %d cells", tier, w, i, l.Width())
				}
			}
		}
	}
}

func TestHeaderAllocationOrder(t *testing.T) {
	for w := 30; w <= 200; w++ {
		for _, tier := range []Tier{TierWide, TierMedium, TierNarrow} {
			d := fullHeader()
			d.project = strings.Repeat("p", 32)
			first := headerLines(d, w, tier)[0].text()
			if !strings.Contains(first, GlyphWorking+" bus") || !strings.Contains(first, GlyphWorking+" disp") {
				t.Fatalf("w %d tier %d: health/dispatch missing from %q", w, tier, first)
			}
			if p := strings.Count(first, "p"); p > 0 && p < 5 { // ≥ 6 cells: 5 p + "…"
				t.Fatalf("w %d: project squeezed below 6 cells in %q", w, first)
			}
		}
	}
	wide := joined(headerLines(fullHeader(), 230, TierWide))
	for _, s := range []string{"uidemo", GlyphWorking + " bus ok", GlyphWorking + " dispatch up", "pilot master",
		"1 waiting", "anthropic used", "openai used", "18:26:20"} {
		if !strings.Contains(wide, s) {
			t.Fatalf("wide 230 header lacks %q: %q", s, wide)
		}
	}
	// Narrower: the clock goes first, then budgets from the right, before pilot.
	mid := joined(headerLines(fullHeader(), 150, TierWide))
	if strings.Contains(mid, "openai") && !strings.Contains(mid, "18:26:20") {
		t.Fatalf("clock dropped before a budget: %q", mid)
	}
	small := joined(headerLines(fullHeader(), 70, TierWide))
	if !strings.Contains(small, "pilot master") || strings.Contains(small, "openai") {
		t.Fatalf("w70: pilot must outlive budgets: %q", small)
	}
	if !strings.Contains(joined(headerLines(fullHeader(), 30, TierNarrow)), GlyphWorking+" bus") {
		t.Fatal("compact form missing at 30")
	}
}

func TestHeaderBusDown(t *testing.T) {
	d := fullHeader()
	d.health = health{ok: false, since: hdrNow.Add(-42 * time.Second), err: errors.New("dial tcp: refused").Error()}
	ls := headerLines(d, 160, TierWide)
	if !strings.Contains(ls[0].text(), GlyphBlocked+" bus down 42s") {
		t.Fatalf("bus down text: %q", ls[0].text())
	}
	for _, s := range ls[0] {
		if strings.Contains(s.Text, "bus down") && s.Role != RoleError {
			t.Fatalf("bus down role %v", s.Role)
		}
	}
	if !strings.Contains(joined(headerLines(d, 40, TierNarrow)), GlyphBlocked+" bus 42s") {
		t.Fatalf("compact bus down: %q", joined(headerLines(d, 40, TierNarrow)))
	}
}

func TestHeaderDispatchStates(t *testing.T) {
	for state, want := range map[string]string{"up": GlyphWorking + " dispatch up", "down": GlyphIdle + " dispatch down", "": "? dispatch"} {
		d := fullHeader()
		d.dispatch = state
		if got := headerLines(d, 160, TierWide)[0].text(); !strings.Contains(got, want) {
			t.Errorf("dispatch %q: %q lacks %q", state, got, want)
		}
	}
}

func TestHeaderBudgetUsedLabel(t *testing.T) {
	for pct, role := range map[float64]Role{74: RoleOK, 75: RoleAttention, 90: RoleError} {
		d := fullHeader()
		d.budgets = map[string]bus.BudgetSnapshot{"anthropic": {SessionPct: pct, WeeklyPct: 10}}
		l := headerLines(d, 200, TierWide)[0]
		found := false
		for _, s := range l {
			if strings.Contains(s.Text, GlyphGauge) {
				found = true
				if s.Role != role {
					t.Errorf("pct %v: first gauge role %v, want %v", pct, s.Role, role)
				}
				break
			}
		}
		if !found || !strings.Contains(l.text(), "used") {
			t.Errorf("pct %v: no gauge/used in %q", pct, l.text())
		}
	}
	if got := joined(headerLines(fullHeader(), 64, TierNarrow)); !strings.Contains(got, "anthropic 25%/44%") {
		t.Fatalf("narrow budget text: %q", got)
	}
}

func TestHeaderNoBudget(t *testing.T) {
	d := fullHeader()
	d.budgets = nil
	if got := joined(headerLines(d, 200, TierWide)); strings.Contains(got, "used") || strings.Contains(got, "%") {
		t.Fatalf("unknown budget rendered: %q", got)
	}
	d.pilot, d.waiting = "", ""
	if got := joined(headerLines(d, 200, TierWide)); !strings.Contains(got, "autonomous") || strings.Contains(got, "waiting") {
		t.Fatalf("autonomous / nothing waiting: %q", got)
	}
}

func TestHeaderHostileProject(t *testing.T) {
	d := fullHeader()
	d.project = "a\x1b[31m\tb"
	if got := headerLines(d, 120, TierWide)[0].text(); strings.ContainsAny(got, "\x1b\t") {
		t.Fatalf("control chars in header: %q", got)
	}
}

func TestHeaderMissionsStates(t *testing.T) {
	for state, want := range map[string]string{"up": GlyphWorking + " missions up", "down": GlyphIdle + " missions down"} {
		d := fullHeader()
		d.missions = state
		if got := headerLines(d, 200, TierWide)[0].text(); !strings.Contains(got, want) {
			t.Errorf("missions %q: %q lacks %q", state, got, want)
		}
		if got := joined(headerLines(d, 60, TierNarrow)); !strings.Contains(got, " miss") {
			t.Errorf("compact missions %q: %q", state, got)
		}
	}
	d := fullHeader()
	without := headerLines(d, 200, TierWide)[0].text()
	if strings.Contains(without, "missions") || strings.Contains(without, "miss") {
		t.Fatalf("absent missions still drawn: %q", without)
	}
}
