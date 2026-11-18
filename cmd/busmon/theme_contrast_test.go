package main

import (
	"math"
	"strings"
	"testing"
)

// luminance is a colour's relative luminance (WCAG 2).
func luminance(c Color) float64 {
	r, g, b := c.RGB()
	lin := func(v int32) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio of two colours, 1 to 21.
func contrast(a, b Color) float64 {
	la, lb := luminance(a), luminance(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// minContrast is the contrast rule: the body text roles read as body text
// (4.5:1), every other role but the frame line as large text or a mark (3:1).
func minContrast(r Role) float64 {
	switch r {
	case RoleText, RoleMuted:
		return 4.5
	case RoleLine:
		return 1 // a frame line, not something to read
	}
	return 3
}

// A theme forced to light on a dark terminal: light's dark text on the
// terminal's own dark background was unreadable (the person's screenshot,
// 2026-11-18). The light palette paints its background on every cell, so its
// text has the background it was chosen for whatever the terminal's.
func TestLightThemePaintsItsBackground(t *testing.T) {
	lt, err := ResolveTheme("light", "", "", false, 1<<24)
	if err != nil {
		t.Fatal(err)
	}
	white := getColor("#ffffff")
	for r := RoleText; r < roleCount; r++ {
		if _, bg, _ := lt.Style(r).Decompose(); bg != white {
			t.Errorf("light Style(%d) background %v, want the painted %v", r, bg, white)
		}
		if _, bg, _ := lt.FrameBorder(r, false).Decompose(); bg != white {
			t.Errorf("light FrameBorder(%d) background %v, want %v", r, bg, white)
		}
	}
	// A blank screen is painted too: every cell, not only the text.
	g := newGrid(4, 2)
	if out := g.render(newPainter(lt)); strings.Count(out, "48;2;255;255;255") != 2 {
		t.Errorf("a blank light grid paints each row's background: %q", out)
	}
}

// The dark palette keeps the terminal's own background (its transparency and
// colour); only light paints, and the 16-colour and no-colour palettes never.
func TestOnlyLightPaintsTheBackground(t *testing.T) {
	cases := []struct {
		name    string
		colors  int
		noColor bool
	}{{"dark", 1 << 24, false}, {"light", 256, false}, {"light", 1 << 24, true}}
	for _, c := range cases {
		th, err := ResolveTheme(c.name, "", "", c.noColor, c.colors)
		if err != nil {
			t.Fatal(err)
		}
		for r := RoleText; r < roleCount; r++ {
			if _, bg, _ := th.Style(r).Decompose(); bg != colorDefault {
				t.Errorf("%s colors=%d noColor=%v: Style(%d) background %v, want the terminal's", c.name, c.colors, c.noColor, r, bg)
			}
		}
	}
}

// Every role keeps the rule's contrast against the background it is drawn
// on: light's painted white, and for dark the background its palette was
// chosen for (the dark pill's text colour), in true colour.
func TestThemeContrast(t *testing.T) {
	for _, c := range []struct{ name, bg string }{{"dark", "#0D1117"}, {"light", "#FFFFFF"}} {
		th, err := ResolveTheme(c.name, "", "", false, 1<<24)
		if err != nil {
			t.Fatal(err)
		}
		bg := getColor(c.bg)
		for r := RoleText; r < roleCount; r++ {
			fg, _, _ := th.Style(r).Decompose()
			if got := contrast(fg, bg); got < minContrast(r) {
				t.Errorf("%s: role %d (%s) on %s: contrast %.2f, want at least %.1f", c.name, r, th.tags[r], c.bg, got, minContrast(r))
			}
		}
		// The selected row and an inactive tab bring their own background.
		fg, sel, _ := th.Selected(RoleText).Decompose()
		if got := contrast(fg, sel); got < 4.5 {
			t.Errorf("%s: text on the selection: contrast %.2f, want 4.5", c.name, got)
		}
		fg, off, _ := th.TabOff().Decompose()
		if got := contrast(fg, off); got < 4.5 {
			t.Errorf("%s: an inactive tab: contrast %.2f, want 4.5", c.name, got)
		}
	}
}
