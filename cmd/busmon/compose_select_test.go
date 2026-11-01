package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var (
	kShiftLeft  = tea.Key{Code: tea.KeyLeft, Mod: tea.ModShift}
	kShiftRight = tea.Key{Code: tea.KeyRight, Mod: tea.ModShift}
	kShiftHome  = tea.Key{Code: tea.KeyHome, Mod: tea.ModShift}
	kShiftUp    = tea.Key{Code: tea.KeyUp, Mod: tea.ModShift}
	kLeftPlain  = tea.Key{Code: tea.KeyLeft}
	kPgUp       = tea.Key{Code: tea.KeyPgUp}
	kPgDn       = tea.Key{Code: tea.KeyPgDown}
)

// composeWith is the shell with the compose window open on the text, the
// cursor at its end.
func composeWith(t *testing.T, text string) Model {
	t.Helper()
	m := fixtureShell(t, 120, 40)
	m.openMessageCompose("", "coder") // addressed: the cursor is in the text
	m = typeText(t, m, text)
	if m.ui.compose == nil || m.ui.compose.area.value() != text {
		t.Fatalf("compose: %+v", m.ui.compose)
	}
	return m
}

func selected(m Model) string {
	a := &m.ui.compose.area
	from, to, ok := a.selection()
	if !ok {
		return ""
	}
	return string(a.text[from:to])
}

// Shift and the arrows (and Home/End) extend a range from where it began;
// typing replaces it, Backspace or Del removes it (the person).
func TestComposeSelectionReplacesAndRemoves(t *testing.T) {
	m := composeWith(t, "hello world")
	for i := 0; i < 5; i++ {
		m = keys(t, m, kShiftLeft)
	}
	if got := selected(m); got != "world" {
		t.Fatalf("shift+left ×5 selected %q", got)
	}
	m = keys(t, m, 'X')
	if got := m.ui.compose.area.value(); got != "hello X" || selected(m) != "" {
		t.Fatalf("typing over the selection: %q, still selected %q", got, selected(m))
	}
	m = keys(t, m, kShiftHome, kBackspce)
	if got := m.ui.compose.area.value(); got != "" {
		t.Fatalf("shift+home then backspace left %q", got)
	}
	m = typeText(t, m, "abc def")
	m = keys(t, m, kShiftLeft, kShiftLeft, kShiftLeft, kDelete)
	if got := m.ui.compose.area.value(); got != "abc " {
		t.Fatalf("del on the selection left %q", got)
	}
	// A selection back and forth: shift+right shrinks it from the same anchor.
	m = keys(t, m, kShiftLeft, kShiftLeft, kShiftRight)
	if got := selected(m); got != " " {
		t.Fatalf("shrunk selection %q", got)
	}
}

// A plain move clears the selection and moves; Esc clears it and keeps the
// window, a second Esc closes it as before.
func TestComposeSelectionClears(t *testing.T) {
	m := composeWith(t, "one two")
	m = keys(t, m, kShiftLeft, kShiftLeft, kLeftPlain)
	if selected(m) != "" || m.ui.compose.area.cur != 4 {
		t.Fatalf("a plain move: selected %q, cursor %d", selected(m), m.ui.compose.area.cur)
	}
	m = keys(t, m, kShiftLeft, kEsc)
	if m.ui.compose == nil || selected(m) != "" {
		t.Fatalf("esc with a selection: window %v, selected %q", m.ui.compose != nil, selected(m))
	}
	if m = keys(t, m, kEsc); m.ui.compose != nil {
		t.Fatal("the second esc did not close the window")
	}
}

// The selection is drawn: its cells reversed, the rest plain.
func TestComposeSelectionIsDrawn(t *testing.T) {
	m := composeWith(t, "keep this")
	m = keys(t, m, kShiftLeft, kShiftLeft, kShiftLeft, kShiftLeft)
	a := &m.ui.compose.area
	_, tr := m.composeRects()
	var reversed strings.Builder
	for _, l := range a.lines(tr.W, tr.H, true, "") {
		for _, s := range l {
			if s.Reverse {
				reversed.WriteString(s.Text)
			}
		}
	}
	if got := reversed.String(); got != "this" {
		t.Fatalf("reversed cells %q, want the selection", got)
	}
}

// PageUp/PageDown move the cursor a page of the window's rows, and extend a
// selection with Shift.
func TestComposePageUpDown(t *testing.T) {
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, "line")
	}
	m := fixtureShell(t, 120, 40)
	m.openMessageCompose("", "coder")
	m.pasteCompose(strings.Join(lines, "\n"))
	_, tr := m.composeRects()
	a := &m.ui.compose.area
	last, _ := a.cursorAt(a.prepare(tr.W))
	m = keys(t, m, kPgUp)
	a = &m.ui.compose.area
	if row, _ := a.cursorAt(a.prepare(tr.W)); row != last-tr.H {
		t.Fatalf("pgup: row %d, want %d (a page of %d)", row, last-tr.H, tr.H)
	}
	m = keys(t, m, kPgDn)
	a = &m.ui.compose.area
	if row, _ := a.cursorAt(a.prepare(tr.W)); row != last {
		t.Fatalf("pgdn: row %d, want %d", row, last)
	}
	m = keys(t, m, kShiftUp)
	if got := selected(m); got != "\nline" {
		t.Fatalf("shift+up selected %q", got)
	}
}

// A selection belongs to its field: switching fields (Tab) ends it, so the
// first Esc after one never closes a window that still showed a selection
// elsewhere (reviewer-codex); and Esc clears a selection in either field.
func TestComposeSelectionAcrossFields(t *testing.T) {
	m := composeWith(t, "body text")
	m = keys(t, m, kShiftLeft, kShiftLeft, kTab)
	if _, _, ok := m.ui.compose.area.selection(); ok {
		t.Fatal("tab to the recipient kept the body's selection")
	}
	if m = keys(t, m, kEsc); m.ui.compose != nil {
		t.Fatal("esc with no selection left did not close the window")
	}
	// Esc clears a selection wherever it is, even with the focus on the other field.
	m = composeWith(t, "body text")
	m = keys(t, m, kShiftLeft)
	m.ui.compose.onTo = true // the focus moved without the key handler (as a click on the recipient row does)
	if m = keys(t, m, kEsc); m.ui.compose == nil {
		t.Fatal("esc closed the window while the body held a selection")
	}
	if _, _, ok := m.ui.compose.area.selection(); ok {
		t.Fatal("esc did not clear the body's selection")
	}
}

// A click that moves the focus ends a selection in either field: one made in
// the recipient no longer catches the next keys (reviewer-codex).
func TestComposeClickEndsSelectionsInBothFields(t *testing.T) {
	m := composeWith(t, "body")
	c := m.ui.compose
	c.onTo = true
	c.to.set("coder")
	c.to.anchor, c.to.mark = 0, true // "coder" selected in the recipient
	f, tr := m.composeRects()
	m.composeClick(tr.X, tr.Y) // into the text
	if _, _, ok := m.ui.compose.to.selection(); ok {
		t.Fatal("a click into the text kept the recipient's selection")
	}
	m.ui.compose.area.anchor, m.ui.compose.area.cur, m.ui.compose.area.mark = 0, 4, true // "body" selected
	m.composeClick(tr.X+1, tr.Y)
	if _, _, ok := m.ui.compose.area.selection(); ok {
		t.Fatal("a click into the text kept the text's own selection (foureyes)")
	}
	m.ui.compose.area.anchor, m.ui.compose.area.cur, m.ui.compose.area.mark = 0, 4, true // "body" selected
	m.ui.compose.to.anchor, m.ui.compose.to.mark = 0, true
	m.composeClick(f.X+2, f.Y+1) // the recipient row
	if _, _, ok := m.ui.compose.to.selection(); ok {
		t.Fatal("a click on the recipient row kept its selection")
	}
	if _, _, ok := m.ui.compose.area.selection(); ok {
		t.Fatal("a click on the recipient row kept the body's selection")
	}
}
