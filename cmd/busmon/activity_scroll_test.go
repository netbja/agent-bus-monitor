package main

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/netbja/agent-bus-monitor/bus"
)

// activity-scroll-bug (the person, 2026-11-07): Up to the top of Activity
// must bring the first rows into view; the selection stays visible at both
// ends, wide and narrow.
func longActivity(t *testing.T, w, h int) Model {
	t.Helper()
	m := fixtureShell(t, w, h)
	var evs []bus.Event
	for i := 0; i < 80; i++ {
		evs = append(evs, bus.Event{ID: fmt.Sprintf("%d-0", shellNow.Add(time.Duration(i-80)*time.Second).UnixMilli()),
			Kind: "status", Agent: "coder", State: "working", Message: fmt.Sprintf("line %d", i)})
	}
	m = step(t, m, tailMsg{events: evs})
	m = step(t, m, tea.KeyPressMsg(tea.Key{Code: '1', Text: "1"}))
	m.focusPanel(PanelActivity)
	m.refresh()
	return m
}

func selVisible(t *testing.T, m Model, what string) {
	t.Helper()
	m.grid() // draw: the viewport follows
	l := m.lists.of(PanelActivity)
	i := l.index(l.Selected())
	if i < 0 {
		t.Fatalf("%s: nothing selected", what)
	}
	if i < l.top || i >= l.top+l.rows() {
		t.Fatalf("%s: selection %d outside the view [%d, %d)", what, i, l.top, l.top+l.rows())
	}
}

func TestActivitySelectionVisibleAtBothEnds(t *testing.T) {
	for _, size := range [][2]int{{140, 40}, {100, 30}, {70, 24}} {
		m := longActivity(t, size[0], size[1])
		if m.layout.Tier == TierNarrow {
			m.ui.view = PanelActivity
			m.refresh()
		}
		n := len(m.lists.of(PanelActivity).items)
		for i := 0; i < n+5; i++ {
			m = keys(t, m, 'k')
			selVisible(t, m, fmt.Sprintf("%dx%d up %d", size[0], size[1], i+1))
		}
		m.grid()
		if l := m.lists.of(PanelActivity); l.top != 0 {
			t.Errorf("%dx%d: at the first row the view starts at %d, not the top", size[0], size[1], l.top)
		}
		for i := 0; i < n+5; i++ {
			m = keys(t, m, 'j')
			selVisible(t, m, fmt.Sprintf("%dx%d down %d", size[0], size[1], i+1))
		}
	}
}

// A selection inside the live window keeps auto-scroll (charm spec); moving
// past it pauses, as a wheel-up does; down onto the newest resumes (A3).
func TestActivityUpLeavesLiveOnlyPastTheWindow(t *testing.T) {
	m := longActivity(t, 140, 40)
	l := &m.lists.activity
	if !l.Live() {
		t.Fatal("fixture not live")
	}
	m = keys(t, m, 'k', 'k')
	if l = &m.lists.activity; !l.Live() || l.Selected() == "" {
		t.Fatalf("a selection inside the window left live (live %v)", l.Live())
	}
	for i := 0; i < l.rows()+2; i++ {
		m = keys(t, m, 'k')
	}
	if l = &m.lists.activity; l.Live() {
		t.Fatal("past the window, still live")
	}
	for i := 0; i < len(l.items); i++ {
		m = keys(t, m, 'j')
	}
	if l = &m.lists.activity; !l.Live() {
		t.Fatal("down onto the newest did not resume")
	}
}

// More rows no one selects above the selection than the view holds: the
// selection still shows, the rows above give way.
func TestViewportUpKeepsTheSelectionOverTallSeparators(t *testing.T) {
	l := &listState{rect: Rect{0, 0, 20, 5}} // 3 rows
	for i := 0; i < 6; i++ {
		l.items = append(l.items, ListItem{Key: fmt.Sprintf("sep%d", i)})
	}
	l.items = append(l.items, ListItem{Key: "a", Selectable: true}, ListItem{Key: "b", Selectable: true},
		ListItem{Key: "c", Selectable: true}, ListItem{Key: "d", Selectable: true})
	l.top = 7
	l.Select("a")
	l.viewport()
	if i := l.index("a"); i < l.top || i >= l.top+l.rows() {
		t.Fatalf("selection %d outside [%d, %d)", i, l.top, l.top+l.rows())
	}
	if l.top != 4 {
		t.Errorf("top %d, want 4: the separators fill the rows above it", l.top)
	}
}
