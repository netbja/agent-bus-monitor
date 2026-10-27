package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// press sends one key press: a rune, or a named key.
func keys(t testing.TB, m Model, keys ...any) Model {
	t.Helper()
	for _, k := range keys {
		switch k := k.(type) {
		case rune:
			m = step(t, m, tea.KeyPressMsg(tea.Key{Code: k, Text: string(k)}))
		case tea.Key:
			m = step(t, m, tea.KeyPressMsg(k))
		default:
			t.Fatalf("press %T", k)
		}
	}
	return m
}

var (
	kTab      = tea.Key{Code: tea.KeyTab}
	kBacktab  = tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}
	kDown     = tea.Key{Code: tea.KeyDown}
	kUp       = tea.Key{Code: tea.KeyUp}
	kEnter    = tea.Key{Code: tea.KeyEnter}
	kEsc      = tea.Key{Code: tea.KeyEscape}
	kEnd      = tea.Key{Code: tea.KeyEnd}
	kBackspce = tea.Key{Code: tea.KeyBackspace}
)

func clickAt(t testing.TB, m Model, x, y int) Model {
	t.Helper()
	return step(t, m, tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func wheelAt(t testing.TB, m Model, x, y int, up bool) Model {
	t.Helper()
	b := tea.MouseWheelDown
	if up {
		b = tea.MouseWheelUp
	}
	return step(t, m, tea.MouseWheelMsg{X: x, Y: y, Button: b})
}

// inside is a cell inside a panel's frame, on its first data row.
func inPanel(m Model, p PanelID) (x, y int) {
	r := m.layout.rect(p)
	return r.X + r.W/2, r.Y + r.H/2
}

func TestShellFocusCycle(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	var got []PanelID
	for i := 0; i < 4; i++ {
		got = append(got, m.ui.focus)
		m = keys(t, m, kTab)
	}
	if want := []PanelID{PanelForYou, PanelAgents, PanelActivity, PanelForYou}; !slices.Equal(got, want) {
		t.Fatalf("wide tab order %v, want %v", got, want)
	}
	if m = keys(t, m, kBacktab); m.ui.focus != PanelForYou {
		t.Fatalf("shift-tab: focus %d", m.ui.focus)
	}
	// Narrow: Tab walks the five views, the view follows the focus.
	n := fixtureShell(t, 60, 20)
	got = nil
	for i := 0; i < 6; i++ {
		got = append(got, n.layout.Active)
		n = keys(t, n, kTab)
	}
	if want := append(append([]PanelID{}, narrowPanels...), PanelForYou); !slices.Equal(got, want) {
		t.Fatalf("narrow views %v, want %v", got, want)
	}
}

func TestShellTabs(t *testing.T) {
	pinLocal(t)
	for _, size := range [][2]int{{140, 40}, {80, 24}} {
		m := fixtureShell(t, size[0], size[1])
		for i, want := range lowerTabs {
			m = keys(t, m, rune('1'+i))
			if m.ui.focus != want || m.layout.Active != want || m.layout.Lower.H == 0 {
				t.Fatalf("%v: key %d: focus %d, active %d", size, i+1, m.ui.focus, m.layout.Active)
			}
		}
		// The focus can leave the tabbed panel; its tab stays.
		m = keys(t, m, kTab)
		if m.ui.focus != PanelForYou || m.layout.Active != PanelRequests {
			t.Fatalf("%v: after tab: focus %d, active %d", size, m.ui.focus, m.layout.Active)
		}
	}
	n := fixtureShell(t, 60, 20)
	for i, want := range narrowPanels {
		if n = keys(t, n, rune('1'+i)); n.layout.Active != want {
			t.Fatalf("narrow key %d: view %d, want %d", i+1, n.layout.Active, want)
		}
	}
}

// A selected row stays selected by its key when a poll reorders the rows.
func TestShellKeyedSelection(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 60)
	m = keys(t, m, kTab, kDown, kDown) // Agents, the third row
	sel := m.lists.agents.Selected()
	if sel == "" || sel == m.lists.agents.items[0].Key {
		t.Fatalf("selection %q", sel)
	}
	snap := shellFixture(shellNow)
	for _, a := range snap.agents {
		a.judgedAt = shellNow
	}
	snap.agents[sel].state, snap.agents[sel].openAsk = "waiting", 1 // it moves to the top group
	m = step(t, m, pollMsg{snap: snap, at: shellNow.Add(time.Second)})
	if got := m.lists.agents.Selected(); got != sel {
		t.Fatalf("after the poll the selection is %q, want %q", got, sel)
	}
	if i := m.lists.agents.index(sel); i < 0 || m.lists.agents.items[i].Key != sel {
		t.Fatalf("selected key not in the rows")
	}
}

// Until the person moves in Agents, its selection follows the top row; a
// key, a wheel or a click there stops that.
func TestShellAgentsPinning(t *testing.T) {
	pinLocal(t)
	for name, touch := range map[string]func(Model) Model{
		"key":   func(m Model) Model { return keys(t, m, kTab, kDown) },
		"wheel": func(m Model) Model { x, y := inPanel(m, PanelAgents); return wheelAt(t, m, x, y, false) },
		"click": func(m Model) Model { x, y := inPanel(m, PanelAgents); return clickAt(t, m, x, y) },
	} {
		m := fixtureShell(t, 140, 60)
		top := m.lists.agents.Selected()
		if top != m.lists.agents.items[0].Key {
			t.Fatalf("%s: untouched selection %q is not the top row", name, top)
		}
		m = touch(m)
		if !m.ui.agentsTouched {
			t.Fatalf("%s: agents not marked touched", name)
		}
		m.lists.agents.Select("docs")
		m = step(t, m, pollMsg{snap: m.snap, at: shellNow})
		if got := m.lists.agents.Selected(); got != "docs" {
			t.Errorf("%s: the poll moved the selection to %q", name, got)
		}
	}
}

// A Tab or a tab digit that only passes through Agents leaves the pinning:
// only a movement, a wheel or a click there is the person's choice.
func TestShellAgentsPinningIgnoresPassingKeys(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 60)
	m = keys(t, m, kTab, kTab, kBacktab, '2', kBacktab, 'a', 'a')
	if m.ui.focus != PanelAgents {
		t.Fatalf("focus %d, want Agents", m.ui.focus)
	}
	if m.ui.agentsTouched {
		t.Fatal("passing through Agents marked it touched")
	}
}

// Activity follows the bottom; selecting keeps it live; f pauses it and new
// lines are counted, not shown; a wheel-up leaves it; End resumes.
func TestShellActivityTail(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	more := func(m Model, n int, from time.Duration) Model {
		var evs []bus.Event
		for i := 0; i < n; i++ {
			evs = append(evs, bus.Event{ID: fmt.Sprintf("%d-0", shellNow.Add(from+time.Duration(i)*time.Second).UnixMilli()),
				Kind: "notify", From: "x", Message: fmt.Sprintf("line %d", i)})
		}
		return step(t, m, tailMsg{events: evs})
	}
	m = more(m, 40, time.Second)
	act := &m.lists.activity
	if !act.Live() || act.top != len(act.items)-act.rows() {
		t.Fatalf("not following the bottom: top %d of %d (%d rows)", act.top, len(act.items), act.rows())
	}
	m = keys(t, m, '3', '1', kUp) // Activity, a line selected
	if !m.lists.activity.Live() {
		t.Fatal("selecting a line left live")
	}
	m = keys(t, m, 'f')
	top := m.lists.activity.top
	m = more(m, 5, time.Minute)
	if m.lists.activity.Live() || m.lists.activity.top != top || m.ui.pause.New() != 5 {
		t.Fatalf("paused: live %v, top %d (was %d), new %d", m.lists.activity.Live(), m.lists.activity.top, top, m.ui.pause.New())
	}
	if !strings.Contains(m.grid().plain(), "5 new") {
		t.Error("the frame does not count the new lines")
	}
	m = keys(t, m, kEnd)
	if !m.lists.activity.Live() || m.ui.pause.Paused() {
		t.Fatal("end did not resume")
	}
	x, y := inPanel(m, PanelActivity)
	if m = wheelAt(t, m, x, y, true); m.lists.activity.Live() {
		t.Fatal("a wheel-up kept live")
	}
	m = keys(t, m, 'f')
	if !m.lists.activity.Live() {
		t.Fatal("f did not resume")
	}
	// A click on a line keeps it live.
	if m = clickAt(t, m, x, y); !m.lists.activity.Live() || m.lists.activity.Selected() == "" {
		t.Fatal("a click left live or selected nothing")
	}
}

// a shows and hides the dormant agents; @all never reaches a dormant one,
// the completions follow a.
func TestShellShowAllDormant(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 60)
	listed := func(m Model) bool { return m.lists.agents.index("security") >= 0 }
	if listed(m) {
		t.Fatal("a dormant agent is listed")
	}
	m = keys(t, m, kTab, 'a')
	if !listed(m) || !m.ui.showAll {
		t.Fatal("a did not show the dormant agent")
	}
	if slices.Contains(agentNamesOf(m.snap.agents, m.snap.pilot, m.cfg.self, false, m.now), "security") {
		t.Error("@all reaches a dormant agent")
	}
	if !slices.Contains(agentNamesOf(m.snap.agents, m.snap.pilot, m.cfg.self, m.ui.showAll, m.now), "security") {
		t.Error("the completions do not follow a")
	}
	if m = keys(t, m, 'a'); listed(m) {
		t.Fatal("a did not hide it again")
	}
}

// / edits the filter in the input line, Enter applies it to Activity, Esc
// clears it after the selection and the pause.
func TestShellFilter(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	m = keys(t, m, '/')
	if m.mode() != ModeFilter || m.ui.focus != PanelActivity || m.layout.Input.H == 0 {
		t.Fatalf("/ : mode %d focus %d", m.mode(), m.ui.focus)
	}
	m = keys(t, m, '@', 'c', 'o', 'd', 'e', 'r', 'x', kBackspce)
	if !strings.Contains(m.grid().rowText(m.layout.Input.Y), "filter"+GlyphPrompt+" @coder") {
		t.Fatalf("input row: %q", m.grid().rowText(m.layout.Input.Y))
	}
	m = keys(t, m, kEnter)
	if m.mode() != ModeShell || m.ui.filter.raw != "@coder" || len(m.lists.activity.items) == 0 {
		t.Fatalf("after enter: mode %d filter %q", m.mode(), m.ui.filter.raw)
	}
	for _, it := range m.lists.activity.items {
		if !strings.Contains(strings.ToLower(it.Line.text()), "coder") {
			t.Errorf("filtered row without coder: %q", it.Line.text())
		}
	}
	m = keys(t, m, kUp, 'f', kEsc) // a selection, a pause: Esc clears the selection first
	if m.lists.activity.Selected() != "" || m.ui.filter.empty() {
		t.Fatal("esc did not clear the selection first")
	}
	m = keys(t, m, kEsc) // then the pause
	if !m.lists.activity.Live() || m.ui.filter.empty() {
		t.Fatal("esc did not resume second")
	}
	if m = keys(t, m, kEsc); !m.ui.filter.empty() {
		t.Fatal("esc did not clear the filter third")
	}
	// Esc while editing cancels the edit and keeps the applied filter.
	m = keys(t, m, '/', 'x', kEsc)
	if m.mode() != ModeShell || !m.ui.filter.empty() {
		t.Fatal("esc in the filter line applied it")
	}
}

// u selects the next unread line, wrapping.
func TestShellNextUnread(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	var unread []string
	for _, fl := range m.feed.feed {
		if fl.mark == markUnread {
			unread = append(unread, fl.id)
		}
	}
	if len(unread) != 2 {
		t.Fatalf("fixture has %d unread lines", len(unread))
	}
	for _, want := range []string{unread[0], unread[1], unread[0]} {
		if m = keys(t, m, 'u'); m.ui.focus != PanelActivity || m.lists.activity.Selected() != want {
			t.Fatalf("u selected %q, want %q", m.lists.activity.Selected(), want)
		}
	}
}

// The wheel scrolls the list under the pointer three rows, in every zone,
// without moving the focus.
func TestShellWheelScrollsEveryZone(t *testing.T) {
	pinLocal(t)
	for _, tab := range lowerTabs {
		for _, p := range []PanelID{PanelForYou, PanelAgents, tab} {
			m := hostileShell(t, 120, 30, false)
			m = keys(t, m, rune('1'+slices.Index(lowerTabs, tab)))
			m.ui.focus = PanelAgents
			for n, a := range m.snap.agents { // gates and threads enough to scroll For you and Requests
				a.gates = map[string]string{"g1-" + n: "x|y", "g2-" + n: "x|y"}
			}
			for i := 0; i < 30; i++ {
				m.snap.threads = append(m.snap.threads, openThread{ID: fmt.Sprintf("%d-0", i+2), Type: "directive", From: "a00", Target: "a02", Message: "t", At: shellNow})
			}
			m.refresh()
			l := m.lists.of(p)
			if l.top = 0; len(l.items) <= l.rows()+3 {
				t.Fatalf("panel %d has too few rows to scroll: %d", p, len(l.items))
			}
			l.SetTail(false)
			x, y := inPanel(m, p)
			m = wheelAt(t, m, x, y, false)
			if got := m.lists.of(p).top; got != 3 {
				t.Errorf("tab %d, panel %d: wheel-down top %d, want 3", tab, p, got)
			}
			if m = wheelAt(t, m, x, y, true); m.lists.of(p).top != 0 {
				t.Errorf("tab %d, panel %d: wheel-up top %d, want 0", tab, p, m.lists.of(p).top)
			}
			if m.ui.focus != PanelAgents {
				t.Errorf("the wheel moved the focus to %d", m.ui.focus)
			}
		}
	}
}

// A click selects a row, focusing its panel; a click on the selected row
// opens it; a click on the scrollbar column pages towards it.
func TestShellClickSelectsOpensAndPages(t *testing.T) {
	pinLocal(t)
	m := hostileShell(t, 120, 30, false)
	m = keys(t, m, '2') // Board: many rows
	m.ui.focus = PanelForYou
	m.refresh()
	r := m.layout.Lower
	_, iy, _, _ := m.lists.board.inner()
	m = clickAt(t, m, r.X+5, iy+1)
	if m.ui.focus != PanelBoard || m.lists.board.Selected() != m.lists.board.items[1].Key {
		t.Fatalf("click: focus %d, selected %q", m.ui.focus, m.lists.board.Selected())
	}
	opened := ""
	m.lists.board.SetOnOpen(func(k string) { opened = k })
	if m = clickAt(t, m, r.X+5, iy+1); opened != m.lists.board.Selected() {
		t.Fatalf("a click on the selected row opened %q", opened)
	}
	_, _, _, ih := m.lists.board.inner()
	if m = clickAt(t, m, r.X+r.W-1, iy+ih-2); m.lists.board.top == 0 {
		t.Fatal("a click low on the scrollbar did not page down")
	}
}

// A click on a tab label focuses that tab; on a frame's title row, its panel;
// on the narrow tab bar, that view.
func TestShellClickTabsAndTitles(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 140, 40)
	r := m.layout.Lower
	x := r.X + 3
	for i, s := range tabBar(lowerTabs, m.layout.Active, r.W-4) {
		want := lowerTabs[i]
		if m = clickAt(t, m, x+1, r.Y); m.ui.focus != want || m.layout.Active != want {
			t.Fatalf("click on %q: focus %d", s.Text, m.ui.focus)
		}
		x += Width(s.Text)
	}
	for _, p := range []PanelID{PanelAgents, PanelForYou} {
		r := m.layout.rect(p)
		if m = clickAt(t, m, r.X+r.W-6, r.Y); m.ui.focus != p {
			t.Fatalf("click on %d's title row: focus %d", p, m.ui.focus)
		}
	}
	n := fixtureShell(t, 60, 20)
	x = 0
	for i, s := range tabBar(narrowPanels, n.layout.Active, 60) {
		if n = clickAt(t, n, x+1, n.layout.Tabs.Y); n.layout.Active != narrowPanels[i] {
			t.Fatalf("narrow click on %q: view %d", s.Text, n.layout.Active)
		}
		x += Width(s.Text)
	}
}
