package main

import (
	"strings"
	"testing"
	"time"
)

// The thread (idle-animation, the person's animation A): a 3-cell thread on
// a dotted line w cells wide, one cell right per step, across the whole width.
func TestIdleThread(t *testing.T) {
	const w = 20
	covered := map[int]bool{}
	prev := -1
	for step := range 2 * (w + idleThreadLen) {
		l := idleThread(w, step)
		if l.Width() != w {
			t.Fatalf("step %d: %d cells, want %d", step, l.Width(), w)
		}
		cells := []rune(l.text())
		first, n := -1, 0
		for i, r := range cells {
			switch string(r) {
			case GlyphThinBar:
				if first < 0 {
					first = i
				}
				n++
				covered[i] = true
			case GlyphNone:
			default:
				t.Fatalf("step %d: cell %d is %q", step, i, r)
			}
		}
		if n > idleThreadLen || n == 0 && step%(w+idleThreadLen) != 0 {
			t.Fatalf("step %d: %d thread cells: %q", step, n, l.text())
		}
		if n == idleThreadLen && !strings.Contains(l.text(), strings.Repeat(GlyphThinBar, n)) {
			t.Fatalf("step %d: a broken thread %q", step, l.text())
		}
		if prev >= 0 && first > 0 && first != prev+1 {
			t.Fatalf("step %d: the thread jumped from %d to %d", step, prev, first)
		}
		prev = first
		for _, seg := range l {
			want := RoleDim
			if strings.Contains(seg.Text, GlyphThinBar) {
				want = RoleAccent
			}
			if seg.Role != want || strings.Contains(seg.Text, GlyphThinBar) && strings.Contains(seg.Text, GlyphNone) {
				t.Fatalf("step %d: segment %q in role %v", step, seg.Text, seg.Role)
			}
		}
	}
	if len(covered) != w {
		t.Fatalf("over a pass the thread covered %d of %d cells", len(covered), w)
	}
}

// On screen: the row above the strip, still within a second, a cell further
// at the next one; at every size, and without colour too (no colour is no
// reason to stop or blink it).
func TestIdleViewThreadMoves(t *testing.T) {
	pinLocal(t)
	threadAt := func(m Model) int {
		rows := strings.Split(m.grid().plain(), "\n")
		row := []rune(rows[m.height-2])
		if strings.Count(string(row), GlyphNone)+strings.Count(string(row), GlyphThinBar) != m.width {
			t.Fatalf("%dx%d: the row above the strip is not the thread: %q", m.width, m.height, string(row))
		}
		for i, r := range row {
			if string(r) == GlyphThinBar {
				return i
			}
		}
		return -1
	}
	for _, c := range []struct {
		w, h    int
		noColor bool
	}{{140, 40, false}, {40, 12, false}, {140, 40, true}} {
		withPalette(t, "dark", c.noColor, 1<<24)
		m := idleAt(t, c.w, c.h)
		m = pollAt(t, m, time.Hour+5*time.Second)
		x0 := threadAt(m)
		if x0 < 0 {
			t.Fatalf("%dx%d: no thread", c.w, c.h)
		}
		if x := threadAt(pollAt(t, m, time.Hour+5*time.Second+900*time.Millisecond)); x != x0 {
			t.Fatalf("%dx%d no-colour=%v: moved within the second: %d to %d", c.w, c.h, c.noColor, x0, x)
		}
		if x := threadAt(pollAt(t, m, time.Hour+6*time.Second)); x != x0+1 {
			t.Fatalf("%dx%d no-colour=%v: after a second at %d, want %d", c.w, c.h, c.noColor, x, x0+1)
		}
	}
}
