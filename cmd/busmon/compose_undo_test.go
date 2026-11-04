package main

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
)

var (
	kUndo      = tea.Key{Code: 'z', Mod: tea.ModCtrl}
	kRedo      = tea.Key{Code: 'y', Mod: tea.ModCtrl}
	kShiftUndo = tea.Key{Code: 'z', Mod: tea.ModCtrl | tea.ModShift}
)

func text(m Model) string { return m.ui.compose.area.value() }

// A bad paste is one step: Ctrl+Z takes it back whole, the cursor where it
// was (the person's case).
func TestComposeUndoAPaste(t *testing.T) {
	m := composeWith(t, "hello ")
	m = step(t, m, tea.PasteMsg{Content: "a very long bad paste\nover two lines"})
	m = keys(t, m, kUndo)
	if got := text(m); got != "hello " || m.ui.compose.area.cur != 6 {
		t.Fatalf("after undo: %q, cursor %d", got, m.ui.compose.area.cur)
	}
}

// A run of typed characters is one step; a move, a line break or a paste
// ends the run.
func TestComposeUndoATypedRun(t *testing.T) {
	m := composeWith(t, "")
	m = typeText(t, m, "abc")
	m = keys(t, m, kUndo)
	if got := text(m); got != "" {
		t.Fatalf("a typed run is one step: %q left", got)
	}
	m = typeText(t, m, "abc")
	m = keys(t, m, kLeftPlain)
	m = typeText(t, m, "x")
	m = keys(t, m, kUndo)
	if got := text(m); got != "abc" {
		t.Fatalf("a move ends the run: %q", got)
	}
	m = keys(t, m, kEnter)
	m = typeText(t, m, "def")
	m = keys(t, m, kUndo)
	if got := text(m); got != "ab\nc" {
		t.Fatalf("a line break ends the run: %q", got)
	}
	m = keys(t, m, kUndo)
	if got := text(m); got != "abc" {
		t.Fatalf("the line break is its own step: %q", got)
	}
	// Deletions are a run too.
	m = keys(t, m, kBackspce, kBackspce)
	m = keys(t, m, kUndo)
	if got := text(m); got != "abc" {
		t.Fatalf("a run of backspaces is one step: %q", got)
	}
}

// Undo past the start of the history does nothing; a key that changes
// nothing is no step.
func TestComposeUndoPastTheStart(t *testing.T) {
	m := composeWith(t, "")
	m = keys(t, m, kBackspce, kUndo, kUndo)
	if got := text(m); got != "" {
		t.Fatalf("undo on an empty history: %q", got)
	}
	m = typeText(t, m, "ab")
	m = keys(t, m, kUndo, kUndo, kUndo)
	if got := text(m); got != "" {
		t.Fatalf("undo past the start: %q", got)
	}
	// Del at the end of the text changes nothing, so it adds no step: one
	// undo still takes the typed run back.
	m = typeText(t, m, "abc")
	m = keys(t, m, kDelete, kUndo)
	if got := text(m); got != "" {
		t.Fatalf("a key that changed nothing took a step: %q", got)
	}
}

// A selection replaced by a paste comes back, selected, on undo.
func TestComposeUndoAPasteOverASelection(t *testing.T) {
	m := composeWith(t, "hello world")
	for range 5 {
		m = keys(t, m, kShiftLeft)
	}
	m = step(t, m, tea.PasteMsg{Content: "PASTED"})
	if got := text(m); got != "hello PASTED" {
		t.Fatalf("paste over the selection: %q", got)
	}
	m = keys(t, m, kUndo)
	if got := text(m); got != "hello world" || selected(m) != "world" {
		t.Fatalf("after undo: %q, selected %q", got, selected(m))
	}
}

// Ctrl+Y (or Ctrl+Shift+Z) redoes what was undone; a new edit drops the redo.
func TestComposeRedo(t *testing.T) {
	m := composeWith(t, "a ")
	m = step(t, m, tea.PasteMsg{Content: "one"})
	m = keys(t, m, kUndo, kRedo)
	if got := text(m); got != "a one" {
		t.Fatalf("redo: %q", got)
	}
	m = keys(t, m, kUndo, kShiftUndo)
	if got := text(m); got != "a one" {
		t.Fatalf("ctrl+shift+z: %q", got)
	}
	m = keys(t, m, kUndo)
	m = typeText(t, m, "b")
	m = keys(t, m, kRedo)
	if got := text(m); got != "a b" {
		t.Fatalf("a new edit drops the redo: %q", got)
	}
}

// The history is bounded: the oldest steps go first.
func TestComposeUndoIsBounded(t *testing.T) {
	m := composeWith(t, "")
	for i := range undoMax + 50 {
		m = step(t, m, tea.PasteMsg{Content: fmt.Sprintf("%d,", i)})
	}
	for range undoMax + 50 {
		m = keys(t, m, kUndo)
	}
	want := ""
	for i := range 50 {
		want += fmt.Sprintf("%d,", i)
	}
	if got := text(m); got != want {
		t.Fatalf("after undoing past the bound: %q, want %q", got, want)
	}
}

// Closing the window ends its history: a reopened draft starts a new one.
func TestComposeUndoEndsWithTheWindow(t *testing.T) {
	m := composeWith(t, "draft")
	m = keys(t, m, kEsc)
	m.openMessageCompose("", "coder")
	if m.ui.compose == nil || text(m) != "draft" {
		t.Fatalf("draft not reopened: %+v", m.ui.compose)
	}
	m = keys(t, m, kUndo)
	if got := text(m); got != "draft" {
		t.Fatalf("undo reached into the closed window's history: %q", got)
	}
}

// A click moves the cursor: typing after it is a new step.
func TestComposeUndoAClickEndsTheRun(t *testing.T) {
	m := composeWith(t, "")
	m = typeText(t, m, "abc")
	m.ui.compose.area.clickAt(80, 0, 0)
	m = typeText(t, m, "x")
	m = keys(t, m, kUndo)
	if got := text(m); got != "abc" {
		t.Fatalf("after a click and undo: %q", got)
	}
}
