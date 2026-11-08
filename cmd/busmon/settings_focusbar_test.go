package main

import (
	"strings"
	"testing"
)

// barCells is the number of focus-bar cells in l, and whether each bar seg
// is the accent, bold.
func barCells(t *testing.T, l Line) int {
	t.Helper()
	n := 0
	for _, s := range l {
		if strings.Contains(s.Text, GlyphFocusBar) {
			if s.Role != RoleAccent || !s.Bold || strings.Trim(s.Text, GlyphFocusBar) != "" {
				t.Fatalf("bar seg %q: %v bold=%v", s.Text, s.Role, s.Bold)
			}
			n += Width(s.Text)
		}
	}
	return n
}

// settings-focus-bar §2: the bar sits on the focused panel's top edge: the
// sidebar's part or the keys' column of row 0 on a wide page; row 0 (above
// the tabs) or row 2 (above the keys) on a narrow one. The text line stays.
func TestSettingsFocusBarPlace(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	page := func(keys bool, w int) []Line {
		set := &settingsPage{groups: settingsGroups(getenv, settingsFlags{}, settingsPageGroups), loaded: true, keys: keys}
		return settingsBody(set, 0, w)
	}
	split := func(l Line) (Line, Line) { // the sidebar's part and the keys' column of a wide row
		for i, s := range l {
			if s.Text == glyphVertical {
				return l[:i], l[i+1:]
			}
		}
		t.Fatalf("no rule in %q", l.text())
		return nil, nil
	}
	// wide, the groups: the bar across the sidebar, none in the keys' column
	side, col := split(page(false, 96)[0])
	if n := barCells(t, side); n != settingsSideW-2 {
		t.Fatalf("wide groups: sidebar bar %d cells, want %d: %q", n, settingsSideW-2, side.text())
	}
	if barCells(t, col) != 0 || !strings.Contains(col.text(), "focus: groups") {
		t.Fatalf("wide groups: keys' column %q", col.text())
	}
	// wide, the keys: none in the sidebar; text then the bar to the column's end
	row := page(true, 96)[0]
	side, col = split(row)
	if barCells(t, side) != 0 || strings.TrimSpace(side.text()) != "" {
		t.Fatalf("wide keys: sidebar %q", side.text())
	}
	if n := barCells(t, col); n < 2 || !strings.Contains(col.text(), "focus: keys") || !strings.HasSuffix(col.text(), GlyphFocusBar) {
		t.Fatalf("wide keys: column %q (%d bar cells)", col.text(), n)
	}
	if row.Width() != 96 {
		t.Fatalf("wide keys: row 0 is %d cells, want the full 96", row.Width())
	}
	// narrow, the tabs: row 0 text then the bar; row 2 blank
	lines := page(false, 70)
	if barCells(t, lines[0]) < 2 || !strings.Contains(lines[0].text(), "focus: tabs") || lines[0].Width() != 70 {
		t.Fatalf("narrow tabs: row 0 %q", lines[0].text())
	}
	if barCells(t, lines[2]) != 0 {
		t.Fatalf("narrow tabs: row 2 %q", lines[2].text())
	}
	// narrow, the keys: row 0 the text alone; row 2 the bar across the full width
	lines = page(true, 70)
	if barCells(t, lines[0]) != 0 || !strings.Contains(lines[0].text(), "focus: keys") {
		t.Fatalf("narrow keys: row 0 %q", lines[0].text())
	}
	if n := barCells(t, lines[2]); n != 70 {
		t.Fatalf("narrow keys: row 2 bar %d cells, want 70: %q", n, lines[2].text())
	}
}

// The text fits first and the bar takes what is left, never under 2 cells;
// the focused word stays.
func TestSettingsFocusBarFit(t *testing.T) {
	for _, c := range []struct {
		keys, side bool
		width      int
		word       string
	}{
		{true, true, 60, "keys"},
		{true, true, 12, "keys"},
		{true, true, 14, "keys"}, // " focus: keys" would fit 13 and leave a 1-cell bar
		{false, false, 10, "tabs"},
		{false, false, 40, "tabs"},
	} {
		l := settingsFocusLine(c.keys, c.side, true, c.width)
		if l.Width() != c.width {
			t.Fatalf("%+v: %d cells, want %d: %q", c, l.Width(), c.width, l.text())
		}
		if n := barCells(t, l); n < 2 || !strings.Contains(l.text(), c.word) {
			t.Fatalf("%+v: %q (%d bar cells)", c, l.text(), n)
		}
	}
	if l := settingsFocusLine(true, true, false, 60); barCells(t, l) != 0 {
		t.Fatalf("no bar asked: %q", l.text())
	}
}

// The bar's stroke is heavy: without colour it still differs from the
// light rules busmon draws elsewhere (§2, NO_COLOR and 16 colours).
func TestSettingsFocusBarStroke(t *testing.T) {
	if GlyphFocusBar == glyphHorizontal || GlyphFocusBar == glyphVertical || Width(GlyphFocusBar) != 1 {
		t.Fatalf("GlyphFocusBar %q", GlyphFocusBar)
	}
}
