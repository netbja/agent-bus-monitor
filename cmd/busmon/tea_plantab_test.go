package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// A11: the lower panel's fourth tab is the plan's task list (what P shows;
// P stays). 4 picks it at the wide and medium tiers, 6 at the narrow one;
// Enter on a task opens its page, on the done group expands it.
func TestShellPlanTab(t *testing.T) {
	pinLocal(t)
	a := plan.Task{ID: "a-board", Title: "Board columns", Owner: "coder", Board: "a-board"}
	b := plan.Task{ID: "a-look", Title: "busmon look", Owner: "coder", Board: "a-look"}
	st := &shared{agents: map[string]*agentState{}, person: "bernard"}
	st.plan = planState{loaded: true, file: plan.File{Plan: "v0.23", Goal: "group A", Tasks: []plan.Task{a, b}},
		rows: []plan.Row{{Task: a, State: plan.InProgress}, {Task: b, State: plan.Done}},
		in:   plan.Inputs{Board: map[string]bus.BoardEntry{}}}
	m := newShell(shellConfig{project: "demo", self: "bernard", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})

	tabs := ""
	for _, s := range tabBar(lowerTabs, PanelActivity, 200) {
		tabs += s.Text
	}
	if !strings.Contains(tabs, " 4 Plan ") {
		t.Fatalf("tab bar %q has no Plan", tabs)
	}
	m = keys(t, m, '4')
	if m.ui.focus != PanelPlan || m.layout.Active != PanelPlan {
		t.Fatalf("4 focused %v (active %v)", m.ui.focus, m.layout.Active)
	}
	l := m.lists.of(PanelPlan)
	var sel []string
	for _, it := range l.items {
		if it.Selectable {
			sel = append(sel, it.Key)
		}
	}
	if strings.Join(sel, " ") != "task:a-board toggle" {
		t.Fatalf("selectable plan rows %v", sel)
	}
	l.Select("task:a-board")
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p == nil || p.kind != ovPlanTask || p.key != "a-board" {
		t.Fatalf("Enter on a task: %+v", p)
	}
	m = keys(t, m, kEsc)
	m.lists.of(PanelPlan).Select("toggle")
	m = keys(t, m, kEnter)
	sel = nil
	for _, it := range m.lists.of(PanelPlan).items {
		if it.Selectable {
			sel = append(sel, it.Key)
		}
	}
	if strings.Join(sel, " ") != "task:a-board task:a-look" {
		t.Fatalf("after expanding done: %v", sel)
	}

	// The narrow tier's sixth view.
	m = step(t, m, tea.WindowSizeMsg{Width: 60, Height: 24})
	m = keys(t, m, '6')
	if m.layout.Active != PanelPlan {
		t.Fatalf("6 at narrow: active %v", m.layout.Active)
	}
}

// No plan loaded: the tab says how to load one, and nothing is selectable.
func TestPlanTabWithoutPlan(t *testing.T) {
	its := planTabItems(planState{}, false, shellNow, 100)
	if len(its) != 1 || its[0].Selectable || !strings.Contains(its[0].Line.text(), "agentbus plan load") {
		t.Fatalf("no plan: %+v", its)
	}
}

// A sixth narrow view must not shorten every label: the longest names
// shorten first, only until the bar fits.
func TestNarrowTabBarShortensOnlyWhatItMust(t *testing.T) {
	bar := ""
	l := tabBar(narrowPanels, PanelForYou, 60)
	for _, s := range l {
		bar += s.Text
	}
	if l.Width() > 60 {
		t.Fatalf("bar %q is %d wide", bar, l.Width())
	}
	for _, full := range []string{"1 For you", "2 Agents", "4 Board", "6 Plan"} {
		if !strings.Contains(bar, full) {
			t.Errorf("bar %q shortened %q", bar, full)
		}
	}
	// Wide enough: every name in full.
	bar = ""
	for _, s := range tabBar(narrowPanels, PanelForYou, 200) {
		bar += s.Text
	}
	if !strings.Contains(bar, "3 Activity") || !strings.Contains(bar, "5 Requests") {
		t.Errorf("wide bar %q", bar)
	}
}

// Review (reviewer-codex): y copies the selected task's page text and End/G
// selects the last task, as on the Board; the done toggle copies nothing.
// Review (foureyes): the overlay's two-cell mark column is not drawn.
func TestPlanTabCopyAndEnd(t *testing.T) {
	pinLocal(t)
	a := plan.Task{ID: "a-board", Title: "Board columns", Owner: "coder"}
	b := plan.Task{ID: "a-nav", Title: "reader navigation", Owner: "coder"}
	st := &shared{agents: map[string]*agentState{}, person: "bernard"}
	st.plan = planState{loaded: true, file: plan.File{Plan: "v0.23", Goal: "group A", Tasks: []plan.Task{a, b}},
		rows: []plan.Row{{Task: a, State: plan.InProgress}, {Task: b, State: plan.InProgress}},
		in:   plan.Inputs{Board: map[string]bus.BoardEntry{}}}
	m := newShell(shellConfig{project: "demo", self: "bernard", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m = step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
	m = keys(t, m, '4')
	m = keys(t, m, kEnd)
	if got := m.lists.of(PanelPlan).Selected(); got != "task:a-nav" {
		t.Fatalf("End selected %q, want the last task", got)
	}
	m = keys(t, m, 'y')
	if !strings.Contains(m.ui.flash, "task copied") {
		t.Fatalf("y on a task: flash %q", m.ui.flash)
	}
	for _, it := range planTabItems(st.plan, false, shellNow, 120) {
		if it.Selectable && strings.HasPrefix(it.Line.text(), "  ") {
			t.Fatalf("plan row keeps the overlay's mark column: %q", it.Line.text())
		}
	}
}
