package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/config"
)

// settings-focus-line: the page's top-margin row says in words where the
// focus is, and which key moves it: wide (the sidebar) and narrow (the tab
// bar), on the groups and on the keys.
func TestSettingsFocusLineTexts(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	for _, c := range []struct {
		width int
		keys  bool
		want  string
		word  string
	}{
		{96, false, "focus: groups " + GlyphNone + " " + GlyphArrowR + " or tab: the keys", "groups"},
		{96, true, "focus: keys " + GlyphNone + " " + GlyphArrowL + " or tab: the groups", "keys"},
		{70, false, "focus: tabs " + GlyphNone + " " + GlyphDownArrow + " or tab: the keys", "tabs"},
		{70, true, "focus: keys " + GlyphNone + " " + GlyphArrowL + " or tab: the tabs", "keys"},
	} {
		set := &settingsPage{groups: settingsGroups(getenv, settingsFlags{}, settingsPageGroups), loaded: true, keys: c.keys}
		lines := settingsBody(set, 0, c.width)
		row := lines[0]
		if !strings.Contains(row.text(), c.want) {
			t.Fatalf("%d keys=%v: %q, want %q", c.width, c.keys, row.text(), c.want)
		}
		side := c.width >= settingsSideMin
		if side != strings.Contains(row.text(), glyphVertical) {
			t.Fatalf("%d: the sidebar's rule through the row: %q", c.width, row.text())
		}
		if side && strings.Index(row.text(), "focus:") < strings.Index(row.text(), glyphVertical) {
			t.Fatalf("%d: the line is not in the keys' column: %q", c.width, row.text())
		}
		for _, s := range row {
			switch {
			case s.Text == c.word:
				if s.Role != RoleText || !s.Bold {
					t.Fatalf("%d: the focused word %q is %v bold=%v", c.width, s.Text, s.Role, s.Bold)
				}
			case strings.TrimSpace(s.Text) == "" || s.Text == glyphVertical || strings.Trim(s.Text, GlyphFocusBar) == "": // the bar: settings_focusbar_test
			default:
				if s.Role != RoleMuted || s.Bold {
					t.Fatalf("%d: %q is %v bold=%v, want muted", c.width, s.Text, s.Role, s.Bold)
				}
			}
		}
	}
}

// The words carry the meaning: cut to a narrow column, the hint goes first,
// then "focus:", and the focused word is kept.
func TestSettingsFocusLineFit(t *testing.T) {
	for _, c := range []struct {
		keys, side bool
		width      int
		want, lost string
	}{
		{false, false, 40, "focus: tabs", ""},
		{false, false, 20, "focus: tabs", "or tab"},
		{true, false, 10, "keys", "focus:"},
		{false, true, 8, "groups", "focus:"},
	} {
		got := settingsFocusLine(c.keys, c.side, false, c.width)
		if got.Width() > c.width {
			t.Fatalf("%+v: %d cells", c, got.Width())
		}
		if !strings.Contains(got.text(), c.want) || (c.lost != "" && strings.Contains(got.text(), c.lost)) {
			t.Fatalf("%+v: %q", c, got.text())
		}
	}
}

// The line follows the focus: tab, → and ← on the wide page; ↓ from the tabs,
// ← from a later key and ↑ on the first key on the narrow page.
func TestSettingsFocusLineFollowsKeys(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	word := func(t *testing.T, m Model) string {
		t.Helper()
		row := m.rd.top().body[0].text()
		for _, w := range []string{"groups", "tabs", "keys"} {
			if strings.Contains(row, "focus: "+w) {
				return w
			}
		}
		t.Fatalf("no focus line: %q", row)
		return ""
	}
	wide := shellWith(t, 140, 40, shellConfig{getenv: getenv}, false)
	wide = press(t, wide, 'C')
	for _, step := range []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: tea.KeyTab}, "keys"},
		{tea.Key{Code: tea.KeyTab}, "groups"},
		{tea.Key{Code: tea.KeyRight}, "keys"},
		{tea.Key{Code: tea.KeyLeft}, "groups"},
	} {
		wide = press(t, wide, step.key)
		if got := word(t, wide); got != step.want {
			t.Fatalf("wide, after %v: %s, want %s", step.key, got, step.want)
		}
	}
	narrow := shellWith(t, 80, 24, shellConfig{getenv: getenv}, false)
	narrow = showGroup(t, press(t, narrow, 'C'), config.GroupNotifications)
	if narrow.readerWidth() >= settingsSideMin {
		t.Fatalf("reader %d cells: not the narrow layout", narrow.readerWidth())
	}
	for _, step := range []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: tea.KeyTab}, "keys"},
		{tea.Key{Code: tea.KeyTab}, "tabs"},
		{tea.Key{Code: tea.KeyDown}, "keys"},
		{tea.Key{Code: tea.KeyDown}, "keys"}, // the second key
		{tea.Key{Code: tea.KeyLeft}, "tabs"}, // ← from a later key
		{tea.Key{Code: tea.KeyDown}, "keys"}, // back on the second key (← keeps the selection)
		{tea.Key{Code: tea.KeyUp}, "keys"},   // ↑ on a later key moves up
		{tea.Key{Code: tea.KeyUp}, "tabs"},   // ↑ on the first key
	} {
		narrow = press(t, narrow, step.key)
		if got := word(t, narrow); got != step.want {
			t.Fatalf("narrow, after %v (sel %d): %s, want %s", step.key, narrow.rd.top().sel, got, step.want)
		}
	}
}

// The line takes the margin's place: the first group and key rows keep their
// line numbers, so selLine is unchanged.
func TestSettingsFocusLineKeepsRows(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	for _, w := range []int{96, 70} {
		for _, keys := range []bool{false, true} {
			set := &settingsPage{groups: settingsGroups(getenv, settingsFlags{}, settingsPageGroups), loaded: true, keys: keys}
			lines := settingsBody(set, 0, w)
			if !strings.Contains(lines[1].text(), "Display") {
				t.Fatalf("%d: row 1 is %q", w, lines[1].text())
			}
			want := -1 // the first key's line: where its label is drawn
			for i, l := range lines {
				if strings.Contains(l.text(), set.rows()[0].Label) {
					want = i
					break
				}
			}
			if set.selLine != want {
				t.Fatalf("%d keys=%v: selLine %d, want %d", w, keys, set.selLine, want)
			}
		}
	}
}

// The line is on screen whenever the groups (or tabs) have the focus or the
// first key is selected: a move of the focus back from a scrolled key, or ↑
// to the first key, scrolls to the top (foureyes' probe).
func TestSettingsFocusLineInView(t *testing.T) {
	getenv := settingsEnv(t, "", "", map[string]string{"AGENT_BUS_NO_UPDATE_CHECK": "1"})
	shown := func(t *testing.T, m Model, what string) {
		t.Helper()
		p := m.rd.top()
		if p.top != 0 || !strings.Contains(p.body[0].text(), "focus:") {
			t.Fatalf("%s: top %d, row 0 %q", what, p.top, p.body[0].text())
		}
	}
	walked := func(t *testing.T, w, h int) Model {
		t.Helper()
		m := shellWith(t, w, h, shellConfig{getenv: getenv}, false)
		m = showGroup(t, press(t, m, 'C'), config.GroupNotifications)
		into := tea.Key{Code: tea.KeyRight} // wide: from the groups to the first key
		if w < 120 {
			into = tea.Key{Code: tea.KeyDown} // narrow: from the tabs
		}
		m = press(t, m, into)
		for i := 1; i < len(m.rd.top().set.rows()); i++ {
			m = press(t, m, tea.Key{Code: tea.KeyDown})
		}
		if m.rd.top().top == 0 {
			t.Fatalf("%dx%d: the last key did not scroll the page", w, h)
		}
		return m
	}
	for _, size := range [][2]int{{120, 20}, {80, 16}} {
		for _, k := range []tea.Key{{Code: tea.KeyTab}, {Code: tea.KeyLeft}} {
			m := press(t, walked(t, size[0], size[1]), k)
			if m.rd.top().set.keys {
				t.Fatalf("%v: the focus stayed on the keys", k)
			}
			shown(t, m, fmt.Sprintf("%dx%d, after %v from a scrolled key", size[0], size[1], k.Code))
		}
	}
	m := walked(t, 80, 16)
	for m.rd.top().sel > 0 {
		m = press(t, m, tea.Key{Code: tea.KeyUp})
	}
	shown(t, m, "narrow, the first key")
	m = press(t, m, tea.Key{Code: tea.KeyUp})
	if m.rd.top().set.keys {
		t.Fatal("↑ on the first key: the focus stayed on the keys")
	}
	shown(t, m, "narrow, back to the tabs")
}
