package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/memory"
	"github.com/netbja/agent-bus-monitor/plan"
)

// workShared is the Work tab's shell fixture: a plan with a task in hand, a
// person's task, a next one; a request in hand and one not in the plan; a
// review gate; an idle agent; a really open idea (the DeepSeek one).
func workShared() *shared {
	now := shellNow
	task := func(id, title, owner, board string) plan.Task {
		return plan.Task{ID: id, Title: title, Owner: owner, Board: board}
	}
	st := &shared{agents: map[string]*agentState{
		"coder":    {state: "working", message: "compose-at-file step 2", lastSeen: now},
		"foureyes": {state: "working", lastSeen: now, gates: map[string]string{msID(now.Add(-12 * time.Minute)): "coder|[subject compose-at-file-home] review"}},
		"sentinel": {state: "idle", lastSeen: now},
	}, person: "charlie"}
	st.plan = planState{loaded: true, file: plan.File{Plan: "v0.29"}, in: plan.Inputs{Board: map[string]bus.BoardEntry{}},
		rows: []plan.Row{
			{Task: task("t6", "idle-breathing", "coder", "idle-breathing"), State: plan.Done},
			{Task: task("t7", "compose-at-file", "coder", "compose-at-file"), State: plan.InProgress},
			{Task: task("t5", "Decide: notifications order", plan.Person, ""), State: plan.Waiting},
			{Task: task("t8", "requests-plan redesign", "coder", "requests-plan"), State: plan.Upcoming, WaitingOn: []string{"t7"}},
		}}
	st.requests = []request{
		{Task: "compose-at-file", Tracked: true, State: "accepted", From: "master", Target: "coder", Accepted: now.Add(-50 * time.Minute)},
		{Task: "requests-plan", Tracked: true, State: "requested", From: "master", Target: "coder", Created: now.Add(-time.Hour)},
		{Task: "kimi-quota", Tracked: true, State: "requested", From: "master", Target: "coder", Created: now.Add(-2 * time.Hour)},
	}
	st.cmdHistory = []bus.Event{{ID: msID(now.Add(-3 * time.Hour)), Kind: "cmd", Type: bus.CmdDirective, From: "master", Target: "charlie", Message: "which tag?"}}
	st.ideas = []memory.Question{{ID: "2026-11-03-deepseek-superagent-ideas", Title: "Three ideas from DeepSeek's Super-Agent prototype",
		Path: "docs/memory/open-questions/2026-11-03-deepseek-superagent-ideas.md"}}
	return st
}

func tabText(l Line) string {
	s := ""
	for _, sg := range l {
		s += sg.Text
	}
	return s
}

func workShell(t *testing.T, w, h int) Model {
	t.Helper()
	st := workShared()
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}

// Tab 3 is Work, in place of Requests and Plan; 4 is an alias to Work on
// NEXT, no fourth tab (correction 8); at the narrow tier 5 is Work, 6 the
// alias.
func TestWorkTabKeys(t *testing.T) {
	pinLocal(t)
	bar := tabText(tabBar(lowerTabs, PanelActivity, 200, true))
	if !strings.Contains(bar, " 3 Work ") || strings.Contains(bar, "Requests") || strings.Contains(bar, "Plan") || strings.Contains(bar, " 4 ") {
		t.Fatalf("tab bar %q", bar)
	}
	m := keys(t, workShell(t, 140, 40), '3')
	if m.ui.focus != PanelWork || m.layout.Active != PanelWork {
		t.Fatalf("3: focus %v active %v", m.ui.focus, m.layout.Active)
	}
	m = keys(t, workShell(t, 140, 40), '4')
	if m.ui.focus != PanelWork || m.lists.work.Selected() != "plan:t8" {
		t.Fatalf("4: focus %v selected %q, want Work on NEXT's first row", m.ui.focus, m.lists.work.Selected())
	}
	m = keys(t, workShell(t, 60, 24), '5')
	if m.layout.Active != PanelWork {
		t.Fatalf("5 at narrow: %v", m.layout.Active)
	}
	m = keys(t, workShell(t, 60, 24), '6')
	if m.layout.Active != PanelWork || m.lists.work.Selected() != "plan:t8" {
		t.Fatalf("6 at narrow: %v %q", m.layout.Active, m.lists.work.Selected())
	}
	nb := tabText(tabBar(narrowPanels, PanelForYou, 200, true))
	if !strings.Contains(nb, "5 Work") || strings.Contains(nb, " 6 ") {
		t.Fatalf("narrow bar %q", nb)
	}
}

// The page reads top-down: YOU, TEAM (the reviewers and the gate's age, the
// idle line), NEXT, IDEAS, the folded lines; the label counts them.
func TestWorkTabRows(t *testing.T) {
	pinLocal(t)
	m := keys(t, workShell(t, 140, 40), '3')
	var text []string
	for _, it := range m.lists.work.items {
		text = append(text, it.Line.text())
	}
	page := strings.Join(text, "\n")
	order := []string{"YOU  2", "answer", "which tag?", "decide", "Decide: notifications order", "TEAM  2", "coder", "review: foureyes 12m",
		"reviewing compose-at-file-home for coder", "sentinel", "NEXT  2", "requests-plan redesign", "waits on t7", "kimi-quota", "not in the plan",
		"IDEAS  1", "Three ideas from DeepSeek", "between agents", "done " + GlyphNone + " 1"}
	at := 0
	for _, w := range order {
		i := strings.Index(page[at:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order:\n%s", w, page)
		}
		at += i
	}
	if got := m.lists.work.label.text(); got != "2 for you "+GlyphNone+" 2 at work "+GlyphNone+" 2 next "+GlyphNone+" 1 ideas" {
		t.Fatalf("label %q", got)
	}
}

// Correction 6: the 80-column view labels its counts with the glyphs and
// keeps the idle line.
func TestWorkTabCompact(t *testing.T) {
	pinLocal(t)
	m := keys(t, workShell(t, 80, 40), '3')
	if got := m.lists.work.label.text(); got != GlyphHuman+"2 "+GlyphWorking+"2 "+GlyphIdle+"2 "+GlyphIdea+"1" {
		t.Fatalf("compact label %q", got)
	}
	idle := false
	for _, it := range m.lists.work.items {
		if it.Line.Width() > m.lists.work.rect.W {
			t.Errorf("row wider than the frame: %q", it.Line.text())
		}
		idle = idle || strings.Contains(it.Line.text(), GlyphIdle+" sentinel")
	}
	if !idle {
		t.Fatal("no idle line at 80 columns")
	}
}

// Enter: each row opens its own page; the folded lines open the traffic
// between agents (a sub-view of Work, esc back) and the plan with DONE.
func TestWorkTabEnter(t *testing.T) {
	pinLocal(t)
	open := func(key string) Model {
		m := keys(t, workShell(t, 140, 40), '3')
		m.lists.work.Select(key)
		if m.lists.work.Selected() != key {
			t.Fatalf("cannot select %q", key)
		}
		return keys(t, m, kEnter)
	}
	if p := open("plan:t5").rd.top(); p == nil || p.kind != ovPlanTask || p.key != "t5" {
		t.Fatalf("decide row: %+v", p)
	}
	if p := open("agent:coder").rd.top(); p == nil || p.kind != ovAgent {
		t.Fatalf("team row: %+v", p)
	}
	p := open("idea:2026-11-03-deepseek-superagent-ideas").rd.top()
	if p == nil || p.kind != ovIdea || p.plain != "docs/memory/open-questions/2026-11-03-deepseek-superagent-ideas.md" {
		t.Fatalf("idea row: %+v", p)
	}
	if p := open("req:kimi-quota").rd.top(); p == nil || p.kind != ovRequest {
		t.Fatalf("request row: %+v", p)
	}
	m := open(workFoldBetween)
	if m.ui.focus != PanelRequests || m.layout.Active != PanelRequests || m.rd.open() {
		t.Fatalf("between agents: focus %v active %v", m.ui.focus, m.layout.Active)
	}
	if bar := tabText(m.lists.requests.title); !strings.Contains(bar, "between agents") {
		t.Fatalf("sub-view title %q", bar)
	}
	if r := keys(t, m, tea.Key{Code: tea.KeyRight}); r.ui.focus != PanelActivity { // Work is the last tab: → wraps
		t.Fatalf("→ in the sub-view: focus %v", r.ui.focus)
	}
	if r := keys(t, m, tea.Key{Code: tea.KeyLeft}); r.ui.focus != PanelBoard {
		t.Fatalf("← in the sub-view: focus %v", r.ui.focus)
	}
	m = keys(t, m, kEsc)
	if m.ui.focus != PanelWork || m.lists.work.Selected() != workFoldBetween {
		t.Fatalf("esc: focus %v selected %q", m.ui.focus, m.lists.work.Selected())
	}
	if p := open(workFoldDone).rd.top(); p == nil || p.kind != ovPlan || !p.expanded {
		t.Fatalf("done line: %+v", p)
	}
	m = keys(t, keys(t, workShell(t, 140, 40), '3'), 'd')
	if p := m.rd.top(); p == nil || p.kind != ovPlan || !p.expanded {
		t.Fatalf("d on Work: %+v", p)
	}
}

// Correction 7: + and - act on a row that stands for a tracked request; on
// any other, nothing, and the footer says why.
func TestWorkTabPriorityTarget(t *testing.T) {
	m := keys(t, workShell(t, 140, 40), '3')
	m.lists.work.Select("plan:t8") // names requests-plan
	if r, why := m.priorityTarget(); why != "" || r.Task != "requests-plan" {
		t.Fatalf("plan row with its request: %q %q", r.Task, why)
	}
	m.lists.work.Select("plan:t5")
	if _, why := m.priorityTarget(); why != "only a tracked request has a priority" {
		t.Fatalf("a decide row: %q", why)
	}
	m = keys(t, m, '+')
	if m.ui.flash != "only a tracked request has a priority" {
		t.Fatalf("+ on a decide row: flash %q", m.ui.flash)
	}
}

// y copies a row's text, an idea's path.
func TestWorkTabCopy(t *testing.T) {
	m := keys(t, workShell(t, 140, 40), '3')
	if text, what := m.workCopy("idea:2026-11-03-deepseek-superagent-ideas"); what != "path" || !strings.HasSuffix(text, "deepseek-superagent-ideas.md") {
		t.Fatalf("idea: %q %q", text, what)
	}
	if text, _ := m.workCopy("plan:t5"); text != "Decide: notifications order" {
		t.Fatalf("decide row: %q", text)
	}
}

// foureyes: the decide chip counts the person's plan tasks too; with no
// For you row to select, its click selects Work's.
func TestDecideChipSelectsWork(t *testing.T) {
	pinLocal(t)
	m := workShell(t, 140, 40)
	if m.forYouHas(isDecide) {
		t.Fatal("fixture: For you has a decide row")
	}
	m.headerClick(0, 0) // ensure no panic off a chip
	var x int
	_, spans := headerLayout(m.headerData(), m.width, m.layout.Tier, m.layout.Header.H)
	found := false
	for _, s := range spans {
		if s.hit == hitDecide {
			x, found = s.x, true
		}
	}
	if !found {
		t.Fatal("no decide chip")
	}
	m.headerClick(x, 0)
	if m.ui.focus != PanelWork || m.lists.work.Selected() != m.work.You[0].Key || m.work.You[0].Verb == verbMerge {
		t.Fatalf("decide chip: focus %v selected %q", m.ui.focus, m.lists.work.Selected())
	}
}

// A gate whose ref is no stream id has no age (never "106751d"); the
// person's own rows say you once.
func TestWorkTabGateAgeAndYou(t *testing.T) {
	if got := teamReviewers(teamRow{Reviewers: []teamGate{{Who: "foureyes"}}}, shellNow); got != "  review: foureyes" {
		t.Fatalf("undated gate: %q", got)
	}
	m := keys(t, workShell(t, 140, 40), '3')
	for _, it := range m.lists.work.items {
		if strings.Contains(it.Line.text(), GlyphHuman+" decide "+GlyphHuman) || strings.Contains(it.Line.text(), "decide   "+GlyphHuman+" you") {
			t.Fatalf("doubled mark: %q", it.Line.text())
		}
	}
}
