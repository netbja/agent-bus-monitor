package main

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// idleAt is the fixture shell in the idle view at w x h, entered at shellNow+1h.
func idleAt(t testing.TB, w, h int) Model {
	t.Helper()
	m := pollAt(t, idleShell(t, w, h, time.Minute), time.Hour)
	if !m.ui.idle {
		t.Fatalf("%dx%d: not idle", w, h)
	}
	return m
}

// Layout A (the person's choice): three frames, you, team and next, with the
// big count; the strip at the bottom with tokens, the plan and the clock.
func TestIdleViewLayoutA(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{140, 40}, {80, 24}, {60, 20}} {
		m := idleAt(t, size[0], size[1])
		got := m.grid().plain()
		q := stripOf(m.forYou)
		for _, want := range []string{" you ", " team ", " next ", "█", forYouLabel(q), "foureyes", "waiting", " idle",
			"anthropic 62% 5h", "xai held", "16:04"} {
			if !strings.Contains(got, want) {
				t.Errorf("%dx%d: no %q:\n%s", size[0], size[1], want, got)
			}
		}
		if size[0] == 140 && (!strings.Contains(got, "release    working  tagging") || !strings.Contains(got, "1 idle")) {
			t.Errorf("140x40: not every agent not idle:\n%s", got)
		}
		if size[0] == 60 && !strings.Contains(got, "+5 more") {
			t.Errorf("60x20: the cut team does not say so:\n%s", got)
		}
		if strings.Contains(got, "Activity") || strings.Contains(got, "For you") {
			t.Errorf("%dx%d: the dashboard shows through:\n%s", size[0], size[1], got)
		}
	}
}

// Too short for the frames: one line per frame.
func TestIdleViewNarrow(t *testing.T) {
	pinLocal(t)
	m := idleAt(t, 40, 12)
	got := m.grid().plain()
	n := stripOf(m.forYou)
	for _, want := range []string{strconv.Itoa(n.decide+n.merge) + " need you", "waiting", "idle", "next"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q:\n%s", want, got)
		}
	}
}

// Nothing waiting: "nothing needs you", and no digit.
func TestIdleViewNothingNeedsYou(t *testing.T) {
	m := idleAt(t, 140, 40)
	m.snap.asks = nil
	for _, a := range m.snap.agents {
		a.state, a.openAsk, a.forYou, a.obsPrompt, a.gates, a.gated = "idle", 0, false, false, nil, 0
	}
	m.feed.feed = nil
	m.refresh()
	if len(m.forYou) != 0 {
		t.Fatalf("fixture still has %d For you items: %+v", len(m.forYou), m.forYou)
	}
	got := m.grid().plain()
	if !strings.Contains(got, "nothing needs you") || strings.Contains(got, "█") {
		t.Fatalf("empty:\n%s", got)
	}
}

// §6 drift: the block moves over the hour and never leaves the screen.
func TestIdleViewDrift(t *testing.T) {
	seen := map[[2]int]bool{}
	for k := range 120 {
		x, y := idleDrift(k, 3, 2, 20, 7)
		if x < 0 || x > 20 || y < 0 || y > 7 {
			t.Fatalf("k=%d: %d,%d out of 20x7", k, x, y)
		}
		if k > 0 {
			px, py := idleDrift(k-1, 3, 2, 20, 7)
			if absInt(x-px) > 1 || absInt(y-py) > 1 {
				t.Fatalf("k=%d: jumped from %d,%d to %d,%d", k, px, py, x, y)
			}
		}
		seen[[2]int{x, y}] = true
	}
	if len(seen) < 20 {
		t.Fatalf("only %d places in two hours", len(seen))
	}
	if x, y := idleDrift(5, 0, 0, 0, 0); x != 0 || y != 0 {
		t.Fatalf("no room: %d,%d", x, y)
	}
	m := idleAt(t, 140, 40)
	at := func(m Model) (int, int) {
		for y, row := range strings.Split(m.grid().plain(), "\n") {
			if i := strings.Index(row, "╭─ you"); i >= 0 {
				return len([]rune(row[:i])), y
			}
		}
		t.Fatal("no you frame")
		return 0, 0
	}
	x0, y0 := at(m)
	if x1, y1 := at(pollAt(t, m, time.Hour+59*time.Second)); x1 != x0 || y1 != y0 {
		t.Fatalf("moved within the minute: %d,%d to %d,%d", x0, y0, x1, y1)
	}
	m = pollAt(t, m, time.Hour+time.Minute)
	if x1, y1 := at(m); (x1 == x0 && y1 == y0) || absInt(x1-x0) > 1 || absInt(y1-y0) > 1 {
		t.Fatalf("after a minute: %d,%d to %d,%d", x0, y0, x1, y1)
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// §7: without colour the view reads the same: words and digits carry it.
func TestIdleViewNoColorGolden(t *testing.T) {
	pinLocal(t)
	withPalette(t, "dark", true, 1<<24)
	m := idleAt(t, 140, 40)
	m.paint = newPainter(th)
	checkGolden(t, "idle-140x40-nocolor.ansi", m.View().Content+"\n")
}
