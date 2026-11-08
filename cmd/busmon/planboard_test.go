package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// home-inbox-drawer §4, step 2 (plan-board): one board for the Plan tab and
// the drawer (p, P), NOW · NEXT · LATER · DONE, the progress, a side panel.

func boardRow(id string, st plan.State, owner string, waitingOn ...string) plan.Row {
	return plan.Row{Task: plan.Task{ID: id, Title: "title " + id, Owner: owner}, State: st, WaitingOn: waitingOn}
}

func boardPlan(rows ...plan.Row) planState {
	f := plan.File{Plan: "v0.29", Goal: "the inbox"}
	for _, r := range rows {
		f.Tasks = append(f.Tasks, r.Task)
	}
	return planState{loaded: true, file: f, rows: rows, rec: bus.PlanRecord{LoadedAt: shellNow.UnixMilli(), Commit: "abcdef0123"}}
}

// sectionOf is the section heading a task's row is drawn under.
func sectionOf(v planBoardView, id string) string {
	sec := ""
	for i, l := range v.lines {
		t := l.text()
		for _, h := range []string{"NOW  ", "NEXT  ", "LATER  ", "DONE  "} {
			if strings.HasPrefix(t, h) {
				sec = strings.TrimSpace(h)
			}
		}
		for _, it := range v.items {
			if it.task == id && it.line == i {
				return sec
			}
		}
	}
	return ""
}

// §9 Plan sections: one row per state lands in its section; Upcoming is
// split by WaitingOn; Unknown beside known rows is LATER; DONE folds.
func TestPlanBoardSections(t *testing.T) {
	p := boardPlan(
		boardRow("up-ready", plan.Upcoming, "coder"),
		boardRow("w", plan.Waiting, plan.Person),
		boardRow("up-later", plan.Upcoming, "coder", "w"),
		boardRow("a", plan.Answered, "master"),
		boardRow("b", plan.Blocked, "coder"),
		boardRow("r", plan.InReview, "coder"),
		boardRow("i", plan.InProgress, "coder"),
		boardRow("u", plan.Unknown, "coder"),
		boardRow("d", plan.Done, "coder"),
	)
	v := planBoard(p, -1, true, shellNow, 120, TierWide, false)
	for id, want := range map[string]string{"w": "NOW", "a": "NOW", "b": "NOW", "r": "NOW", "i": "NOW",
		"up-ready": "NEXT", "up-later": "LATER", "u": "LATER", "d": "DONE"} {
		if got := sectionOf(v, id); got != want {
			t.Errorf("%s in %q, want %s", id, got, want)
		}
	}
	// NOW follows plan.Order: waiting, answered, blocked, in review, in progress
	var now []string
	for _, it := range v.items {
		if sectionOf(v, it.task) == "NOW" {
			now = append(now, it.task)
		}
	}
	if strings.Join(now, " ") != "w a b r i" {
		t.Errorf("NOW order %v", now)
	}
	folded := planBoard(p, -1, false, shellNow, 120, TierWide, false)
	if !strings.Contains(folded.plain, "DONE  1   d shows") || sectionOf(folded, "d") != "" {
		t.Errorf("DONE not folded:\n%s", folded.plain)
	}
	// The progress: the plan, done/total, the bar, the share; narrow: the count only.
	if l := v.lines[0].text(); !strings.HasPrefix(l, "v0.29  1/9 done  ") || !strings.HasSuffix(strings.TrimRight(l, " "), "11%") || !strings.Contains(l, GlyphThinBar) {
		t.Errorf("progress %q", l)
	}
	if l := planBoard(p, -1, false, shellNow, 40, TierNarrow, false).lines[0].text(); strings.TrimRight(l, " ") != "1/9 11%" {
		t.Errorf("narrow progress %q", l)
	}
}

// §4.2 A failed read makes every row not done Unknown: no NOW, NEXT or LATER,
// one muted line, those tasks in file order, then DONE.
func TestPlanBoardAllUnknown(t *testing.T) {
	p := boardPlan(boardRow("x", plan.Unknown, "coder"), boardRow("d", plan.Done, "coder"), boardRow("y", plan.Unknown, plan.Person))
	v := planBoard(p, -1, true, shellNow, 120, TierWide, false)
	for _, h := range []string{"NOW  ", "NEXT  ", "LATER  "} {
		if strings.Contains(v.plain, h) {
			t.Errorf("a section %q with every state unknown:\n%s", h, v.plain)
		}
	}
	if !strings.Contains(v.plain, "a read failed: states unknown") || v.items[0].task != "x" || v.items[1].task != "y" || sectionOf(v, "d") != "DONE" {
		t.Errorf("unknown board:\n%s", v.plain)
	}
}

func planShell(t *testing.T, w, h int) Model {
	t.Helper()
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	st.plan = boardPlan(boardRow("w", plan.Waiting, plan.Person), boardRow("r", plan.InReview, "coder"), boardRow("d", plan.Done, "coder"))
	st.plan.rows[1].Verdict, st.plan.rows[1].PR = "approved", 338
	st.plan.in = plan.Inputs{Board: map[string]bus.BoardEntry{}}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}

// §9 The drawer: p opens it; at the wide tier right of For you, For you still
// drawn, Agents not; Enter opens the task page in the same frame; Esc
// returns, Esc closes; at medium and narrow it is the reader's frame. P too.
func TestPlanDrawer(t *testing.T) {
	for _, key := range []rune{'p', 'P'} {
		m := planShell(t, 140, 40)
		m = keys(t, m, key)
		if p := m.rd.top(); p == nil || p.kind != ovPlan || !p.drawer {
			t.Fatalf("%c did not open the drawer", key)
		}
		r, l := m.readerRect(), m.layout
		if r.X != l.ForYou.W || r.Y != l.ForYou.Y || r.X+r.W != m.width || r.Y+r.H != l.Footer.Y {
			t.Fatalf("%c: drawer %+v, For you %+v", key, r, l.ForYou)
		}
		screen := m.grid().plain()
		if !strings.Contains(screen, "For you") || strings.Contains(screen, "Agents") {
			t.Fatalf("%c: For you hidden or Agents drawn under the drawer:\n%s", key, screen)
		}
		m = keys(t, m, kEnter)
		if p := m.rd.top(); p.kind != ovPlanTask || m.readerRect() != r {
			t.Fatalf("%c: enter: %v in %+v", key, p.kind, m.readerRect())
		}
		if m = keys(t, m, kEsc); m.rd.top() == nil || m.rd.top().kind != ovPlan {
			t.Fatalf("%c: esc did not return to the drawer", key)
		}
		if m = keys(t, m, kEsc); m.rd.open() {
			t.Fatalf("%c: esc did not close", key)
		}
	}
	for _, size := range [][2]int{{100, 40}, {70, 30}} {
		m := keys(t, planShell(t, size[0], size[1]), 'p')
		want := Rect{0, m.layout.Lower.Y, m.width, m.layout.Footer.Y - m.layout.Lower.Y}
		if m.layout.Tier == TierNarrow {
			a := m.layout.rect(m.layout.Active)
			want = Rect{0, a.Y, m.width, m.layout.Footer.Y - a.Y}
		}
		if got := m.readerRect(); got != want {
			t.Errorf("%v: drawer %+v, want the reader's %+v", size, got, want)
		}
		if m.rd.top().side != nil {
			t.Errorf("%v: a side panel below the wide tier", size)
		}
	}
}

// §9 d folds and shows DONE in the drawer; p inside it is not a toggle.
func TestPlanDrawerDoneKey(t *testing.T) {
	m := keys(t, planShell(t, 140, 40), 'p')
	if strings.Contains(m.rd.top().plain, "title d") {
		t.Fatal("DONE shown before d")
	}
	m = keys(t, m, 'd')
	if !strings.Contains(m.rd.top().plain, "title d") {
		t.Fatal("d did not show DONE in the drawer")
	}
	if m = keys(t, m, 'd'); strings.Contains(m.rd.top().plain, "title d") {
		t.Fatal("d again did not fold it")
	}
	if m = keys(t, m, 'p'); !m.rd.open() {
		t.Fatal("p inside the drawer closed it")
	}
}

// §9 Side panel: at the wide tier its lines are the selected row's fields and
// it follows j/k, on the tab and in the drawer.
func TestPlanSidePanel(t *testing.T) {
	text := func(ls []Line) string {
		var b strings.Builder
		for _, l := range ls {
			b.WriteString(l.text() + "\n")
		}
		return b.String()
	}
	m := keys(t, planShell(t, 140, 40), 'p')
	side := text(m.rd.top().side)
	if !strings.Contains(side, "w\n") || !strings.Contains(side, "start it: enter, then s") {
		t.Fatalf("drawer side for w:\n%s", side)
	}
	m = keys(t, m, 'j')
	side = text(m.rd.top().side)
	for _, w := range []string{"in review", "coder", "approved"} {
		if !strings.Contains(side, w) {
			t.Fatalf("after j, side lacks %q:\n%s", w, side)
		}
	}
	if !strings.Contains(m.grid().plain(), "Task") {
		t.Fatal("the side frame is not drawn")
	}
	m = keys(t, planShell(t, 140, 40), '4')
	m.lists.of(PanelPlan).Select("task:r")
	m.refresh()
	if s := text(m.lists.planSide); !strings.Contains(s, "PR 338") && !strings.Contains(s, "approved") {
		t.Fatalf("tab side for r:\n%s", s)
	}
	m = keys(t, planShell(t, 100, 40), '4')
	m.lists.of(PanelPlan).Select("task:r")
	m.refresh()
	if m.lists.planSide != nil {
		t.Fatal("a tab side panel at the medium tier")
	}
}

// The side panel shows the PR and the reason as two fields: a blocked task
// keeps its linked PR (reviewer-codex).
func TestPlanSideShowsPRAndReason(t *testing.T) {
	r := boardRow("b", plan.Blocked, "coder")
	r.PR, r.Reason = 351, "waits for the CI fix"
	var text string
	for _, l := range planSide(r, shellNow) {
		text += l.text() + "\n"
	}
	if !strings.Contains(text, "PR       351") || !strings.Contains(text, "reason   waits for the CI fix") {
		t.Fatalf("side:\n%s", text)
	}
}

// §9 Keys: the main screen's footer holds enter, T, p, F1 and F5 at the
// narrow width before any other key; p follows T.
func TestFooterKeepsPlanAfterTriage(t *testing.T) {
	f := footerHintsOn(ModeShell, PanelForYou, TierNarrow, 60, nil, nil).text()
	for _, w := range []string{"enter", "T triage", "p P plan", "F1", "F5"} {
		if !strings.Contains(f, w) {
			t.Errorf("narrow footer lacks %q: %q", w, f)
		}
	}
	if strings.Index(f, "p P plan") < strings.Index(f, "T triage") {
		t.Errorf("p before T: %q", f)
	}
}

// foureyes on step 1 (home-inbox): the label with one decision reads
// "1 decide"; a directed send to a gate row's agent does not end that step.
func TestForYouLabelOneDecide(t *testing.T) {
	if got := forYouLabel(queueStrip{decide: 1, merge: 1}); got != "1 decide "+GlyphNone+" 1 merge" {
		t.Errorf("label %q", got)
	}
}

func TestTriageSendToAGateAgentDoesNotAdvance(t *testing.T) {
	m := rowsFixture(t, false, false)
	m, it := walkTo(t, m, func(it attentionItem) bool { return it.Rank == rankGate })
	m = step(t, m, sent(it.Agent))
	if cur, ok := m.walkItem(); !ok || cur.Key != it.Key {
		t.Fatal("a message to the gate row's agent moved the walk")
	}
}

// §7 The tour's Useful keys page names p, and a person who saw version 4
// sees it again.
func TestTourUsefulKeysNamesThePlanDrawer(t *testing.T) {
	for _, i := range pagesSince(tourBook, 4) {
		if tourBook[i].title == "Useful keys" {
			return
		}
	}
	t.Fatal("Useful keys (with p) is not shown again after version 4")
}

// §4.4 At the wide tier a click on For you closes the drawer first, then
// lands on For you, as a click outside any page does.
func TestPlanDrawerClickOnForYouCloses(t *testing.T) {
	m := keys(t, planShell(t, 140, 40), 'p')
	fy := m.layout.ForYou
	m = clickAt(t, m, fy.X+3, fy.Y+1)
	if m.rd.open() || m.ui.focus != PanelForYou {
		t.Fatalf("after a click on For you: reader open %v, focus %v", m.rd.open(), m.ui.focus)
	}
}

// foureyes on step 2 (plan-board): a section with no row draws no heading.
func TestPlanBoardEmptySectionNotDrawn(t *testing.T) {
	p := boardPlan(boardRow("n", plan.Upcoming, "coder"), boardRow("d", plan.Done, "coder"))
	v := planBoard(p, 0, true, shellNow, 120, TierWide, true)
	for _, l := range v.lines {
		for _, h := range []string{"NOW", "LATER"} {
			if strings.HasPrefix(l.text(), h+"  ") {
				t.Errorf("empty %s drawn:\n%s", h, v.plain)
			}
		}
	}
	if sectionOf(v, "n") != "NEXT" || sectionOf(v, "d") != "DONE" {
		t.Errorf("sections:\n%s", v.plain)
	}
}

// foureyes on step 2: the drawer's side panel never leaves the list under
// planListMin cells; without that room there is none.
func TestPlanDrawerSideKeepsTheList(t *testing.T) {
	sides := 0
	for w := 120; w <= 220; w += 4 {
		m := keys(t, planShell(t, w, 40), 'p')
		list, side := m.planDrawerWidths()
		room := m.readerWidth() >= planListMin+planSideWidth+1
		if side != room || side && list < planListMin {
			t.Errorf("width %d: reader %d, list %d, side %v", w, m.readerWidth(), list, side)
		}
		if side {
			sides++
		}
	}
	if sides == 0 {
		t.Fatal("no wide width gave the drawer a side panel")
	}
	m := keys(t, planShell(t, 100, 40), 'p') // medium: never a side panel
	if _, side := m.planDrawerWidths(); side {
		t.Error("a drawer side panel at the medium tier")
	}
	// The wide tier's own drawer is 80 cells at least; the rule itself, on a
	// frame one cell short of a list of planListMin beside the panel.
	m.layout.Tier = TierWide
	if r := m.readerWidth(); r >= planListMin+planSideWidth+1 {
		t.Fatalf("fixture: reader %d leaves room", r)
	}
	if _, side := m.planDrawerWidths(); side {
		t.Errorf("a side panel leaving the list %d cells", m.readerWidth()-planSideWidth-1)
	}
}

// foureyes on step 2: the person's done task asks nothing of them.
func TestPlanSideForYouOnAPersonsDoneTask(t *testing.T) {
	found := false
	for _, l := range planSide(boardRow("d", plan.Done, plan.Person), shellNow) {
		if strings.HasPrefix(l.text(), "for you") {
			found = true
			if strings.TrimRight(l.text(), " ") != "for you  nothing" {
				t.Fatalf("for you on a done task: %q", l.text())
			}
		}
	}
	if !found {
		t.Fatal("no for you field")
	}
}
