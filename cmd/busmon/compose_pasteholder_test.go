package main

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var kShiftEnd = tea.Key{Code: tea.KeyEnd, Mod: tea.ModShift}

const tenLines = "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\nl10"

// The person: a multi-line paste shows as "[Pasted text #1 +9 lines]" in the
// text, as Claude Code's input does; the whole text is kept and sent.
func TestComposeMultiLinePasteIsAPlaceholder(t *testing.T) {
	m := composeWith(t, "see ")
	m = step(t, m, tea.PasteMsg{Content: tenLines})
	a := &m.ui.compose.area
	if got := string(a.text); got != "see [Pasted text #1 +9 lines]" {
		t.Fatalf("shown %q", got)
	}
	if got := a.value(); got != "see "+tenLines {
		t.Fatalf("value %q", got)
	}
	if !strings.Contains(m.grid().plain(), "[Pasted text #1 +9 lines]") {
		t.Fatalf("not on screen:\n%s", m.grid().plain())
	}
	// A one-line paste stays as it is.
	m = step(t, m, tea.PasteMsg{Content: " and more"})
	if got := string(a.text); got != "see [Pasted text #1 +9 lines] and more" {
		t.Fatalf("one-line paste: %q", got)
	}
}

// The whole text goes out: a message sends the paste, not its placeholder.
func TestComposePastedTextIsSentWhole(t *testing.T) {
	pinLocal(t)
	fw := &fakeWriter{}
	m := keys(t, shellWith(t, 140, 40, shellConfig{w: fw}, false), 'I')
	m = typeText(t, m, "@cod")
	m = keys(t, m, kTab)
	m = step(t, m, tea.PasteMsg{Content: "a\nb"})
	m, cmd := pressCmd(t, m, kCtrlS)
	if m = run(t, m, cmd); !slices.Equal(fw.sent, []string{"cmd coder a\nb"}) {
		t.Fatalf("sent %q flash %q", fw.sent, m.ui.flash)
	}
}

// Backspace at a placeholder's end removes the whole paste, Del at its start
// too; the arrows step over it as one.
func TestComposePlaceholderGoesWhole(t *testing.T) {
	m := composeWith(t, "x")
	m = step(t, m, tea.PasteMsg{Content: "a\nb"})
	m = keys(t, m, kBackspce)
	if got := m.ui.compose.area.value(); got != "x" {
		t.Fatalf("backspace left %q", got)
	}
	m = step(t, m, tea.PasteMsg{Content: "a\nb"})
	m = keys(t, m, kLeftPlain)
	if m.ui.compose.area.cur != 1 {
		t.Fatalf("left stepped into the placeholder: cursor %d", m.ui.compose.area.cur)
	}
	m = keys(t, m, kDelete)
	if got := m.ui.compose.area.value(); got != "x" {
		t.Fatalf("del left %q", got)
	}
	// Undo brings the paste back, whole.
	m = keys(t, m, tea.Key{Code: 'z', Mod: tea.ModCtrl})
	if got := m.ui.compose.area.value(); got != "xa\nb" {
		t.Fatalf("undo: %q", got)
	}
}

// Each input numbers its own pastes, from 1.
func TestComposePlaceholderNumbering(t *testing.T) {
	m := composeWith(t, "")
	m = step(t, m, tea.PasteMsg{Content: "a\nb"})
	m = step(t, m, tea.PasteMsg{Content: "c\nd\ne"})
	if got := string(m.ui.compose.area.text); got != "[Pasted text #1 +1 lines][Pasted text #2 +2 lines]" {
		t.Fatalf("shown %q", got)
	}
	if got := m.ui.compose.area.value(); got != "a\nbc\nd\ne" {
		t.Fatalf("value %q", got)
	}
	m.ui.compose.area.set("")
	m = step(t, m, tea.PasteMsg{Content: "f\ng"})
	if got := string(m.ui.compose.area.text); got != "[Pasted text #1 +1 lines]" {
		t.Fatalf("a new text starts at 1: %q", got)
	}
}

// The limit counts the pasted text, not its placeholder.
func TestComposePlaceholderCountsTheLimit(t *testing.T) {
	m := composeWith(t, "")
	a := &m.ui.compose.area
	a.limit = 5
	m = step(t, m, tea.PasteMsg{Content: "ab\ncd\nef"})
	if got := a.value(); got != "ab\ncd" || !m.ui.flashErr {
		t.Fatalf("value %q flash %q", got, m.ui.flash)
	}
	m = keys(t, m, 'z')
	if got := a.value(); got != "ab\ncd" {
		t.Fatalf("typed past the limit: %q", got)
	}
}

// Shift+Home and Shift+End select to the line's ends; typing replaces it.
func TestComposeShiftHomeEndSelect(t *testing.T) {
	m := composeWith(t, "hello world")
	m = keys(t, m, kLeftPlain, kLeftPlain, kLeftPlain, kLeftPlain, kLeftPlain, kShiftEnd)
	if got := selected(m); got != "world" {
		t.Fatalf("shift+end selected %q", got)
	}
	m = keys(t, m, kEnd, kLeftPlain, kLeftPlain, kLeftPlain, kLeftPlain, kLeftPlain, kShiftHome)
	if got := selected(m); got != "hello " {
		t.Fatalf("shift+home selected %q", got)
	}
	m = keys(t, m, 'X')
	if got := m.ui.compose.area.value(); got != "Xworld" {
		t.Fatalf("typing over: %q", got)
	}
}

// A cursor put inside a placeholder (a click, Up/Down, a wrapped row's Home)
// lands at its nearer end, so typing never breaks it and the paste is never
// lost (reviewer-codex); a selection's ends land on its ends too.
func TestComposePlaceholderStaysWhole(t *testing.T) {
	m := composeWith(t, "")
	m = step(t, m, tea.PasteMsg{Content: "a\nb"})
	a := &m.ui.compose.area
	ph := string(a.text)
	_, tr := m.composeRects()
	a.clickAt(tr.W, 3, 0) // inside, nearer its start
	if a.cur != 0 {
		t.Fatalf("a click inside: cursor %d", a.cur)
	}
	a.clickAt(tr.W, len(ph)-3, 0) // nearer its end
	if a.cur != len([]rune(ph)) {
		t.Fatalf("a click near the end: cursor %d", a.cur)
	}
	a.cur = 3 // however it got there
	m = keys(t, m, 'X')
	if got := m.ui.compose.area.value(); got != "Xa\nb" {
		t.Fatalf("typed inside: %q", got)
	}
	// A selection from inside the placeholder to the end takes it whole.
	a = &m.ui.compose.area
	a.cur, a.anchor, a.mark = len(a.text), 5, true
	m = keys(t, m, kBackspce)
	if got := m.ui.compose.area.value(); got != "X" {
		t.Fatalf("a selection from inside: %q", got)
	}
	m = keys(t, m, tea.Key{Code: 'z', Mod: tea.ModCtrl})
	a = &m.ui.compose.area
	a.cur, a.anchor, a.mark = 0, len(a.text)-4, true // from the start into it: nearer its end
	m = keys(t, m, 'Y')
	if got := m.ui.compose.area.value(); got != "Y" {
		t.Fatalf("a selection into it: %q", got)
	}
}
