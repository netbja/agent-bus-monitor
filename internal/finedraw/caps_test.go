package finedraw

import "testing"

// Resolve (spec §3, the person's Q1 and Q4): a locale without UTF-8 forces
// ascii; else an explicitly set display.rendering wins, then an explicitly
// set display.graphs mapped (block basic, braille fine, ascii ascii), else
// auto, which is fine. Each answer names what decided it.
func TestResolve(t *testing.T) {
	set := func(v string) Setting { return Setting{Value: v, Set: true} }
	dflt := Setting{Value: "auto"}
	cases := []struct {
		name          string
		rendering, gr Setting
		utf8          bool
		want          Mode
		by            string
	}{
		{"neither set", dflt, dflt, true, ModeFine, ByAuto},
		{"graphs braille alone", dflt, set("braille"), true, ModeFine, ByGraphs},
		{"graphs block alone", dflt, set("block"), true, ModeBasic, ByGraphs},
		{"graphs ascii alone", dflt, set("ascii"), true, ModeASCII, ByGraphs},
		{"graphs auto set", dflt, set("auto"), true, ModeFine, ByAuto},
		{"rendering basic over graphs braille", set("basic"), set("braille"), true, ModeBasic, ByRendering},
		{"rendering fine over graphs block", set("fine"), set("block"), true, ModeFine, ByRendering},
		{"rendering auto set over graphs block", set("auto"), set("block"), true, ModeFine, ByRendering},
		{"no UTF-8, neither", dflt, dflt, false, ModeASCII, ByLocale},
		{"no UTF-8 over rendering fine", set("fine"), dflt, false, ModeASCII, ByLocale},
		{"no UTF-8 over graphs braille", dflt, set("braille"), false, ModeASCII, ByLocale},
		{"an unknown rendering is not set", set("dense"), set("block"), true, ModeBasic, ByGraphs},
	}
	for _, c := range cases {
		if got, by := Resolve(c.rendering, c.gr, c.utf8); got != c.want || by != c.by {
			t.Errorf("%s: %v by %q, want %v by %q", c.name, got, by, c.want, c.by)
		}
	}
}

func TestDepthOf(t *testing.T) {
	for _, c := range []struct {
		colors  int
		noColor bool
		want    Depth
	}{{1 << 24, false, DepthTrue}, {256, false, Depth256}, {16, false, Depth16}, {8, false, Depth16}, {1 << 24, true, DepthNone}, {0, false, DepthNone}} {
		if got := DepthOf(c.colors, c.noColor); got != c.want {
			t.Errorf("DepthOf(%d, %v) = %v, want %v", c.colors, c.noColor, got, c.want)
		}
	}
}

// The fallback table (§3): bars are fine with eighths only where a track
// colour shows (256 colours and up), basic otherwise; ascii stays ascii;
// sparklines are braille in fine, blocks in basic, ascii in ascii, whatever
// the colours.
func TestCapsFallbacks(t *testing.T) {
	for _, c := range []struct {
		caps  Caps
		bar   Mode
		spark string
	}{
		{Caps{ModeFine, DepthTrue}, ModeFine, StyleBraille},
		{Caps{ModeFine, Depth256}, ModeFine, StyleBraille},
		{Caps{ModeFine, Depth16}, ModeBasic, StyleBraille},
		{Caps{ModeFine, DepthNone}, ModeBasic, StyleBraille},
		{Caps{ModeBasic, DepthTrue}, ModeBasic, StyleBlock},
		{Caps{ModeBasic, DepthNone}, ModeBasic, StyleBlock},
		{Caps{ModeASCII, DepthTrue}, ModeASCII, StyleASCII},
		{Caps{ModeASCII, DepthNone}, ModeASCII, StyleASCII},
	} {
		if got := c.caps.BarMode(); got != c.bar {
			t.Errorf("%+v: bars %v, want %v", c.caps, got, c.bar)
		}
		if got := c.caps.SparkStyle(); got != c.spark {
			t.Errorf("%+v: sparklines %q, want %q", c.caps, got, c.spark)
		}
	}
}

// A ramp is linear between its stops; at 256 colours and up it is drawn as
// RGB (the terminal library downsamples it); in 16 colours it is a band
// (the caller's role) at the thresholds; without colour, none.
func TestRamp(t *testing.T) {
	r := Ramp{{0, 200, 0}, {200, 200, 0}, {200, 0, 0}}
	for _, c := range []struct {
		pct  float64
		want RGB
	}{{0, RGB{0, 200, 0}}, {25, RGB{100, 200, 0}}, {50, RGB{200, 200, 0}}, {100, RGB{200, 0, 0}}, {-5, RGB{0, 200, 0}}, {140, RGB{200, 0, 0}}} {
		if got := r.At(c.pct); got != c.want {
			t.Errorf("At(%v) = %v, want %v", c.pct, got, c.want)
		}
	}
	if got := (Ramp{{1, 2, 3}}).At(70); got != (RGB{1, 2, 3}) {
		t.Errorf("one stop: %v", got)
	}
	bands := []float64{80, 90} // amber from 80, red from 90
	for _, c := range []struct {
		caps   Caps
		pct    float64
		rgb    bool
		band   int
		colour bool
	}{
		{Caps{ModeFine, DepthTrue}, 85, true, 1, true},
		{Caps{ModeFine, Depth256}, 85, true, 1, true},
		{Caps{ModeBasic, DepthTrue}, 85, false, 1, true}, // basic: role colours
		{Caps{ModeFine, Depth16}, 79, false, 0, true},
		{Caps{ModeFine, Depth16}, 80, false, 1, true},
		{Caps{ModeFine, Depth16}, 95, false, 2, true},
		{Caps{ModeFine, DepthNone}, 95, false, 2, false},
	} {
		s := c.caps.Shade(r, c.pct, bands)
		if s.UseRGB != c.rgb || s.Band != c.band || s.Colour != c.colour || (s.UseRGB && s.RGB != r.At(c.pct)) {
			t.Errorf("%+v at %v: %+v", c.caps, c.pct, s)
		}
	}
}
