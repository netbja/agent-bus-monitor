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
	m := newShell(shellConfig{project: "demo", self: self, st: st, w: w,
		getenv: func(string) string { return "" }}) // never the test runner's own (an agent's AGENT_BUS_AGENT)
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

// a: the compose window, its first line the title, the rest the text;
// Ctrl+S adds it as the person (reviewer-codex: the design's compose window,
// so an idea added from F6 keeps its context).
func TestIdeasAdd(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 'a')
	if c := m.ui.compose; c == nil || c.kind != composeIdea {
		t.Fatalf("a: compose %+v", m.ui.compose)
	}
	if !strings.Contains(m.grid().plain(), "New idea") {
		t.Fatalf("the window\n%s", m.grid().plain())
	}
	for _, r := range "voice notes" {
		m = keys(t, m, r)
	}
	m = keys(t, m, kEnter)
	for _, r := range "read reports aloud" {
		m = keys(t, m, r)
	}
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if !slices.Contains(w.sent, "idea add charlie voice notes") || m.ui.compose != nil {
		t.Fatalf("sent %v compose %+v", w.sent, m.ui.compose)
	}
	got := m.snap.ideas[len(m.snap.ideas)-1]
	if got.Title != "voice notes" || got.Text != "read reports aloud" {
		t.Fatalf("added %+v", got)
	}
}

// Esc keeps the idea's draft for the next a; a title too long is said, not sent.
func TestIdeasAddDraftAndLimits(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 'a')
	for _, r := range "half an idea" {
		m = keys(t, m, r)
	}
	m = keys(t, m, kEsc)
	if m.ui.compose != nil {
		t.Fatal("esc kept the window")
	}
	m = keys(t, m, 'a')
	if c := m.ui.compose; c == nil || c.area.value() != "half an idea" {
		t.Fatalf("the draft is lost: %+v", m.ui.compose)
	}
	m.ui.compose.area.set(strings.Repeat("t", bus.IdeaMaxTitle+1) + "\ntext")
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "title") || m.ui.compose == nil {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
	m.ui.compose.area.set("\n\n")
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if len(w.sent) != 0 || m.ui.compose == nil {
		t.Fatalf("an empty idea sent: %v", w.sent)
	}
}

// busmon run from an agent's environment (AGENT_BUS_AGENT) inherits the
// person's name: it never sets a state (reviewer-codex). A herdr pane alone
// is not refused here: the person's own busmon runs in one (the plugin).
func TestIdeasSetRefusedInAnAgentsEnv(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m.cfg.getenv = func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	m = keys(t, m, kF6, 's', kDown)
	m = press1(t, m, 'y')
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "agent") {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
	m.cfg.getenv = func(k string) string {
		if k == "HERDR_PANE_ID" {
			return "w1:p3"
		}
		return ""
	}
	m = keys(t, m, 's', kDown)
	m = press1(t, m, 'y')
	if len(w.sent) != 1 {
		t.Fatalf("the person's busmon in a herdr pane: sent %v flash %q", w.sent, m.ui.flash)
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
	if p := m.rd.top(); p.kind != ovHealth || p.ideas.pick != nil || p.ideas.filter != 0 || m.ui.compose != nil {
		t.Fatalf("tab/s on Health: %+v", p)
	}
	m = keys(t, m, 'a')
	if p := m.rd.top(); p.health.all == before.all || m.ui.compose != nil {
		t.Fatal("a on Health is not its own 'all'")
	}
	if len(w.sent) != 0 {
		t.Fatalf("sent %v", w.sent)
	}
}

// reviewer-codex: a failed add keeps the text (a reopens it), and only a
// written idea spends the draft.
func TestIdeasAddFailureKeepsTheDraft(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	w.fail = errors.New("broker down")
	m = keys(t, m, kF6, 'a')
	for _, r := range "keep me" {
		m = keys(t, m, r)
	}
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if !m.ui.flashErr || !strings.Contains(m.ui.flash, "broker down") || !strings.Contains(m.ui.flash, "kept") {
		t.Fatalf("flash %q", m.ui.flash)
	}
	m = keys(t, m, 'a')
	if c := m.ui.compose; c == nil || c.area.value() != "keep me" {
		t.Fatalf("the draft after a failed add: %+v", m.ui.compose)
	}
	w.fail = nil
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if m.ui.ideaDraft != "" {
		t.Fatalf("a written idea left its draft %q", m.ui.ideaDraft)
	}
}

// reviewer-codex: busmon in an agent's environment would credit the person
// with the agent's idea: the add is refused there, as the set is (an agent
// adds with agentbus idea add, under its own name).
func TestIdeasAddRefusedInAnAgentsEnv(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m.cfg.getenv = func(k string) string {
		if k == "AGENT_BUS_AGENT" {
			return "coder"
		}
		return ""
	}
	m = keys(t, m, kF6, 'a')
	for _, r := range "an agent's idea" {
		m = keys(t, m, r)
	}
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if len(w.sent) != 0 || !m.ui.flashErr || !strings.Contains(m.ui.flash, "idea add") {
		t.Fatalf("sent %v flash %q", w.sent, m.ui.flash)
	}
}

// reviewer-codex: while an add is in flight a second Ctrl+S (a reopens the
// kept text) adds nothing; its result spends the draft only if the draft is
// still what was sent (an edit made meanwhile is kept).
func TestIdeasAddOneInFlight(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	m = keys(t, m, kF6, 'a')
	for _, r := range "once" {
		m = keys(t, m, r)
	}
	next, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("no write")
	}
	m = keys(t, m, 'a') // the kept text, reopened while the add is on its way
	next, cmd2 := m.Update(tea.KeyPressMsg(tea.Key{Code: 's', Mod: tea.ModCtrl}))
	m = next.(Model)
	if cmd2 != nil || !strings.Contains(m.ui.flash, "on its way") {
		t.Fatalf("a second add in flight: cmd %v flash %q", cmd2 != nil, m.ui.flash)
	}
	// An edit made meanwhile, then the window closed: kept past the result.
	m.ui.compose.area.set("once, edited")
	m = keys(t, m, kEsc)
	m = step(t, m, cmd().(ideaMsg))
	if n := strings.Count(strings.Join(w.sent, "\n"), "idea add"); n != 1 {
		t.Fatalf("%d adds sent: %v", n, w.sent)
	}
	if m.ui.ideaDraft != "once, edited" {
		t.Fatalf("the edited draft after the first add's result: %q", m.ui.ideaDraft)
	}
	// The result came: a new add may go.
	m = keys(t, m, 'a')
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if n := strings.Count(strings.Join(w.sent, "\n"), "idea add"); n != 2 {
		t.Fatalf("after the result, a new add: %v", w.sent)
	}
}

// reviewer-codex: an add committed whose reply was lost is retried with the
// same request id, so the bus finds the idea it made (no second one); an
// edited text is another request.
func TestIdeasAddRetrySameRequest(t *testing.T) {
	m, w := ideasShell(t, "charlie")
	w.failAfterCommit = true
	m = keys(t, m, kF6, 'a')
	for _, r := range "lost reply" {
		m = keys(t, m, r)
	}
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if !m.ui.flashErr || m.ui.ideaDraft != "lost reply" {
		t.Fatalf("flash %q draft %q", m.ui.flash, m.ui.ideaDraft)
	}
	m = keys(t, m, 'a')
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if len(w.ideaReqs) != 2 || w.ideaReqs[0] == "" || w.ideaReqs[1] != w.ideaReqs[0] {
		t.Fatalf("request ids %q: the retry must reuse the first", w.ideaReqs)
	}
	if n := len(w.ideas); n != len(ideasFixture())+1 {
		t.Fatalf("%d ideas: the retry made a second one", n)
	}
	// A new text after that: a new request.
	m = keys(t, m, 'a')
	for _, r := range "another" {
		m = keys(t, m, r)
	}
	m = press1(t, m, tea.Key{Code: 's', Mod: tea.ModCtrl})
	if len(w.ideaReqs) != 3 || w.ideaReqs[2] == w.ideaReqs[0] {
		t.Fatalf("request ids %q: a new text is a new request", w.ideaReqs)
	}
}
