package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/internal/finedraw"
)

// braille-sparklines §4: a braille cell is U+2800 plus the dots of its two
// days, the left column day 2i, the right 2i+1, filled from the bottom.
func TestBrailleCell(t *testing.T) {
	for _, c := range []struct {
		left, right int
		want        rune
	}{
		{0, 0, '⠀'},
		{1, 0, '⡀'}, {2, 0, '⡄'}, {3, 0, '⡆'}, {4, 0, '⡇'},
		{0, 1, '⢀'}, {0, 2, '⢠'}, {0, 3, '⢰'}, {0, 4, '⢸'},
		{4, 4, '⣿'}, {3, 1, '⣆'}, {4, 2, '⣧'},
	} {
		if got := finedraw.BrailleCell(c.left, c.right); got != c.want {
			t.Errorf("finedraw.BrailleCell(%d, %d) = %U, want %U", c.left, c.right, got, c.want)
		}
	}
}

// Every pair of levels reads back from its cell: no two pairs share a glyph.
func TestBrailleCellRoundTrip(t *testing.T) {
	for l := 0; l <= 4; l++ {
		for r := 0; r <= 4; r++ {
			if gl, gr := finedraw.BrailleLevels(finedraw.BrailleCell(l, r)); gl != l || gr != r {
				t.Errorf("(%d, %d) reads back as (%d, %d)", l, r, gl, gr)
			}
		}
	}
}

// The scale is unchanged (§4), and any non-zero value lights at least the
// lowest step, the bottom dot in braille: a small day never looks like none
// (the person, with Q1 and Q2).
func TestSparkLevel(t *testing.T) {
	for _, c := range []struct {
		v, max  float64
		n, want int
	}{
		{0, 100, 8, 0}, {0, 0, 4, 0}, {-1, 100, 4, 0},
		{1e-9, 100, 4, 1}, {1e-9, 100, 8, 1}, {1, 5_100_000, 4, 1},
		{50, 100, 8, 5}, {100, 100, 8, 8}, {100, 100, 4, 4}, {99.9, 100, 4, 4},
	} {
		if got := finedraw.Level(c.v, c.max, c.n); got != c.want {
			t.Errorf("finedraw.Level(%v, %v, %d) = %d, want %d", c.v, c.max, c.n, got, c.want)
		}
	}
}

// §3's sample week, on the zone's largest day (5.1M), in each style, the
// column kept at 7 cells.
func TestSparkLineStyles(t *testing.T) {
	m := func(xs ...float64) []float64 {
		for i := range xs {
			xs[i] *= 1e6
		}
		return xs
	}
	abm := m(0, 1.2, 3.4, 0.4, 5.1, 2.2, 4.0)
	mine := m(0.3, 0, 0, 0.9, 0.6, 0.1, 1.4)
	for _, c := range []struct {
		style     string
		abm, mine string
	}{
		{graphsBlock, " ▂▆▁█▄▇", "▁  ▂▁▁▃"},
		{graphsASCII, " .+_#:*", "_  .__-"},
		{graphsBraille, "⢀⣆⣧⡇   ", "⡀⢀⣀⡄   "},
	} {
		if got := finedraw.Spark(abm, 5.1e6, c.style); got != c.abm {
			t.Errorf("%s abm = %q, want %q", c.style, got, c.abm)
		}
		if got := finedraw.Spark(mine, 5.1e6, c.style); got != c.mine {
			t.Errorf("%s mine = %q, want %q", c.style, got, c.mine)
		}
		if w := Width(finedraw.Spark(abm, 5.1e6, c.style)); w != 7 {
			t.Errorf("%s: width %d, want 7", c.style, w)
		}
	}
	// An odd count leaves the last right column empty; a tiny day lights the bottom dot.
	if got := finedraw.Spark([]float64{0, 0, 1}, 1e9, graphsBraille); got != "⠀⡀ " {
		t.Errorf("odd count = %q", got)
	}
	// An empty week is blanks; braille cells that hold nothing are U+2800.
	if got := finedraw.Spark(make([]float64, 7), 0, graphsBraille); got != "⠀⠀⠀⠀   " {
		t.Errorf("empty braille week = %q", got)
	}
}

// Each braille glyph is one cell wide, so no alignment code changes (§4).
func TestBrailleWidth(t *testing.T) {
	for l := 0; l <= 4; l++ {
		for r := 0; r <= 4; r++ {
			if w := Width(string(finedraw.BrailleCell(l, r))); w != 1 {
				t.Errorf("%U: width %d", finedraw.BrailleCell(l, r), w)
			}
		}
	}
}

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// The locale's character set (tui-rendering §3): UTF-8 or not, every pair
// of the locale's precedence pinned; TERM=linux does not matter. Without
// UTF-8 every setting draws ascii (finedraw.Resolve, TestResolve).
func TestLocaleUTF8(t *testing.T) {
	for _, c := range []struct {
		env  []string
		want bool
	}{
		{nil, false},
		{[]string{"LANG", "C"}, false},
		{[]string{"LANG", "POSIX"}, false},
		{[]string{"LANG", "fr_FR.UTF-8"}, true},
		{[]string{"LANG", "en_US.utf8"}, true},
		{[]string{"LC_ALL", "C", "LC_CTYPE", "en_US.UTF-8"}, false},
		{[]string{"LC_ALL", "C", "LANG", "en_US.UTF-8"}, false},
		{[]string{"LC_CTYPE", "C", "LANG", "fr_FR.UTF-8"}, false},
		{[]string{"LC_CTYPE", "en_US.UTF-8", "LANG", "C"}, true},
		{[]string{"TERM", "linux", "LANG", "en_US.UTF-8"}, true},
	} {
		if got := localeUTF8(envOf(c.env...)); got != c.want {
			t.Errorf("localeUTF8(%v) = %v, want %v", c.env, got, c.want)
		}
	}
}

// §5, tui-rendering §3: the Tokens page in each mode (fine's braille,
// basic's blocks, ascii), at 80 and 140 columns and on an 80x25 terminal.
func TestTokensGraphsGoldens(t *testing.T) {
	pinLocal(t)
	for _, style := range []string{graphsBraille, graphsBlock, graphsASCII} {
		for _, sz := range [][2]int{{80, 44}, {140, 44}, {80, 25}} {
			t.Run(fmt.Sprintf("%s-%dx%d", style, sz[0], sz[1]), func(t *testing.T) {
				withPalette(t, "dark", false, 1<<24)
				m := fixtureShell(t, sz[0], sz[1])
				withRendering(t, map[string]finedraw.Mode{graphsBraille: finedraw.ModeFine, graphsBlock: finedraw.ModeBasic, graphsASCII: finedraw.ModeASCII}[style])
				m.snap.tok = tokFixtureState(shellNow)
				m = keys(t, m, 'U')
				if !m.rd.open() || m.rd.top().kind != ovTokens {
					t.Fatal("U opened no tokens page")
				}
				m.paint = newPainter(th)
				checkGolden(t, fmt.Sprintf("tokens-%dx%d-graphs-%s.txt", sz[0], sz[1], style), m.grid().plain()+"\n")
			})
		}
	}
}

// Sparklines only (§2): in ascii or braille, the page differs from block's
// only in each Projects row's 7-cell sparkline; every gauge and bar keeps its
// blocks.
func TestGraphsLeaveTheGauges(t *testing.T) {
	w := []bus.BudgetWindow{{Name: "5h", UsedPct: 40, ObservedAt: 1}}
	budgets := map[string]bus.BudgetSnapshot{"anthropic": {Windows: w}}
	page := func(style string) []string {
		return strings.Split(linesText(tokensPage(iconsOff, style, tokFixtureState(shellNow), budgets, nil, nil, "agent-bus-monitor", shellNow, 120, false, barView{}, true)), "\n")
	}
	block := page(graphsBlock)
	if !strings.Contains(strings.Join(block, "\n"), GlyphBarFull+GlyphBarFull) {
		t.Fatal("the fixture draws no gauge")
	}
	for _, style := range []string{graphsASCII, graphsBraille} {
		other, differ := page(style), 0
		if len(other) != len(block) {
			t.Fatalf("%s: %d lines, block %d", style, len(other), len(block))
		}
		for i := range block {
			a, b := []rune(block[i]), []rune(other[i])
			if string(a) == string(b) {
				continue
			}
			differ++
			pre, suf := 0, 0
			for pre < min(len(a), len(b)) && a[pre] == b[pre] {
				pre++
			}
			for suf < min(len(a), len(b))-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
				suf++
			}
			if len(a) != len(b) || len(a)-pre-suf > tokSparkCells {
				t.Errorf("%s: line %d differs beyond a sparkline:\n%s\n%s", style, i, block[i], other[i])
			}
		}
		if differ == 0 {
			t.Errorf("%s: no sparkline changed", style)
		}
	}
}
