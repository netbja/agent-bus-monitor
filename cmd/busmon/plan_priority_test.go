package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/plan"
)

// views-redesign §4, step 4b: + and - on a plan task (the Plan tab, the
// drawer, its page) write the person's level through the writer, one step
// from the level in force; a done task or a fold takes none.
func TestPlanPriorityKeys(t *testing.T) {
	pinLocal(t)
	lo := boardRow("lo", plan.Upcoming, "coder")
	lo.Task.Priority = "low"
	p := boardPlan(boardRow("n", plan.Upcoming, "coder"), lo, boardRow("d", plan.Done, "coder"))
	press := func(m Model, ks ...any) Model { // keys, the commands run (the writer's call is one)
		for _, k := range ks {
			next, cmd := m.Update(keyMsg(k))
			m = run(t, next.(Model), cmd)
		}
		return m
	}
	fresh := func(k ...any) (Model, *fakeWriter) {
		m := planShellOf(t, p, 160, 40)
		fw := &fakeWriter{}
		m.cfg.w = fw
		return press(m, k...), fw
	}
	// The tab: the first task (n, normal) up to high.
	m, fw := fresh('4')
	if m.lists.plan.Selected() != "plan:n" {
		t.Fatalf("selected %q", m.lists.plan.Selected())
	}
	m = press(m, '+')
	if strings.Join(fw.sent, "|") != "plan priority n high charlie" {
		t.Fatalf("+ on n: %v flash %q focus %v", fw.sent, m.ui.flash, m.ui.focus)
	}
	// Again before a poll: stepped from what was written, already high.
	if m = press(m, '+'); len(fw.sent) != 1 || !strings.Contains(m.ui.flash, "already high") {
		t.Fatalf("+ twice: %v %q", fw.sent, m.ui.flash)
	}
	// The low task (from the file) one step up: normal.
	m, fw = fresh('4')
	m.lists.plan.Select("plan:lo")
	if m = press(m, '+'); strings.Join(fw.sent, "|") != "plan priority lo normal charlie" {
		t.Fatalf("+ on lo: %v", fw.sent)
	}
	// The drawer and the task's page.
	m, fw = fresh('p', '-')
	if strings.Join(fw.sent, "|") != "plan priority n low charlie" {
		t.Fatalf("- in the drawer: %v", fw.sent)
	}
	m, fw = fresh('4', kEnter, '+')
	if m.rd.top() == nil || m.rd.top().kind != ovPlanTask || strings.Join(fw.sent, "|") != "plan priority n high charlie" {
		t.Fatalf("+ on the task page: %v", fw.sent)
	}
	// A done task, the DONE fold line: nothing written, the reason said.
	m, fw = fresh('4', 'd')
	m.lists.plan.Select("plan:d")
	if m = press(m, '+'); len(fw.sent) != 0 || !strings.Contains(m.ui.flash, "done") {
		t.Fatalf("+ on a done task: %v %q", fw.sent, m.ui.flash)
	}
	// The tab's frame says the keys, as the mock-ups do.
	if m, _ = fresh('4'); !strings.Contains(m.grid().plain(), "+ - priority") {
		t.Fatal("the Plan tab does not say + - priority")
	}
}
