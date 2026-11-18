package main

import "testing"

// The 16 ANSI names the palettes use resolve to their terminal index, a hex
// string to true colour (any case), and anything else to the terminal's own.
func TestGetColor(t *testing.T) {
	for name, want := range map[string]int{
		"black": 0, "maroon": 1, "green": 2, "olive": 3, "navy": 4, "purple": 5, "teal": 6, "silver": 7,
		"gray": 8, "red": 9, "lime": 10, "yellow": 11, "blue": 12, "fuchsia": 13, "aqua": 14, "white": 15,
	} {
		c := getColor(name)
		if i, ok := c.index(); !ok || i != want {
			t.Errorf("%s = %+v, want index %d", name, c, want)
		}
	}
	if c := getColor("#7CC4FA"); c != getColor("#7cc4fa") || c != newRGBColor(0x7C, 0xC4, 0xFA) {
		t.Errorf("hex: %+v", c)
	}
	if r, g, b := getColor("#7CC4FA").RGB(); r != 0x7C || g != 0xC4 || b != 0xFA {
		t.Errorf("RGB %d %d %d", r, g, b)
	}
	for _, bad := range []string{"", "-", "nope", "#12345", "#12345g"} {
		if c := getColor(bad); c != colorDefault {
			t.Errorf("%q = %+v, want the terminal's own", bad, c)
		}
	}
	if r, g, b := colorDefault.RGB(); r != -1 || g != -1 || b != -1 {
		t.Errorf("default RGB %d %d %d", r, g, b)
	}
}

// A style is a value: setters return a copy, and Decompose gives back what
// was set.
func TestStyleValue(t *testing.T) {
	base := styleDefault.Foreground(getColor("red"))
	bold := base.Bold(true).Background(getColor("#000000")).Reverse(true)
	if fg, bg, attr := base.Decompose(); fg != getColor("red") || bg != colorDefault || attr != 0 {
		t.Fatalf("base changed: %v %v %v", fg, bg, attr)
	}
	if fg, bg, attr := bold.Decompose(); fg != getColor("red") || bg != newRGBColor(0, 0, 0) || attr != attrBold|attrReverse {
		t.Fatalf("bold: %v %v %v", fg, bg, attr)
	}
	if bold.Bold(false).Reverse(false) != base.Background(getColor("#000000")) {
		t.Fatal("clearing the attributes")
	}
}
