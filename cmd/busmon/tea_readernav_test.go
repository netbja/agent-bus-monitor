package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
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

// Review (reviewer-codex): in For you, opening an unread line marks it read
// and its row leaves the list; n must still go to the line after the one
// shown, and p back to it, not follow the list's shifted selection.
func TestReaderNextInForYouUnread(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	// Two more lines for the person, from other senders (a newer line from
	// the same sender would supersede the older one).
	at := func(d time.Duration) string { return fmt.Sprintf("%d-0", shellNow.Add(-d).UnixMilli()) }
	m = step(t, m, tailMsg{events: []bus.Event{
		{ID: at(90 * time.Second), Kind: "cmd", Type: bus.CmdDirective, From: "architect", Target: "bernard", Message: "spec question"},
		{ID: at(60 * time.Second), Kind: "cmd", Type: bus.CmdDirective, From: "sentinel", Target: "bernard", Message: "health note"},
	}})
	m.focusPanel(PanelForYou)
	m.refresh()
	var unread []string
	for _, it := range m.forYou {
		if it.Rank == rankUnread {
			unread = append(unread, it.Key)
		}
	}
	if len(unread) < 3 {
		t.Fatalf("fixture has %d unread For you lines, want 3+", len(unread))
	}
	evOf := func(key string) string { return strings.TrimPrefix(key, "unread:") }
	m.lists.of(PanelForYou).Select(unread[0])
	m = keys(t, m, kEnter, kEnter)
	if p := m.rd.top(); p == nil || p.ev.ID != evOf(unread[0]) {
		t.Fatalf("opened %+v, want %s", p, unread[0])
	}
	m = keys(t, m, 'n')
	if p := m.rd.top(); p == nil || p.ev.ID != evOf(unread[1]) {
		t.Fatalf("n after the first unread: %+v (flash %q), want %s", p, m.ui.flash, unread[1])
	}
	m = keys(t, m, 'n')
	if p := m.rd.top(); p == nil || p.ev.ID != evOf(unread[2]) {
		t.Fatalf("n again: %+v (flash %q), want %s (none skipped)", p, m.ui.flash, unread[2])
	}
	m = keys(t, m, 'p', 'p')
	if p := m.rd.top(); p == nil || p.ev.ID != evOf(unread[0]) {
		t.Fatalf("p p back: %+v (flash %q), want %s", p, m.ui.flash, unread[0])
	}
}
