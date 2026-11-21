package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/plan"
)

// views-redesign §2, §4: Plan is the lower panel's fourth tab (4; 6 at the
// narrow tier), a list like Board's; p and P keep opening the drawer.

func TestPlanTabKeys(t *testing.T) {
	pinLocal(t)
	bar := tabText(tabBar(lowerTabs, PanelActivity, 200, true))
	if !strings.Contains(bar, " 4 Plan ") {
		t.Fatalf("tab bar %q lacks 4 Plan", bar)
	}
	m := keys(t, planShell(t, 140, 40), '4')
	if m.rd.open() || m.ui.focus != PanelPlan || m.layout.Active != PanelPlan {
		t.Fatalf("4: reader %v focus %v active %v, want the Plan tab", m.rd.open(), m.ui.focus, m.layout.Active)
	}
	if nb := tabText(tabBar(narrowPanels, PanelForYou, 200, true)); !strings.Contains(nb, "6 Pln") && !strings.Contains(nb, "6 Plan") {
		t.Fatalf("narrow bar %q lacks 6", nb)
	}
	m = keys(t, planShell(t, 60, 24), '6')
	if m.rd.open() || m.layout.Active != PanelPlan {
		t.Fatalf("6 at narrow: reader %v active %v", m.rd.open(), m.layout.Active)
	}
	// p and P keep the drawer, from the tab too.
	for _, k := range []rune{'p', 'P'} {
		if p := keys(t, planShell(t, 140, 40), '4', k).rd.top(); p == nil || p.kind != ovPlan || !p.drawer {
			t.Fatalf("%c on the tab: %+v, want the drawer", k, p)
		}
	}
	// The arrows step across the four tabs: Work → Plan → Activity.
	if m = keys(t, planShell(t, 140, 40), '3', kRight); m.ui.focus != PanelPlan {
		t.Fatalf("→ from Work: %v", m.ui.focus)
	}
	if m = keys(t, m, kRight); m.ui.focus != PanelActivity {
		t.Fatalf("→ from Plan: %v, want Activity (wrap)", m.ui.focus)
	}
	if m = keys(t, m, kLeft); m.ui.focus != PanelPlan {
		t.Fatalf("← from Activity: %v", m.ui.focus)
	}
}

// The tab's rows are the roadmap's: its frame says the plan and its progress,
// never a for-you count; Enter opens the task page and Esc returns to the
// tab; d shows DONE.
func TestPlanTabRows(t *testing.T) {
	pinLocal(t)
	m := keys(t, planShell(t, 140, 40), '4')
	screen := m.grid().plain()
	for _, w := range []string{"4 Plan", "v0.29", "1/3   2 left", "APPROVED", "NEXT", "title r", "title w", "DONE"} {
		if !strings.Contains(screen, w) {
			t.Fatalf("tab lacks %q:\n%s", w, screen)
		}
	}
	if strings.Contains(screen, "title d") {
		t.Fatalf("DONE open before d:\n%s", screen)
	}
	top := strings.SplitN(screen[strings.Index(screen, "4 Plan"):], "\n", 2)[0]
	if strings.Contains(top, GlyphHuman) {
		t.Fatalf("the Plan's title counts the person's tasks: %q", top)
	}
	if got := m.lists.plan.Selected(); got != "plan:r" {
		t.Fatalf("selected %q, want the first task", got)
	}
	m = keys(t, m, kEnter)
	if p := m.rd.top(); p == nil || p.kind != ovPlanTask || p.key != "r" {
		t.Fatalf("enter: %+v", p)
	}
	if m = keys(t, m, kEsc); m.rd.open() || m.ui.focus != PanelPlan {
		t.Fatalf("esc: reader %v focus %v", m.rd.open(), m.ui.focus)
	}
	if m = keys(t, m, 'd'); !strings.Contains(m.grid().plain(), "title d") {
		t.Fatalf("d did not show DONE:\n%s", m.grid().plain())
	}
	if m = keys(t, m, 'd'); strings.Contains(m.grid().plain(), "title d") {
		t.Fatal("d again did not fold DONE")
	}
}

// The detail at the wide tier (views-redesign §4.1) follows the selection;
// none below it.
func TestPlanTabSidePanel(t *testing.T) {
	pinLocal(t)
	m := keys(t, planShell(t, 160, 40), '4')
	screen := m.grid().plain()
	for _, w := range []string{"r " + GlyphNone + " in review", "approved", "for you:", "enter: task page"} {
		if !strings.Contains(screen, w) {
			t.Fatalf("detail lacks %q:\n%s", w, screen)
		}
	}
	m = keys(t, m, 'j')
	if !strings.Contains(m.grid().plain(), "waiting for you") {
		t.Fatalf("detail did not follow j:\n%s", m.grid().plain())
	}
	if s := keys(t, planShell(t, 70, 40), '6').grid().plain(); strings.Contains(s, "enter: task page") { // the strip shows at every tier but the narrow one (§4.2)
		t.Fatalf("a detail below the wide tier:\n%s", s)
	}
}

// Below the wide tier LATER and DONE fold to counts that Enter opens.
func TestPlanTabNarrowFolds(t *testing.T) {
	pinLocal(t)
	hi := boardRow("hi", plan.Upcoming, "coder", "now1")
	hi.Task.Priority = "high"
	p := boardPlan(boardRow("now1", plan.InProgress, "coder"), hi, boardRow("d", plan.Done, "coder"))
	m := keys(t, planShellOf(t, p, 76, 30), '6')
	if m.layout.Active != PanelPlan {
		t.Fatalf("6: active %v", m.layout.Active)
	}
	if s := m.grid().plain(); !strings.Contains(s, "LATER 1") || strings.Contains(s, "title hi") {
		t.Fatalf("LATER not folded:\n%s", s)
	}
	m.lists.plan.Select("fold:later")
	if w, _ := rowHint(PanelPlan, m.selectedItem(PanelPlan)); w != "unfold" {
		t.Fatalf("the fold's word %q, want unfold", w)
	}
	if m = keys(t, m, kEnter); m.rd.open() || !strings.Contains(m.grid().plain(), "title hi") {
		t.Fatalf("enter on LATER:\n%s", m.grid().plain())
	}
	if l := keys(t, m, '5', '6'); strings.Contains(l.grid().plain(), "title hi") { // left and back: folded again
		t.Fatalf("LATER still open after leaving the tab:\n%s", l.grid().plain())
	}
	// The Requests sub-view opened from the tab is no leaving: LATER stays open (foureyes V2).
	if l := keys(t, m, 'r', kEsc); l.ui.focus != PanelPlan || !strings.Contains(l.grid().plain(), "title hi") {
		t.Fatalf("r and esc folded LATER (focus %v):\n%s", l.ui.focus, l.grid().plain())
	}
	m.lists.plan.Select("fold:done")
	if m = keys(t, m, kEnter); !strings.Contains(m.grid().plain(), "title d") {
		t.Fatalf("enter on DONE:\n%s", m.grid().plain())
	}
}

// No plan loaded: the tab says how to load one.
func TestPlanTabNoPlan(t *testing.T) {
	pinLocal(t)
	m := keys(t, planShellOf(t, planState{}, 140, 40), '4')
	if m.ui.focus != PanelPlan || !strings.Contains(m.grid().plain(), "no plan loaded") {
		t.Fatalf("no plan: focus %v\n%s", m.ui.focus, m.grid().plain())
	}
}

// The mock-ups of §4 as goldens: the tab at 160 columns with its Releases
// column (plan-tab-r3), at 80 without it, LATER and DONE folded.
func TestPlanTabGoldens(t *testing.T) {
	pinLocal(t)
	row := func(id string, st plan.State, owner, prio string, after ...string) plan.Row {
		r := boardRow(id, st, owner)
		r.Task.Title, r.Task.Priority, r.Task.After = id, prio, after
		return r
	}
	blocked := func(r plan.Row) plan.Row { r.WaitingOn = r.Task.After; return r } // as plan.Derive blocks a task on an open dependency
	appr := row("human-not-an-agent-test", plan.InReview, "coder2", "")
	appr.Verdict = plan.Approved
	p := boardPlan(
		row("holder-g1", plan.Done, "coder", ""),
		row("quota-settings-step2", plan.Done, "coder", ""),
		row("agent-screen-blocked", plan.InProgress, "coder", "", "holder-g1"),
		row("ask-refuses-person", plan.InReview, "coder2", ""),
		appr,
		row("quota-step-3", plan.Upcoming, "coder", "high", "quota-settings-step2"),
		row("holder-g1-part2", plan.Upcoming, "coder", ""),
		row("decide-compose-undo", plan.Waiting, plan.Person, ""),
		blocked(row("quota-step-4", plan.Upcoming, "coder", "high", "quota-step-3")),
		blocked(row("master-conversation", plan.Upcoming, "coder", "low", "decide-compose-undo")),
		row("agent-led-setup", plan.Upcoming, "coder", "low"),
	)
	for _, c := range []struct {
		name string
		w    int
	}{{"plan-tab-160x34.txt", 160}, {"plan-tab-80x34.txt", 80}} {
		m := keys(t, planShellWith(t, p, c.w, 34, releasesFixture), '4')
		r := m.layout.rect(PanelPlan)
		if m.ui.focus != PanelPlan || r.W == 0 {
			t.Fatalf("%s: no Plan (focus %v, rect %+v)", c.name, m.ui.focus, r)
		}
		w := r.W + m.layout.Side.W // the Releases column too, from 130 columns (plan-tab-r3 item 5)
		lines := strings.Split(m.grid().plain(), "\n")
		var frame []string
		for y := r.Y; y < r.Y+r.H && y < len(lines); y++ {
			frame = append(frame, strings.TrimRight(string([]rune(lines[y])[r.X:r.X+w]), " "))
		}
		checkGolden(t, c.name, strings.Join(frame, "\n")+"\n")
	}
}
