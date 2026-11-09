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
	withPalette(t, "dark", false, 1<<24)
	const w = 20
	wantRoles := [idleBreathCycle]Role{RoleDim, RoleMuted, RoleAccent, RoleMuted}
	covered := map[int]bool{}
	for step := range 2 * w * idleBreathCycle {
		l := idleBreath(w, step)
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
		l := idleBreath(w, step)
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
		for _, a := range m.snap.agents {
			a.bg = &bus.Background{Text: "2 checks running", Since: m.now.Add(-time.Minute)}
			break
		}
		if d := m.idleData(); !d.allIdle || len(d.team) == 0 {
			t.Fatalf("no-colour=%v: an idle agent with background work: allIdle=%v, %d team rows", noColor, d.allIdle, len(d.team))
		}
		if r := row(m); strings.Contains(r, GlyphThinBar) {
			t.Fatalf("no-colour=%v: the thread for background work: %q", noColor, r)
		}
	}
}
