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
		mode Mode
		want string
	}{
		{0, ModeFine, "    "},
		{0.1, ModeFine, Eighth1 + "   "},
		{50, ModeFine, Full + Full + "  "},
		{99.9, ModeFine, Full + Full + Full + Eighth7},
		{100, ModeFine, Full + Full + Full + Full},
		{150, ModeFine, Full + Full + Full + Full},
		{0, ModeBasic, Empty + Empty + Empty + Empty},
		{50, ModeBasic, Gauge + Gauge + Empty + Empty},
		{100, ModeBasic, Gauge + Gauge + Gauge + Gauge},
		{0, ModeASCII, "----"},
		{0.1, ModeASCII, "#---"},
		{50, ModeASCII, "##--"},
		{99.9, ModeASCII, "###-"},
		{100, ModeASCII, "####"},
	}
	for _, c := range cases {
		segs := Bar(c.pct, 4, c.mode)
		if got := texts(segs); got != c.want || uniseg.StringWidth(got) != 4 {
			t.Errorf("Bar(%v, %v) = %q, want %q", c.pct, c.mode, got, c.want)
		}
	}
	if s := Bar(30, 4, ModeFine); s[0].Part != PartFill || s[1].Part != PartEdge || !s[1].Lead || s[2].Part != PartTrack {
		t.Errorf("parts %+v", s)
	}
	if s := Bar(50, 4, ModeFine); s[0].Lead || !s[1].Lead || s[1].Text != Full {
		t.Errorf("a whole fill leads with its last cell: %+v", s)
	}
	for _, s := range Bar(0, 4, ModeFine) {
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
		mode      Mode
		want      string
	}{
		{0, -1, ModeFine, Thin + Thin + Thin + Thin + Thin},
		{40, 80, ModeFine, Thin + Thin + Thin + Thin + Pace},
		{40, 80, ModeBasic, Thin + Thin + Line + Line + Pace},
		{40, 20, ModeBasic, Thin + ThinMark + Line + Line + Line},
		{20, 60, ModeBasic, Thin + Line + Line + Pace + Line},
		{0.1, -1, ModeBasic, Thin + Line + Line + Line + Line},
		{99, -1, ModeBasic, Thin + Thin + Thin + Thin + Line},
		{40, 80, ModeASCII, "==--|"},
		{40, 20, ModeASCII, "=+---"},
	}
	for _, c := range cases {
		cells := ThinCells(c.pct, c.pace, 5, c.mode)
		if len(cells) != 5 || texts(cells) != c.want {
			t.Errorf("ThinCells(%v, %v, %v) = %q, want %q", c.pct, c.pace, c.mode, texts(cells), c.want)
		}
	}
	c := ThinCells(40, 20, 5, ModeFine)
	parts := []Part{c[0].Part, c[1].Part, c[2].Part, c[3].Part, c[4].Part}
	if !reflect.DeepEqual(parts, []Part{PartFill, PartFillMk, PartEmpty, PartEmpty, PartEmpty}) || !c[1].Lead || c[0].Lead {
		t.Errorf("parts %+v", c)
	}
	if p := ThinCells(20, 60, 5, ModeFine)[3]; p.Part != PartPace {
		t.Errorf("pace on the track %+v", p)
	}
}

// Blocks rounds to the nearest eighth and Rule clamps its fill.
func TestBlocksAndRule(t *testing.T) {
	if got := texts(Blocks(30, 4, ModeFine)); got != Full+Eighth2+Empty+Empty {
		t.Errorf("Blocks(30) = %q", got)
	}
	if got := texts(Blocks(0, 3, ModeFine)); got != Empty+Empty+Empty {
		t.Errorf("Blocks(0) = %q", got)
	}
	if got := texts(Blocks(100, 3, ModeFine)); got != Full+Full+Full {
		t.Errorf("Blocks(100) = %q", got)
	}
	if r := Rule(3, 10, ModeFine); r[0].Text != strings.Repeat(Thin, 3) || r[1].Text != strings.Repeat(Thin, 7) || r[1].Part != PartEmpty {
		t.Errorf("Rule %+v", r)
	}
	if r := Rule(12, 10, ModeFine); texts(r) != strings.Repeat(Thin, 10) {
		t.Errorf("Rule over %+v", r)
	}
	// ASCII (a locale without UTF-8): the same geometry in # and -, = and -.
	if got := texts(Blocks(30, 4, ModeASCII)); got != "#---" {
		t.Errorf("Blocks ascii = %q", got)
	}
	if got := texts(Blocks(30, 4, ModeBasic)); got != Full+Eighth2+Empty+Empty {
		t.Errorf("Blocks basic = %q", got)
	}
	if r := Rule(3, 10, ModeASCII); texts(r) != "===-------" || r[1].Part != PartEmpty {
		t.Errorf("Rule ascii %+v", r)
	}
}
