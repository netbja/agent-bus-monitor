package finedraw

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

// Every glyph the package draws is one terminal cell (busmon measures with
// uniseg too).
func TestGlyphWidths(t *testing.T) {
	glyphs := []string{Full, Empty, Gauge, Thin, ThinMark, Line, Pace}
	glyphs = append(glyphs, Eighths[1:]...)
	glyphs = append(glyphs, SparkSteps...)
	for l := 0; l <= 4; l++ {
		for r := 0; r <= 4; r++ {
			glyphs = append(glyphs, string(BrailleCell(l, r)))
		}
	}
	for _, g := range glyphs {
		if w := uniseg.StringWidth(g); w != 1 {
			t.Errorf("%q (%U): width %d", g, []rune(g), w)
		}
	}
}

func texts(segs []Seg) string {
	var b strings.Builder
	for _, s := range segs {
		b.WriteString(s.Text)
	}
	return b.String()
}

// Bar: every width is cells; fine draws eighths (never empty for a value,
// never full under 100), the leading filled segment Lead; whole cells
// otherwise.
func TestBar(t *testing.T) {
	cases := []struct {
		pct  float64
		fine bool
		want string
	}{
		{0, true, "    "},
		{0.1, true, Eighth1 + "   "},
		{50, true, Full + Full + "  "},
		{99.9, true, Full + Full + Full + Eighth7},
		{100, true, Full + Full + Full + Full},
		{150, true, Full + Full + Full + Full},
		{0, false, Empty + Empty + Empty + Empty},
		{50, false, Gauge + Gauge + Empty + Empty},
		{100, false, Gauge + Gauge + Gauge + Gauge},
	}
	for _, c := range cases {
		segs := Bar(c.pct, 4, c.fine)
		if got := texts(segs); got != c.want || uniseg.StringWidth(got) != 4 {
			t.Errorf("Bar(%v, fine %v) = %q, want %q", c.pct, c.fine, got, c.want)
		}
	}
	if s := Bar(30, 4, true); s[0].Part != PartFill || s[1].Part != PartEdge || !s[1].Lead || s[2].Part != PartTrack {
		t.Errorf("parts %+v", s)
	}
	if s := Bar(50, 4, true); s[0].Lead || !s[1].Lead || s[1].Text != Full {
		t.Errorf("a whole fill leads with its last cell: %+v", s)
	}
	for _, s := range Bar(0, 4, true) {
		if s.Lead {
			t.Errorf("an empty bar has no lead: %+v", s)
		}
	}
}

// ThinCells: one cell per segment, the pace mark on the track or inside the
// fill, the track Thin in fine and Line without.
func TestThinCells(t *testing.T) {
	cases := []struct {
		pct, pace float64
		fine      bool
		want      string
	}{
		{0, -1, true, Thin + Thin + Thin + Thin + Thin},
		{40, 80, true, Thin + Thin + Thin + Thin + Pace},
		{40, 80, false, Thin + Thin + Line + Line + Pace},
		{40, 20, false, Thin + ThinMark + Line + Line + Line},
		{20, 60, false, Thin + Line + Line + Pace + Line},
		{0.1, -1, false, Thin + Line + Line + Line + Line},
		{99, -1, false, Thin + Thin + Thin + Thin + Line},
	}
	for _, c := range cases {
		cells := ThinCells(c.pct, c.pace, 5, c.fine)
		if len(cells) != 5 || texts(cells) != c.want {
			t.Errorf("ThinCells(%v, %v, fine %v) = %q, want %q", c.pct, c.pace, c.fine, texts(cells), c.want)
		}
	}
	c := ThinCells(40, 20, 5, true)
	parts := []Part{c[0].Part, c[1].Part, c[2].Part, c[3].Part, c[4].Part}
	if !reflect.DeepEqual(parts, []Part{PartFill, PartFillMk, PartEmpty, PartEmpty, PartEmpty}) || !c[1].Lead || c[0].Lead {
		t.Errorf("parts %+v", c)
	}
	if p := ThinCells(20, 60, 5, true)[3]; p.Part != PartPace {
		t.Errorf("pace on the track %+v", p)
	}
}

// Blocks rounds to the nearest eighth and Rule clamps its fill.
func TestBlocksAndRule(t *testing.T) {
	if got := texts(Blocks(30, 4)); got != Full+Eighth2+Empty+Empty {
		t.Errorf("Blocks(30) = %q", got)
	}
	if got := texts(Blocks(0, 3)); got != Empty+Empty+Empty {
		t.Errorf("Blocks(0) = %q", got)
	}
	if got := texts(Blocks(100, 3)); got != Full+Full+Full {
		t.Errorf("Blocks(100) = %q", got)
	}
	if r := Rule(3, 10); r[0].Text != strings.Repeat(Thin, 3) || r[1].Text != strings.Repeat(Thin, 7) || r[1].Part != PartEmpty {
		t.Errorf("Rule %+v", r)
	}
	if r := Rule(12, 10); texts(r) != strings.Repeat(Thin, 10) {
		t.Errorf("Rule over %+v", r)
	}
}
