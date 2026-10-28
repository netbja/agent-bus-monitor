package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// barText is a gauge as the spec's mock-up writes it: a track cell (a space
// on the track colour) reads `·`.
func barText(l Line) string {
	var b strings.Builder
	for _, s := range l {
		t := s.Text
		if s.Track {
			t = strings.ReplaceAll(t, " ", "·")
		}
		b.WriteString(t)
	}
	return b.String()
}

// The spec's 0..100 table at width 10: eighths in true colour, on a track,
// with both clamps; today's whole steps under 16 colours and no colour.
func TestGaugeEighthsTable(t *testing.T) {
	rows := []struct {
		pct           float64
		eighth, whole string
	}{
		{0, "··········", "░░░░░░░░░░"},
		{0.4, "▏·········", "░░░░░░░░░░"},
		{5, "▌·········", "▇░░░░░░░░░"},
		{12, "█▎········", "▇░░░░░░░░░"},
		{23, "██▎·······", "▇▇░░░░░░░░"},
		{41, "████▏·····", "▇▇▇▇░░░░░░"},
		{50, "█████·····", "▇▇▇▇▇░░░░░"},
		{62, "██████▎···", "▇▇▇▇▇▇░░░░"},
		{88, "████████▊·", "▇▇▇▇▇▇▇▇▇░"},
		{95, "█████████▌", "▇▇▇▇▇▇▇▇▇▇"},
		{99.6, "█████████▉", "▇▇▇▇▇▇▇▇▇▇"},
		{100, "██████████", "▇▇▇▇▇▇▇▇▇▇"},
	}
	for _, pal := range []struct {
		name    string
		noColor bool
		colors  int
		eighths bool
	}{{"dark", false, 1 << 24, true}, {"light", false, 1 << 24, true}, {"dark", false, 16, false}, {"dark", true, 1 << 24, false}} {
		withPalette(t, pal.name, pal.noColor, pal.colors)
		for _, r := range rows {
			want := r.whole
			if pal.eighths {
				want = r.eighth
			}
			l := gaugeSegs(r.pct, 10, RoleOK, false)
			if got := barText(l); got != want {
				t.Errorf("%s/%d: %.1f%% = %q, want %q", pal.name, pal.colors, r.pct, got, want)
			}
			if l.Width() != 10 {
				t.Errorf("%s/%d: %.1f%% is %d cells wide", pal.name, pal.colors, r.pct, l.Width())
			}
			for _, s := range l {
				if !pal.eighths && (s.Track || s.Lift > 0) {
					t.Errorf("%s/%d: %.1f%% draws a track or a tint without true colour", pal.name, pal.colors, r.pct)
				}
			}
		}
	}
	withPalette(t, "dark", false, 1<<24)
	if got := barText(gaugeSegs(62, 5, RoleOK, false)); got != "███▏·" {
		t.Errorf("62%% at width 5 = %q", got)
	}
}

// The fill keeps its role; the partial cell is drawn on the track, so fill
// and track meet; the leading cell is tinted only while the bar moves.
func TestGaugeRolesTrackAndTint(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	l := gaugeSegs(23, 10, RoleError, false)
	for _, s := range l {
		if s.Lift != 0 {
			t.Errorf("a still bar is tinted: %+v", s)
		}
		if strings.ContainsAny(s.Text, "█▎") && s.Role != RoleError {
			t.Errorf("the fill lost its role: %+v", s)
		}
		if strings.Contains(s.Text, "▎") && !s.Track {
			t.Errorf("the partial cell is not on the track: %+v", s)
		}
	}
	lifted := 0
	for _, s := range gaugeSegs(23, 10, RoleError, true) {
		if s.Lift > 0 {
			lifted++
			if s.Text != "▎" {
				t.Errorf("the tint is on %q, not the leading cell", s.Text)
			}
		}
	}
	if lifted != 1 {
		t.Errorf("%d tinted segments while moving, want 1", lifted)
	}
	for _, s := range gaugeSegs(50, 10, RoleOK, true) { // a whole last cell leads
		if s.Lift > 0 && s.Text != "█" {
			t.Errorf("the tint is on %q, want the last whole cell", s.Text)
		}
	}
	for _, s := range gaugeSegs(0, 10, RoleOK, true) {
		if s.Lift > 0 {
			t.Error("an empty bar has a tinted cell")
		}
	}
}

// The spec's slide: 23 % to 41 % at width 10, ease-out over 0.5 s, from the
// poll that read 41 %.
func TestBarSlideFrames(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	t0 := shellNow
	slides := updateSlides(nil, map[string]float64{"anthropic|5h": 23}, t0.Add(-time.Minute), true)
	slides = updateSlides(slides, map[string]float64{"anthropic|5h": 41}, t0, true)
	for _, f := range []struct {
		ms     int
		bar    string
		moving bool
	}{{0, "██▎·······", true}, {100, "███▏······", true}, {200, "███▊······", true}, {300, "████······", true}, {500, "████▏·····", false}, {900, "████▏·····", false}} {
		v := barView{slides: slides, now: t0.Add(time.Duration(f.ms) * time.Millisecond)}
		shown, moving := v.at("anthropic|5h", 41)
		if got := barText(gaugeSegs(shown, 10, RoleOK, false)); got != f.bar || moving != f.moving {
			t.Errorf("t = %d ms: %q moving %v, want %q moving %v", f.ms, got, moving, f.bar, f.moving)
		}
	}
	// A new reading during a slide continues from where the bar is drawn.
	mid := t0.Add(200 * time.Millisecond)
	shown, _ := barView{slides: slides, now: mid}.at("anthropic|5h", 41)
	slides = updateSlides(slides, map[string]float64{"anthropic|5h": 10}, mid, true)
	if s := slides["anthropic|5h"]; s.from != shown || s.to != 10 || !s.at.Equal(mid) {
		t.Errorf("interrupted slide = %+v, want from %.2f to 10 at %v", s, shown, mid)
	}
}

// Never animated: a first reading (a busmon start reads every bar for the
// first time), an unchanged value, a window gone (unknown), animation off
// (16 colours, no colour, fade = false: the caller passes false), and a
// value the view did not slide to.
func TestBarNeverSlides(t *testing.T) {
	t0 := shellNow
	first := updateSlides(nil, map[string]float64{"a|5h": 40}, t0, true)
	if _, moving := (barView{slides: first, now: t0}).at("a|5h", 40); moving {
		t.Error("the first reading slides")
	}
	same := updateSlides(first, map[string]float64{"a|5h": 40}, t0.Add(time.Minute), true)
	if same["a|5h"] != first["a|5h"] {
		t.Error("an unchanged value restarted its slide")
	}
	gone := updateSlides(first, map[string]float64{}, t0.Add(time.Minute), true)
	if _, ok := gone["a|5h"]; ok {
		t.Error("a window no longer read keeps its slide")
	}
	off := updateSlides(first, map[string]float64{"a|5h": 80}, t0.Add(time.Minute), false)
	if shown, moving := (barView{slides: off, now: t0.Add(time.Minute)}).at("a|5h", 80); moving || shown != 80 {
		t.Errorf("animation off: shown %.1f moving %v", shown, moving)
	}
	on := updateSlides(first, map[string]float64{"a|5h": 80}, t0.Add(time.Minute), true)
	if shown, moving := (barView{slides: on, now: t0.Add(time.Minute)}).at("a|5h", 60); moving || shown != 60 {
		t.Errorf("a value the slide does not end on: shown %.1f moving %v", shown, moving)
	}
	if shown, moving := (barView{}).at("a|5h", 33); moving || shown != 33 {
		t.Errorf("no slides: shown %.1f moving %v", shown, moving)
	}
}

// barTargets reads every window the header or the U page draws, under a key
// without its reset (a reset slides down), and the legacy session/week.
func TestBarTargets(t *testing.T) {
	got := barTargets(map[string]bus.BudgetSnapshot{
		"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 62, ResetsAt: 1}, {Name: "7d", UsedPct: 41}}},
		"legacy":    {SessionPct: 12, WeeklyPct: 30},
		"xai":       {Unknown: true, Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 9}}},
		"free":      {Unlimited: true},
	})
	want := map[string]float64{"anthropic|5h": 62, "anthropic|7d": 41, "legacy|session": 12, "legacy|week": 30}
	if len(got) != len(want) {
		t.Fatalf("targets %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("targets[%s] = %v, want %v", k, got[k], v)
		}
	}
}

// In the shell: a poll that moves a window schedules frames, only with fade
// on and true colour; the first poll never does; once the last slide is
// over, a frame schedules no other (when nothing else animates).
func TestShellBarSlideSchedule(t *testing.T) {
	pinLocal(t)
	calm := func() snapshot {
		s := shellFixture(shellNow)
		for _, a := range s.agents {
			a.state, a.askID, a.openAsk, a.obsPrompt, a.obsBusy = "idle", "", 0, false, false
		}
		return s
	}
	for _, c := range []struct {
		name   string
		fade   bool
		colors int
		noCol  bool
		want   bool
	}{
		{"fade on, true colour", true, 1 << 24, false, true},
		{"fade = false", false, 1 << 24, false, false},
		{"16 colours", true, 16, false, false},
		{"no colour", true, 1 << 24, true, false},
	} {
		withPalette(t, "dark", c.noCol, c.colors)
		m := newShell(shellConfig{project: "agent-bus-monitor", self: "bernard", settings: busmonSettings{Fade: c.fade}})
		m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
		m = pollAgain(t, m, calm(), shellNow)
		if m.sig.fading {
			t.Fatalf("%s: the first poll animates", c.name)
		}
		s := calm()
		w := s.budgets["anthropic"]
		w.Windows = append([]bus.BudgetWindow(nil), w.Windows...)
		w.Windows[0].UsedPct = 80
		s.budgets["anthropic"] = w
		at := shellNow.Add(time.Second)
		m = pollAgain(t, m, s, at)
		if m.sig.fading != c.want {
			t.Errorf("%s: a moved window scheduled frames %v, want %v", c.name, m.sig.fading, c.want)
		}
		if !c.want {
			continue
		}
		m = step(t, m, fadeMsg(at.Add(300*time.Millisecond)))
		if !m.sig.fading {
			t.Errorf("%s: a frame mid-slide scheduled no other", c.name)
		}
		m = step(t, m, fadeMsg(at.Add(600*time.Millisecond)))
		if m.sig.fading {
			t.Errorf("%s: a frame after the last slide scheduled another", c.name)
		}
	}
}

// Joining segments keeps every attribute: a track cell next to a fill cell
// of the same role stays a separate segment.
func TestAppendSegmentKeepsTrackAndLift(t *testing.T) {
	l := appendSegment(nil, "█", Seg{Role: RoleOK})
	l = appendSegment(l, "▏", Seg{Role: RoleOK, Track: true})
	l = appendSegment(l, " ", Seg{Role: RoleOK, Track: true})
	l = appendSegment(l, "█", Seg{Role: RoleOK, Lift: barLift})
	if len(l) != 3 || l[1].Text != "▏ " || !l[1].Track || l[2].Lift != barLift {
		t.Fatalf("segments %+v", l)
	}
}

// A window never observed (ObservedAt 0, UsedPct 0) is unknown, as the U page
// reads it: it has no target, so its first real reading starts still.
func TestBarUnobservedWindowNeverSlides(t *testing.T) {
	unobserved := map[string]bus.BudgetSnapshot{"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h"}, {Name: "7d", UsedPct: 41, ObservedAt: 1}}}}
	targets := barTargets(unobserved)
	if _, ok := targets["anthropic|5h"]; ok {
		t.Fatalf("an unobserved window has a target: %v", targets)
	}
	slides := updateSlides(nil, targets, shellNow, true)
	observed := map[string]bus.BudgetSnapshot{"anthropic": {Windows: []bus.BudgetWindow{{Name: "5h", UsedPct: 62, ObservedAt: 2}, {Name: "7d", UsedPct: 41, ObservedAt: 2}}}}
	slides = updateSlides(slides, barTargets(observed), shellNow.Add(time.Minute), true)
	if shown, moving := (barView{slides: slides, now: shellNow.Add(time.Minute)}).at("anthropic|5h", 62); moving || shown != 62 {
		t.Errorf("the first real reading after unknown: shown %.1f moving %v", shown, moving)
	}
}
