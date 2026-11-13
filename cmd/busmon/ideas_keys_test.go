package main

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

var kF6 = tea.Key{Code: tea.KeyF6}

// ideasShell: busmon as charlie on the fixture's ideas, a fake writer
// holding the same list.
func ideasShell(t *testing.T, self string) (Model, *fakeWriter) {
	t.Helper()
	st := workShared()
	st.ideas = ideasFixture()
	w := &fakeWriter{ideas: ideasFixture()}
	m := newShell(shellConfig{project: "demo", self: self, st: st, w: w})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	return m, w
}

// press1 is one key whose command, if any, runs and comes back as its
// message (an idea write's outcome).
func press1(t *testing.T, m Model, k any) Model {
	t.Helper()
	var msg tea.Msg
	switch k := k.(type) {
	case rune:
		msg = tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)})
	case tea.Key:
		msg = tea.KeyPressMsg(k)
	}
	next, cmd := m.Update(msg)
	m = next.(Model)
	if cmd != nil {
		if out, ok := cmd().(ideaMsg); ok {
			m = step(t, m, out)
		}
	}
	return m
}

func TestIdeasF6Opens(t *testing.T) {
	m, _ := ideasShell(t, "charlie")
	m = keys(t, m, kF6)
	p := m.rd.top()
	if p == nil || p.kind != ovIdeas || len(p.iitems) != 4 {
		t.Fatalf("F6: %+v", p)
	}
	if !strings.Contains(m.grid().plain(), "Ideas "+GlyphNone+" 2 to discuss") {
		t.Fatalf("F6 page\n%s", m.grid().plain())
	}
	// tab: one state at a time.
	m = keys(t, m, kTab)
	if p := m.rd.top(); p.ideas.filter != 1 || len(p.iitems) != 2 {
		t.Fatalf("tab: filter %d items %d", p.ideas.filter, len(p.iitems))
	}
	// enter opens the selected idea's page.
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p.kind != ovIdea || p.key != "i1" {
		t.Fatalf("enter: %+v", p)
	}
}

// s, ↓, y: the state set as the person, the state shown when the picker
// opened as the expected one; the list read again shows it at once.
func TestIdeasPickerSets(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 's')
	p := m.rd.top()
	if p.ideas.pick == nil || p.ideas.pick.id != "i1" || p.ideas.pick.from != bus.IdeaToDiscuss {
		t.Fatalf("s: %+v", p.ideas.pick)
	}
	m = keys(t, m, kDown)
	m = press1(t, m, 'y')
	if !slices.Contains(w.sent, "idea set i1 to discuss -> decided charlie") {
		t.Fatalf("sent %v", w.sent)
	}
	if p := m.rd.top(); p.ideas.pick != nil || p.kind != ovIdeas {
		t.Fatalf("after y: %+v", p)
	}
	if m.snap.ideas[0].State != bus.IdeaDecided || !strings.Contains(m.ui.flash, "to discuss → decided") {
		t.Fatalf("shown %q, flash %q", m.snap.ideas[0].State, m.ui.flash)
	}
	if m.cfg.st.ideas[0].State != bus.IdeaDecided {
		t.Fatal("the shared state still holds the old list: the next snapshot brings it back")
	}
}

// esc closes the picker, not the page; y on the current state writes nothing.
func TestIdeasPickerCancels(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 's', kEsc)
	if p := m.rd.top(); p == nil || p.kind != ovIdeas || p.ideas.pick != nil {
		t.Fatalf("esc: %+v", p)
	}
	m = keys(t, m, 's')
	m = press1(t, m, 'y')
	if len(w.sent) != 0 || !strings.Contains(m.ui.flash, "already") {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
}

// No person named: nothing is written, busmon says how to name yourself.
func TestIdeasNeedAPerson(t *testing.T) {
	m, w := ideasShell(t, "")
	m = keys(t, m, kF6, 's', kDown)
	m = press1(t, m, 'y')
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "identity set") {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
}

// The bus's refusal (a change made elsewhere) is said, never hidden.
func TestIdeasRefusalSaid(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	w.fail = errors.New("idea refused: the idea is now \"doing\"")
	m = keys(t, m, kF6, 's', kDown)
	m = press1(t, m, 'y')
	if !m.ui.flashErr || !strings.Contains(m.ui.flash, "doing") {
		t.Fatalf("flash %q", m.ui.flash)
	}
}

// a: the title on the line, Enter adds it as the person.
func TestIdeasAdd(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 'a')
	if m.ui.line != lineIdeaTitle {
		t.Fatalf("a: line %v", m.ui.line)
	}
	for _, r := range "voice notes" {
		m = keys(t, m, r)
	}
	m = press1(t, m, kEnter)
	if !slices.Contains(w.sent, "idea add charlie voice notes") || m.ui.line != lineNone {
		t.Fatalf("sent %v line %v", w.sent, m.ui.line)
	}
	if n := len(m.snap.ideas); n != 5 || m.snap.ideas[4].Title != "voice notes" {
		t.Fatalf("ideas %d", n)
	}
}

// s on an idea's page too.
func TestIdeaPageSets(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, kDown, kEnter, 's', kDown)
	m = press1(t, m, 'y')
	if !slices.Contains(w.sent, "idea set i4 to discuss -> decided charlie") {
		t.Fatalf("sent %v", w.sent)
	}
}

// Tab is F6's alone: on another page it does nothing, and a and s keep
// their own pages' meanings.
func TestIdeasKeysStayOnF6(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, tea.Key{Code: tea.KeyF5})
	before := m.rd.top().health
	m = keys(t, m, kTab, 's')
	if p := m.rd.top(); p.kind != ovHealth || p.ideas.pick != nil || p.ideas.filter != 0 || m.ui.line != lineNone {
		t.Fatalf("tab/s on Health: %+v", p)
	}
	m = keys(t, m, 'a')
	if p := m.rd.top(); p.health.all == before.all || m.ui.line == lineIdeaTitle {
		t.Fatal("a on Health is not its own 'all'")
	}
	if len(w.sent) != 0 {
		t.Fatalf("sent %v", w.sent)
	}
}
