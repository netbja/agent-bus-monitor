package main

import (
	"maps"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// betweenShell is the fixture shell at 140×40 whose board holds a request
// between agents, one the person sent, one owed to the person and a plain task.
func betweenShell(t *testing.T) Model {
	t.Helper()
	m := shellWith(t, 140, 40, shellConfig{}, false)
	snap := m.snap
	snap.board = maps.Clone(snap.board)
	req := func(from, to string) *bus.RequestInfo {
		return &bus.RequestInfo{ID: "1-0", From: from, Target: to, CreatedAt: shellNow.Add(-time.Minute).UnixMilli()}
	}
	at := shellNow.Add(-time.Minute).Unix()
	snap.board["agents-only"] = bus.BoardEntry{Owner: "coder", State: "requested", Updated: at, Request: req("master", "coder")}
	snap.board["from-person"] = bus.BoardEntry{Owner: "coder", State: "requested", Updated: at, Request: req("charlie", "coder")}
	snap.board["to-person"] = bus.BoardEntry{Owner: "charlie", State: "requested", Updated: at, Request: req("master", "charlie")}
	return step(t, m, pollMsg{snap: snap, at: shellNow})
}

// views-redesign §3, the person's choice (a): Work's "between agents" line
// opens the Board filtered to the rows between agents (no request to or from
// the person); Esc returns to Work on that line, the filter gone.
func TestWorkBetweenOpensTheFilteredBoard(t *testing.T) {
	pinLocal(t)
	m := keys(t, betweenShell(t), '3')
	m.lists.work.Select(workFoldBetween)
	m.ui.workLevel = workAtCards
	m = keys(t, m, kEnter)
	if m.ui.focus != PanelBoard || !m.ui.boardBetween || m.rd.open() {
		t.Fatalf("enter on between agents: focus %v filter %v page %v", m.ui.focus, m.ui.boardBetween, m.rd.open())
	}
	got := strings.Join(keysOf(m.lists.board.items), " ")
	if !strings.Contains(got, "agents-only") || !strings.Contains(got, "busmon-charm-b") || strings.Contains(got, "person") {
		t.Fatalf("filtered rows %q", got)
	}
	if !strings.Contains(m.grid().plain(), "between agents "+GlyphNone) {
		t.Fatalf("no filter in the label:\n%s", m.grid().plain())
	}
	m = keys(t, m, kEsc)
	if m.ui.focus != PanelWork || m.ui.boardBetween || m.lists.work.Selected() != workFoldBetween {
		t.Fatalf("esc: focus %v filter %v selected %q", m.ui.focus, m.ui.boardBetween, m.lists.work.Selected())
	}
	if got := strings.Join(keysOf(keys(t, m, '2').lists.board.items), " "); !strings.Contains(got, "from-person") {
		t.Fatalf("the Board keeps the filter: %q", got)
	}
	if h, ok := rowHint(PanelWork, ListItem{Key: workFoldBetween}); !ok || h != "board" {
		t.Fatalf("hint %q", h)
	}
}

// Option (i): r and F2 open the Requests sub-view from anywhere, no page;
// the old read-only Requests page is gone. Its origin, the lower tab it was
// opened from, is what Esc returns to, what the tab bar marks and where the
// arrows step from.
func TestRequestsKeyOpensTheSubViewFromItsOrigin(t *testing.T) {
	pinLocal(t)
	for _, c := range []struct {
		tab  rune
		from PanelID
		next PanelID
	}{{'2', PanelBoard, PanelWork}, {'3', PanelWork, PanelPlan}, {'4', PanelPlan, PanelActivity}, {'1', PanelActivity, PanelBoard}} {
		for _, key := range []any{'r', kF2} {
			m := keys(t, keys(t, betweenShell(t), c.tab), key)
			if m.ui.focus != PanelRequests || m.rd.open() || m.ui.reqFrom != c.from {
				t.Fatalf("%v from %v: focus %v page %v origin %v", key, c.from, m.ui.focus, m.rd.open(), m.ui.reqFrom)
			}
			if tab := m.tabOf(m.ui.focus); tab != c.from { // the tab bar marks the origin
				t.Fatalf("tab of the sub-view %v, want %v", tab, c.from)
			}
			if r := keys(t, m, kEsc); r.ui.focus != c.from {
				t.Fatalf("esc from the sub-view opened from %v: focus %v", c.from, r.ui.focus)
			}
			if r := keys(t, m, kRight); r.ui.focus != c.next {
				t.Fatalf("→ from the sub-view opened from %v: focus %v, want %v", c.from, r.ui.focus, c.next)
			}
		}
	}
}

// d, o, + and - mean the same in the sub-view whatever opened it: o folds
// alike, and + aims at the same request.
func TestRequestsKeysSameFromEveryOrigin(t *testing.T) {
	pinLocal(t)
	var folded []bool
	var targets []string
	for _, tab := range []rune{'2', '3', '1'} {
		m := keys(t, keys(t, betweenShell(t), tab), 'r')
		folded = append(folded, keys(t, m, 'o').ui.reqOpen.folded)
		m.lists.requests.Select("req:agents-only")
		r, why := m.priorityTarget()
		targets = append(targets, r.Task+"|"+why)
		for _, k := range []rune{'d', 'o', '+', '-'} {
			if a := lookup(ModeShell, PanelRequests, m.layout.Tier, Key{Code: keyRune, Rune: k}); a == ActNone {
				t.Fatalf("%c unbound in the sub-view", k)
			}
		}
	}
	if folded[0] != folded[1] || folded[1] != folded[2] || targets[0] != targets[1] || targets[1] != targets[2] {
		t.Fatalf("by origin: o %v, + %q", folded, targets)
	}
}

var kF2 = tea.Key{Code: tea.KeyF2}
