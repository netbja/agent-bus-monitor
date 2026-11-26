package finedraw

import (
	"strings"
	"testing"
)

// Stops place a ramp's colours at given percents (tui-rendering §4.5: the
// context ramp's amber at the alert threshold, red at the urgent one): linear
// between two stops, flat before the first and past the last.
func TestStops(t *testing.T) {
	green, amber, red := RGB{0, 200, 0}, RGB{200, 200, 0}, RGB{200, 0, 0}
	s := Stops{{0, green}, {80, amber}, {90, red}}
	for _, c := range []struct {
		pct  float64
		want RGB
	}{{-3, green}, {0, green}, {40, RGB{100, 200, 0}}, {80, amber}, {85, RGB{200, 100, 0}}, {90, red}, {120, red}} {
		if got := s.At(c.pct); got != c.want {
			t.Errorf("At(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
	if got := (Stops{{50, amber}}).At(10); got != amber {
		t.Errorf("one stop: %v", got)
	}
	if got := (Stops{}).At(10); got != (RGB{}) {
		t.Errorf("no stop: %v", got)
	}
	// Two stops at one place (urgent not above alert): the later one past it.
	if got := (Stops{{0, green}, {80, amber}, {80, red}}).At(80); got != red {
		t.Errorf("stops at one place: %v", got)
	}
	// Shade takes either gradient.
	if sh := (Caps{ModeFine, DepthTrue}).Shade(s, 85, []float64{80, 90}); !sh.UseRGB || sh.RGB != s.At(85) || sh.Band != 1 {
		t.Errorf("shade on stops: %+v", sh)
	}
}

// SparkLines: one row is SparkWide's line; braille on two rows gives 8
// levels per column (the bottom row's four dots first), so the top row
// shows only what passes half; block and ascii stay one row. Share is each
// cell's highest value in percent of top (the caller's colour).
func TestSparkLines(t *testing.T) {
	week := []float64{0, 1, 2, 3, 4, 6, 8}
	for _, style := range []string{StyleBlock, StyleBraille, StyleASCII} {
		lines, _ := SparkLines(week, 8, 14, 1, style)
		if len(lines) != 1 || lines[0] != SparkWide(week, 8, 14, style) {
			t.Errorf("%s one row: %q", style, lines)
		}
		if lines, _ := SparkLines(week, 8, 14, 2, style); style != StyleBraille && len(lines) != 1 {
			t.Errorf("%s keeps one row: %q", style, lines)
		}
	}
	lines, share := SparkLines([]float64{2, 8}, 8, 2, 2, StyleBraille) // a cell per value
	if len(lines) != 2 || len(share) != 2 {
		t.Fatalf("two rows: %q %v", lines, share)
	}
	top, bottom := []rune(lines[0]), []rune(lines[1])
	if l, r := BrailleLevels(bottom[0]); l != 3 || r != 3 { // 2 of 8: level 3 (Level: any value shows), both samples
		t.Errorf("bottom of 2/8: %d %d", l, r)
	}
	if top[0] != BrailleBlank {
		t.Errorf("top of 2/8: %q", top[0])
	}
	if l, r := BrailleLevels(bottom[1]); l != 4 || r != 4 {
		t.Errorf("bottom of 8/8: %d %d", l, r)
	}
	if l, r := BrailleLevels(top[1]); l != 4 || r != 4 {
		t.Errorf("top of 8/8: %d %d", l, r)
	}
	if share[0] != 25 || share[1] != 100 {
		t.Errorf("shares %v", share)
	}
	// 5 of 8 is level 6: two dots on top.
	if lines, _ := SparkLines([]float64{5}, 8, 1, 2, StyleBraille); func() bool { l, _ := BrailleLevels([]rune(lines[0])[0]); return l != 2 }() {
		t.Errorf("5/8 top: %q", lines)
	}
	// Where the values do not reach: spaces on every row, share 0.
	lines, share = SparkLines([]float64{8, 8, 8}, 8, 4, 2, StyleBraille) // 6 points: 3 cells
	for _, l := range lines {
		if r := []rune(l); len(r) != 4 || string(r[3:]) != " " {
			t.Errorf("padded %q", l)
		}
	}
	if share[3] != 0 {
		t.Errorf("share of nothing %v", share)
	}
	if lines, _ := SparkLines(nil, 0, 3, 2, StyleBraille); lines[0] != strings.Repeat(" ", 3) || lines[1] != strings.Repeat(" ", 3) {
		t.Errorf("no values %q", lines)
	}
}
