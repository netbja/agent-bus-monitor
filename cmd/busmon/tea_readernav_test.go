package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A5: once a line is open in the reader, n (or right) opens the next item of
// the list it came from and p (or left) the previous one, without going back.
func TestReaderNextPrevious(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	m.focusPanel(PanelActivity)
	m.refresh()
	act := m.lists.of(PanelActivity)
	act.Move(-1)
	act.Move(-1) // an older line, so there is a next one
	evOf := func(m Model) string {
		fl, ok := m.feedLine(m.lists.of(PanelActivity).Selected())
		if !ok {
			t.Fatalf("no feed line for %q", m.lists.of(PanelActivity).Selected())
		}
		return fl.ev.ID
	}
	start := evOf(m)
	m = keys(t, m, kEnter, kEnter)
	p := m.rd.top()
	if p == nil || p.kind != ovMessage || p.ev.ID != start {
		t.Fatalf("opened %+v, want message %s", p, start)
	}
	depth := len(m.rd.pages)
	m = keys(t, m, 'n')
	next := evOf(m)
	if p := m.rd.top(); next == start || p == nil || p.kind != ovMessage || p.ev.ID != next || len(m.rd.pages) != depth {
		t.Fatalf("n: selected %s, page %+v, depth %d (want %d)", next, p, len(m.rd.pages), depth)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyLeft})
	if p := m.rd.top(); p == nil || p.ev.ID != start {
		t.Fatalf("left: page %+v, want %s", p, start)
	}
	m = keys(t, m, tea.Key{Code: tea.KeyRight}, 'p')
	if p := m.rd.top(); p == nil || p.ev.ID != start {
		t.Fatalf("right then p: page %+v, want %s", p, start)
	}
	// At the newest line, n stays and says so.
	for i := 0; i < 20; i++ {
		m = keys(t, m, 'n')
	}
	last := m.rd.top().ev.ID
	m = keys(t, m, 'n')
	if m.rd.top().ev.ID != last || m.ui.flash == "" {
		t.Fatalf("n past the end: page %s flash %q", m.rd.top().ev.ID, m.ui.flash)
	}
	// A page not opened from a list (the help) ignores n.
	m = keys(t, m, kEsc)
	m = keys(t, m, '?')
	if top := m.rd.top(); top == nil || top.kind != ovHelp {
		t.Fatalf("help: %+v", top)
	}
	m = keys(t, m, 'n')
	if top := m.rd.top(); top == nil || top.kind != ovHelp {
		t.Fatalf("n on help: %+v", top)
	}
}
