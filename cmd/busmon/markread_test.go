package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/bus"
)

// triage-unread step 1 (spec docs/superpowers/specs/2026-11-26-triage-unread-design.md
// §4): in Activity, N marks every unread line no triage item holds read
// (asked first, the lines unread at the press), m toggles one line, z puts
// back the last act's marks once; on an agent's page N marks that agent's
// lines. Read marks stay this busmon's: nothing is written on the bus.

// mrReport is a report from agent and its notice to the person (charlie),
// at ms milliseconds before shellNow, with needs.
func mrReport(ms int64, agent, needs, text string) []bus.Event {
	at := shellNow.Add(-time.Duration(ms) * time.Millisecond).UnixMilli()
	rep := bus.Event{Kind: "report", ID: fmt.Sprintf("%d-0", at), Agent: agent, RKind: "note", Message: text, Needs: needs}
	n := bus.Event{Kind: "cmd", ID: fmt.Sprintf("%d-1", at), From: agent, Target: "charlie", Type: bus.CmdDirective, Ref: rep.ID,
		Message: "Report " + rep.ID + " for you (full text: agentbus reports " + rep.ID + "): " + text}
	return []bus.Event{rep, n}
}

func mrShell(t *testing.T, evs ...bus.Event) Model {
	t.Helper()
	m := shellWith(t, 140, 40, shellConfig{w: &fakeWriter{}}, false)
	m = step(t, m, tailMsg{events: evs})
	m.focusPanel(PanelActivity)
	m.refresh()
	return m
}

func unreadLine(m Model, id string) bool {
	for _, fl := range m.feed.feed {
		if !fl.sep && fl.ev.ID == id {
			return fl.mark == markUnread
		}
	}
	return false
}

func TestMarkAllRead(t *testing.T) {
	plain := mrReport(60_000, "release", "", "tagged v1")
	plain2 := mrReport(50_000, "release", "", "tagged v2")
	held := mrReport(40_000, "release", bus.NeedsRead, "read the release notes")
	m := mrShell(t, append(append(plain, plain2...), held...)...)
	for _, id := range []string{plain[0].ID, plain2[0].ID, held[0].ID} {
		if !unreadLine(m, id) {
			t.Fatalf("%s is not unread to start with", id)
		}
	}
	m = press(t, m, 'N')
	d := m.ui.dialog
	if d == nil || !strings.Contains(d.Title, "mark ") || !strings.Contains(d.Title, " lines read?") {
		t.Fatalf("N asked nothing: %+v", d)
	}
	n := len(m.ui.markAsk)
	// A line arriving while it asks stays unread: N marks what was unread at the press.
	late := mrReport(1_000, "release", "", "tagged v3")
	m = step(t, m, tailMsg{events: late})
	m = press(t, m, 'y')
	if unreadLine(m, plain[0].ID) || unreadLine(m, plain2[0].ID) {
		t.Error("a plain report stays unread after N")
	}
	if !unreadLine(m, held[0].ID) {
		t.Error("N marked a --needs read report: a triage item's line")
	}
	if !unreadLine(m, late[0].ID) {
		t.Error("a line arriving during the question was marked")
	}
	if !strings.Contains(m.ui.flash, fmt.Sprintf("%d marked read", n)) || !strings.Contains(m.ui.flash, "z undo") {
		t.Errorf("flash %q", m.ui.flash)
	}
	if !slices.Contains(m.cache_().Read, plain[1].ID) {
		t.Errorf("the marks are not in the local cache: %v", m.cache_().Read)
	}
	// z: the act undone, once.
	m = press(t, m, 'z')
	if !unreadLine(m, plain[0].ID) || !unreadLine(m, plain2[0].ID) || slices.Contains(m.cache_().Read, plain[1].ID) {
		t.Error("z did not put the marks back")
	}
	m = press(t, m, 'z')
	if !strings.Contains(m.ui.flash, "nothing to undo") {
		t.Errorf("a second z: %q", m.ui.flash)
	}
	// Any other answer marks nothing.
	m = press(t, m, 'N')
	m = press(t, m, 'n')
	if !unreadLine(m, plain[0].ID) || m.ui.dialog != nil {
		t.Error("n marked lines read")
	}
}

// With nothing to mark, N says so and asks nothing.
func TestMarkAllReadNothing(t *testing.T) {
	m := mrShell(t, mrReport(40_000, "release", bus.NeedsRead, "read it")...)
	for _, id := range m.markAllSet("") { // every line N would mark, read by hand
		m.markLineRead(id)
	}
	m.refresh()
	m = press(t, m, 'N')
	if m.ui.dialog != nil || !strings.Contains(m.ui.flash, "nothing to mark") {
		t.Fatalf("dialog %+v flash %q", m.ui.dialog, m.ui.flash)
	}
}

// m toggles the selected line; a triage item's line is refused; a line
// below the floor is never marked.
func TestMarkOneLine(t *testing.T) {
	plain := mrReport(60_000, "release", "", "tagged v1")
	held := mrReport(40_000, "release", bus.NeedsRead, "read the release notes")
	m := mrShell(t, append(plain, held...)...)
	sel := func(id string) {
		for _, fl := range m.feed.feed {
			if !fl.sep && fl.ev.ID == id {
				m.lists.activity.Select(fl.id)
			}
		}
	}
	sel(plain[0].ID)
	m = press(t, m, 'm')
	if unreadLine(m, plain[0].ID) {
		t.Fatal("m did not mark the line read")
	}
	sel(plain[0].ID)
	m = press(t, m, 'm')
	if !unreadLine(m, plain[0].ID) || slices.Contains(m.cache_().Read, plain[1].ID) {
		t.Fatal("m again did not mark it unread")
	}
	m = press(t, m, 'z') // undo the last act: back to read
	if unreadLine(m, plain[0].ID) {
		t.Error("z did not undo the unread mark")
	}
	sel(held[0].ID)
	m = press(t, m, 'm')
	if !unreadLine(m, held[0].ID) || !strings.Contains(m.ui.flash, "this line is a triage item: open it from T") {
		t.Errorf("m on a triage item's line: unread %v, flash %q", unreadLine(m, held[0].ID), m.ui.flash)
	}
	// Below the floor: never marked.
	old := mrReport(10*60_000, "release", "", "old news")
	m.feed.unread.floor = shellNow.Add(-5 * time.Minute).UnixMilli()
	m = step(t, m, tailMsg{events: old})
	sel(old[0].ID)
	m = press(t, m, 'm')
	if unreadLine(m, old[0].ID) || slices.Contains(m.cache_().Read, old[1].ID) || !strings.Contains(m.ui.flash, "before your last visit") {
		t.Errorf("m below the floor: flash %q", m.ui.flash)
	}
}

// On an agent's page, N marks that agent's lines (the filter's @agent match).
func TestMarkAgentRead(t *testing.T) {
	mine := mrReport(60_000, "release", "", "tagged v1")
	other := mrReport(50_000, "docs", "", "docs out")
	m := mrShell(t, append(mine, other...)...)
	m.pushAgent("release")
	if p := m.rd.top(); p == nil || p.kind != ovAgent {
		t.Fatal("no agent page")
	}
	m = press(t, m, 'N')
	if m.ui.dialog == nil {
		t.Fatal("N on an agent's page asked nothing")
	}
	m = press(t, m, 'y')
	if unreadLine(m, mine[0].ID) || !unreadLine(m, other[0].ID) {
		t.Errorf("agent N: release's unread %v, docs's unread %v", unreadLine(m, mine[0].ID), unreadLine(m, other[0].ID))
	}
}

// The filter: is:unread keeps the unread lines, ANDed with @agent; the word
// unread stays a word.
func TestFilterIsUnread(t *testing.T) {
	f := parseFilter("@master is:unread")
	if !f.unread || f.agent != "master" || f.text != "" {
		t.Fatalf("parsed %+v", f)
	}
	if g := parseFilter("unread"); g.unread || g.text != "unread" {
		t.Fatalf("the word: %+v", g)
	}
	line := func(agent string, mk mark) feedLine {
		return feedLine{ev: bus.Event{Kind: "report", ID: "1-0", Agent: agent, Message: "x"}, mark: mk}
	}
	for _, c := range []struct {
		fl   feedLine
		want bool
	}{{line("master", markUnread), true}, {line("master", markNone), false}, {line("coder", markUnread), false}} {
		if got := f.match(c.fl); got != c.want {
			t.Errorf("%+v: %v", c.fl.ev.Agent, got)
		}
	}
	if d := f.describe(); !strings.Contains(d, "is:unread") {
		t.Errorf("describe %q", d)
	}
}
