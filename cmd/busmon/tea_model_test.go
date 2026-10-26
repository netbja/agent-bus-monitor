package main

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
)

// panicReader panics on any read: a model built on it proves that View and
// Update never touch the bus (only commands do).
type panicReader struct{ busReader }

// View reads only the model: twice on the same model, the same frame, and a
// model whose reader panics renders.
func TestShellViewIsPure(t *testing.T) {
	pinLocal(t)
	for _, size := range goldenSizes {
		m := fixtureShell(t, size[0], size[1])
		m.cfg.reader = panicReader{}
		m.cfg.st = nil
		a, b := m.View().Content, m.View().Content
		if a != b {
			t.Fatalf("%v: two Views of one model differ", size)
		}
		// Update on every message the dashboard takes, then View: no read.
		m = step(t, m, tea.WindowSizeMsg{Width: size[0] - 1, Height: size[1]})
		m = step(t, m, tailMsg{events: []bus.Event{{ID: "1793000000000-0", Kind: "notify", From: "x", Message: "y"}}})
		m = step(t, m, pollMsg{snap: shellFixture(shellNow), at: shellNow.Add(time.Second)})
		_ = m.View()
	}
}

// The poll is a command: Init and every tick return it, it runs pollOnce on
// the reader with the tick's number (the dispatcher facts every 5th), and its
// pollMsg schedules the next tick.
func TestShellPollCycle(t *testing.T) {
	f := &fakeReader{calls: map[string]int{}}
	st := &shared{agents: map[string]*agentState{}, person: "bernard"}
	m := newShell(shellConfig{ctx: context.Background(), reader: f, st: st, project: "demo", self: "bernard"})
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if f.calls["PilotDriver"] != 0 {
		t.Fatal("a size read the bus")
	}
	msg := m.Init()()
	pm, ok := msg.(pollMsg)
	if !ok {
		t.Fatalf("Init's command returned %T", msg)
	}
	if f.calls["PilotDriver"] != 1 || f.calls["DispatchAlive"] != 1 {
		t.Fatalf("first poll reads: %v", f.calls)
	}
	if len(pm.snap.agents) != 2 || pm.snap.pilot != "master" {
		t.Fatalf("snapshot: %+v", pm.snap)
	}
	next, cmd := m.Update(pm)
	m = next.(Model)
	if cmd == nil || len(m.snap.agents) != 2 || m.gen != 1 {
		t.Fatalf("pollMsg: cmd %v, gen %d", cmd, m.gen)
	}
	// Ticks 2-5 read no dispatcher facts; tick 6 does.
	for i := 2; i <= 6; i++ {
		next, cmd = m.Update(tickMsg(shellNow))
		m = next.(Model)
		if _, ok := cmd().(pollMsg); !ok {
			t.Fatalf("tick %d: no poll", i)
		}
	}
	if f.calls["PilotDriver"] != 6 || f.calls["DispatchAlive"] != 2 {
		t.Fatalf("six polls read: %v", f.calls)
	}
}

// A tailed status reaches both the Activity feed and the agent's state.
func TestShellTailFoldsEvents(t *testing.T) {
	st := &shared{agents: map[string]*agentState{}, person: "bernard"}
	m := newShell(shellConfig{st: st, project: "demo", self: "bernard"})
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 30})
	gen := m.gen
	m = step(t, m, tailMsg{events: []bus.Event{{ID: "1793000000000-0", Kind: "status", Agent: "coder", State: "working", Message: "x"}}})
	if m.gen != gen+1 {
		t.Errorf("generation %d, want %d", m.gen, gen+1)
	}
	if a := m.snap.agents["coder"]; a == nil || a.state != "working" {
		t.Fatalf("agent state: %+v", a)
	}
	if n := len(m.feed.feed); n != 2 { // the day separator and the status
		t.Fatalf("feed: %d lines", n)
	}
}

// At the medium tier For you shows four items, then "+N more"; focused, it
// lists them all and scrolls (Bernard's decision 1).
func TestShellForYouMediumShowsFourThenMore(t *testing.T) {
	pinLocal(t)
	m := fixtureShell(t, 80, 24)
	if n := len(m.forYou); n != 6 {
		t.Fatalf("fixture has %d items, want 6", n)
	}
	m.ui.focus = PanelAgents
	m.refresh()
	items := m.lists.forYou.items
	if len(items) != 5 || items[4].Line.text() != "+2 more" {
		t.Fatalf("unfocused rows: %d, last %q", len(items), items[len(items)-1].Line.text())
	}
	m.ui.focus = PanelForYou
	m.refresh()
	if n := len(m.lists.forYou.items); n != 6 {
		t.Fatalf("focused rows: %d, want 6", n)
	}
}
