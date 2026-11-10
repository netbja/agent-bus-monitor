package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// breathCell is the breathing cell of l: its index, glyph and role; -1 when
// every cell is the dim dotted row.
func breathCell(t *testing.T, l Line) (int, string, Role) {
	t.Helper()
	x, at, glyph, role := 0, -1, "", RoleDim
	for _, s := range l {
		for _, r := range s.Text {
			if g := string(r); g != GlyphNone || s.Role != RoleDim {
				if at >= 0 {
					t.Fatalf("two lit cells in %q", l.text())
				}
				at, glyph, role = x, g, s.Role
			}
			x++
		}
	}
	return at, glyph, role
}

// Respiration (the person's choice for a team all idle): one cell on a fixed
// dotted row brightens and dims over a 4 s cycle (dim, muted, bright, muted),
// a cell further right each cycle so that over w cycles every cell carries
// it (burn-in). Never more than one lit cell (OLED).
func TestIdleBreath(t *testing.T) {
	withPalette(t, "dark", false, 16) // 16 colours: no shade to ramp, the role steps stay
	const w = 20
	wantRoles := [idleBreathCycle]Role{RoleDim, RoleMuted, RoleAccent, RoleMuted}
	covered := map[int]bool{}
	for step := range 2 * w * idleBreathCycle {
		l := idleBreath(w, step, false)
		if l.Width() != w {
			t.Fatalf("step %d: %d cells, want %d", step, l.Width(), w)
		}
		at, glyph, role := breathCell(t, l) // in colour the dot stays, only its shade breathes
		phase := step % idleBreathCycle
		if at != (step/idleBreathCycle)%w {
			t.Fatalf("step %d: the cell at %d, want %d (%q)", step, at, (step/idleBreathCycle)%w, l.text())
		}
		if glyph != GlyphSet || role != wantRoles[phase] {
			t.Fatalf("step %d (phase %d): %q in %v, want %q in %v", step, phase, glyph, role, GlyphSet, wantRoles[phase])
		}
		covered[at] = true
	}
	if len(covered) != w {
		t.Fatalf("over %d cycles the cell visited %d of %d cells", 2*w, len(covered), w)
	}
}

// Without colour the cycle is two glyphs: the dim dot of the row, then the
// bright one, then the dim one again, over the same 4 s.
func TestIdleBreathNoColor(t *testing.T) {
	withPalette(t, "dark", true, 1<<24)
	const w = 12
	seen := map[string]bool{}
	for step := range idleBreathCycle * 3 {
		l := idleBreath(w, step, false)
		cells := []rune(l.text())
		x := (step / idleBreathCycle) % w
		bright := step%idleBreathCycle == 1 || step%idleBreathCycle == 2
		want := GlyphNone
		if bright {
			want = GlyphSet
		}
		if got := string(cells[x]); got != want {
			t.Fatalf("step %d: cell %d is %q, want %q (%q)", step, x, got, want, l.text())
		}
		if n := strings.Count(l.text(), GlyphSet); n > 1 || !bright && n != 0 {
			t.Fatalf("step %d: %d bright cells in %q", step, n, l.text())
		}
		seen[string(cells[x])] = true
	}
	if !seen[GlyphNone] || !seen[GlyphSet] {
		t.Fatalf("no-colour cycle saw %v, want both dots", seen)
	}
}

// On screen: with every agent idle the row above the strip breathes (no
// thread); with one at work the thread slides as before (idle-animation).
func TestIdleViewBreathesWhenAllIdle(t *testing.T) {
	pinLocal(t)
	for _, noColor := range []bool{false, true} {
		withPalette(t, "dark", noColor, 1<<24)
		m := pollAt(t, idleAt(t, 140, 40), time.Hour+5*time.Second) // mid-pass, as TestIdleViewThreadMoves reads it
		row := func(m Model) string { return strings.Split(m.grid().plain(), "\n")[m.height-2] }
		if !strings.Contains(row(m), GlyphThinBar) {
			t.Fatalf("no-colour=%v: a team at work shows no thread: %q", noColor, row(m))
		}
		for _, a := range m.snap.agents {
			a.state, a.message, a.openAsk, a.held, a.paused, a.bg, a.obsPrompt = "idle", "", 0, false, false, nil, false
		}
		if d := m.idleData(); !d.allIdle {
			t.Fatalf("no-colour=%v: every agent idle, but not allIdle: %v", noColor, d.counts)
		}
		lit := 0
		for s := range idleBreathCycle {
			m.now = m.ui.idleSince.Add(time.Hour + time.Duration(s)*time.Second)
			r := row(m)
			if strings.Contains(r, GlyphThinBar) {
				t.Fatalf("no-colour=%v: the thread while all idle: %q", noColor, r)
			}
			if strings.Count(r, GlyphNone)+strings.Count(r, GlyphSet) != m.width {
				t.Fatalf("no-colour=%v: the row is not the dotted row: %q", noColor, r)
			}
			lit += strings.Count(r, GlyphSet)
		}
		if lit == 0 {
			t.Fatalf("no-colour=%v: the cell never brightened over a cycle", noColor)
		}
		// background work adds a team row but is still idle (progress-line
		// §4.3): the team still breathes (foureyes)
		shown, _ := visibleAgents(m.snap.agents, m.snap.pilot, false, m.now) // an agent the view shows, not a dormant one (foureyes)
		if len(shown) == 0 {
			t.Fatalf("no-colour=%v: no visible agent in the fixture", noColor)
		}
		m.snap.agents[shown[0]].bg = &bus.Background{Text: "2 checks running", Since: m.now.Add(-time.Minute)}
		if d := m.idleData(); !d.allIdle || len(d.team) == 0 {
			t.Fatalf("no-colour=%v: an idle agent with background work: allIdle=%v, %d team rows", noColor, d.allIdle, len(d.team))
		}
		if r := row(m); strings.Contains(r, GlyphThinBar) {
			t.Fatalf("no-colour=%v: the thread for background work: %q", noColor, r)
		}
	}
}

// idle-dot-smooth (the person: the breath felt jerky next to the agents'
// dots): with true colour and fade on, the breath takes the agents' own ramp,
// fadeLevel's raised cosine of the accent (Seg.Fade), sampled once a second
// (the poll, no added frame) over idleSmoothCycle steps, so a step changes
// the shade by a little, never from dim to bright at once. The cell moves
// once per cycle; still one lit cell.
func TestIdleBreathSmoothRamp(t *testing.T) {
	withPalette(t, "dark", false, 1<<24)
	const w = 10
	want := []int{20, 40, 80, 100, 80, 40}
	if len(want) != idleSmoothCycle {
		t.Fatalf("cycle %d, want %d", idleSmoothCycle, len(want))
	}
	prev := -1
	for step := range 3 * idleSmoothCycle {
		l := idleBreath(w, step, true)
		if l.Width() != w {
			t.Fatalf("step %d: %d cells", step, l.Width())
		}
		var lit *Seg
		x, at := 0, -1
		for i := range l {
			if l[i].Role != RoleDim {
				if lit != nil {
					t.Fatalf("two lit cells in %q", l.text())
				}
				lit, at = &l[i], x
			}
			x += Width(l[i].Text)
		}
		if lit == nil || lit.Text != GlyphSet || lit.Role != RoleAccent {
			t.Fatalf("step %d: lit %+v", step, lit)
		}
		phase := step % idleSmoothCycle
		if lit.Fade != want[phase] || at != (step/idleSmoothCycle)%w {
			t.Fatalf("step %d: fade %d at %d, want %d at %d", step, lit.Fade, at, want[phase], (step/idleSmoothCycle)%w)
		}
		if prev >= 0 && phase != 0 && absInt(lit.Fade-prev) > 40 {
			t.Fatalf("step %d: a jump of %d", step, lit.Fade-prev)
		}
		prev = lit.Fade
	}
	// The same curve as the agents' dots: fadeLevel at the same phase.
	for k := range idleSmoothCycle {
		at := time.UnixMilli(int64(k) * fadePeriod.Milliseconds() / idleSmoothCycle)
		if got := rampLevel(float64(k) / idleSmoothCycle); got != fadeLevel(at) {
			t.Errorf("phase %d/%d: ramp %d, the agents' fade %d", k, idleSmoothCycle, got, fadeLevel(at))
		}
	}
}

// On screen the breath ramps only where a shade can show: true colour with
// the fade setting on (the agents' rule); otherwise the role steps.
func TestIdleViewBreathSmoothFollowsTheFadeSetting(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", false, 1<<24)
	m := idleAt(t, 140, 40)
	m.cfg.settings.Fade = true
	if !m.idleSmooth() {
		t.Error("true colour and fade on: not smooth")
	}
	m.cfg.settings.Fade = false
	if m.idleSmooth() {
		t.Error("fade off: smooth")
	}
	withPalette(t, "dark", false, 16)
	m.cfg.settings.Fade = true
	if m.idleSmooth() {
		t.Error("16 colours: smooth")
	}
}

// On screen, the whole team resting, true colour and fade on: the breath's
// lit cell carries the ramp's shade (Seg.Fade), over a whole cycle.
func TestIdleViewBreathRampsOnScreen(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", false, 1<<24)
	m := pollAt(t, idleAt(t, 140, 40), time.Hour)
	m.cfg.settings.Fade = true
	for _, a := range m.snap.agents {
		a.state, a.message, a.openAsk, a.held, a.paused, a.bg, a.obsPrompt = "idle", "", 0, false, false, nil, false
	}
	d := m.idleData()
	if !d.allIdle {
		t.Fatal("not all idle")
	}
	var levels []int
	for s := range idleSmoothCycle {
		m.now = m.ui.idleSince.Add(time.Hour + time.Duration(s)*time.Second)
		for _, seg := range m.idleRow(d, 40) {
			if seg.Role == RoleAccent {
				levels = append(levels, seg.Fade)
			}
		}
	}
	if len(levels) != idleSmoothCycle || levels[0] == levels[3] {
		t.Errorf("levels over a cycle %v, want the ramp", levels)
	}
}
