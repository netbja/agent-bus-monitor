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
		for _, h := range []string{"NOW  ", "APPROVED  ", "NEXT  ", "LATER  ", "DONE  "} {
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

// views-redesign §4: the roadmap. NOW is in hand (in progress, in review,
// answered, blocked), by priority then file order; APPROVED holds what both
// reviewers approved, waiting for delivery or the merge; NEXT is not
// started and unblocked, the person's decisions among them, by priority then
// file order; LATER is blocked by another task, then the low-priority ones,
// blocked or not, then the unknown; DONE folds.
func TestPlanBoardSections(t *testing.T) {
	prio := func(r plan.Row, p string) plan.Row { r.Task.Priority = p; return r }
	approved := func(r plan.Row) plan.Row { r.Verdict = plan.Approved; return r }
	p := boardPlan(
		boardRow("up-ready", plan.Upcoming, "coder"),
		boardRow("w", plan.Waiting, plan.Person),
		boardRow("up-later", plan.Upcoming, "coder", "w"),
		boardRow("a", plan.Answered, "master"),
		boardRow("b", plan.Blocked, "coder"),
		boardRow("r", plan.InReview, "coder"),
		prio(boardRow("i", plan.InProgress, "coder"), "high"),
		boardRow("u", plan.Unknown, "coder"),
		boardRow("d", plan.Done, "coder"),
		approved(boardRow("rev-ok", plan.InReview, "coder2")),
		approved(boardRow("merge-me", plan.Waiting, "coder")),
		prio(boardRow("up-high", plan.Upcoming, "coder"), "high"),
		prio(boardRow("up-low", plan.Upcoming, "coder"), "low"),
		prio(boardRow("up-later-high", plan.Upcoming, "coder", "r"), "high"),
	)
	v := planBoard(p, -1, true, shellNow, 120, TierWide, false, nil)
	for id, want := range map[string]string{"a": "NOW", "b": "NOW", "r": "NOW", "i": "NOW",
		"rev-ok": "APPROVED", "merge-me": "APPROVED",
		"w": "NEXT", "up-ready": "NEXT", "up-high": "NEXT",
		"up-later": "LATER", "up-later-high": "LATER", "up-low": "LATER", "u": "LATER", "d": "DONE"} {
		if got := sectionOf(v, id); got != want {
			t.Errorf("%s in %q, want %s", id, got, want)
		}
	}
	order := func(sec string) string {
		var ids []string
		for _, it := range v.items {
			if sectionOf(v, it.task) == sec {
				ids = append(ids, it.task)
			}
		}
		return strings.Join(ids, " ")
	}
	for sec, want := range map[string]string{
		"NOW":      "i a b r",                         // the high one first, then file order
		"NEXT":     "up-high up-ready w",              // by priority, then file order
		"LATER":    "up-later up-later-high up-low u", // blocked by a task, then low, then unknown
		"APPROVED": "rev-ok merge-me",
	} {
		if got := order(sec); got != want {
			t.Errorf("%s order %q, want %q", sec, got, want)
		}
	}
	empty := planBoard(boardPlan(boardRow("x", plan.Upcoming, "coder")), -1, false, shellNow, 120, TierWide, false, nil)
	if strings.Contains(empty.plain, "APPROVED") {
		t.Errorf("an empty APPROVED is drawn:\n%s", empty.plain)
	}
	folded := planBoard(p, -1, false, shellNow, 120, TierWide, false, nil)
	if !strings.Contains(folded.plain, "DONE  1   d shows") || sectionOf(folded, "d") != "" {
		t.Errorf("DONE not folded:\n%s", folded.plain)
	}
	// The progress: the plan, done/total, the bar, the share; narrow: the count only.
	if l := v.lines[0].text(); !strings.HasPrefix(l, "v0.29  1 of 14 done  ") || !strings.Contains(l, "  7%  ") || !strings.Contains(l, GlyphThinBar) { // progress-line §2.3: N of M, then now and next
		t.Errorf("progress %q", l)
	}
	if l := planBoard(p, -1, false, shellNow, 40, TierNarrow, false, nil).lines[0].text(); strings.TrimRight(l, " ") != "1/14 7%" {
		t.Errorf("narrow progress %q", l)
	}
}

// §4.2 A failed read makes every row not done Unknown: no NOW, NEXT or LATER,
// one muted line, those tasks in file order, then DONE.
func TestPlanBoardAllUnknown(t *testing.T) {
	p := boardPlan(boardRow("x", plan.Unknown, "coder"), boardRow("d", plan.Done, "coder"), boardRow("y", plan.Unknown, plan.Person))
	v := planBoard(p, -1, true, shellNow, 120, TierWide, false, nil)
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
	m := keys(t, planShell(t, 140, 40), 'p') // APPROVED's r first, then NEXT's w (views-redesign §4)
	side := text(m.rd.top().side)
	for _, w := range []string{"in review", "coder", "approved"} {
		if !strings.Contains(side, w) {
			t.Fatalf("side for r lacks %q:\n%s", w, side)
		}
	}
	m = keys(t, m, 'j')
	side = text(m.rd.top().side)
	if !strings.Contains(side, "w\n") || !strings.Contains(side, "start it: enter, then s") {
		t.Fatalf("after j, side for w:\n%s", side)
	}
	if !strings.Contains(m.grid().plain(), "Task") {
		t.Fatal("the side frame is not drawn")
	}
}

// The side panel shows the PR and the reason as two fields: a blocked task
// keeps its linked PR (reviewer-codex).
func TestPlanSideShowsPRAndReason(t *testing.T) {
	r := boardRow("b", plan.Blocked, "coder")
	r.PR, r.Reason = 351, "waits for the CI fix"
	var text string
	for _, l := range planSide(boardPlan(r), r, shellNow) {
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
	v := planBoard(p, 0, true, shellNow, 120, TierWide, true, nil)
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
	for _, l := range planSide(boardPlan(boardRow("d", plan.Done, plan.Person)), boardRow("d", plan.Done, plan.Person), shellNow) {
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

// views-redesign §4: a row's first cell is its priority (↑ high, ↓ low), or
// its state's glyph in NOW (✓ in APPROVED), nothing in NEXT and LATER; its
// dependencies read "after <first>" with "+N" for more; the person's task
// says ♙ before its owner and "decide", never "waiting for you".
func TestPlanBoardRowMarks(t *testing.T) {
	prio := func(r plan.Row, p string, after ...string) plan.Row {
		r.Task.Priority, r.Task.After = p, after
		return r
	}
	approved := boardRow("ok", plan.InReview, "coder2")
	approved.Verdict = plan.Approved
	p := boardPlan(
		prio(boardRow("i", plan.InProgress, "coder"), "", "dep1"),
		approved,
		prio(boardRow("hi", plan.Upcoming, "coder"), "high", "dep1", "dep2", "dep3"),
		boardRow("plain", plan.Upcoming, "coder"),
		boardRow("decide-x", plan.Waiting, plan.Person),
		prio(boardRow("lo", plan.Upcoming, "coder"), "low"),
		boardRow("dep1", plan.Done, "coder"),
		boardRow("dep2", plan.Done, "coder"),
		boardRow("dep3", plan.Done, "coder"),
	)
	v := planBoard(p, -1, false, shellNow, 140, TierWide, false, nil)
	line := func(id string) string {
		for _, it := range v.items {
			if it.task == id {
				return strings.TrimSpace(v.lines[it.line].text())
			}
		}
		t.Fatalf("no row %s", id)
		return ""
	}
	for id, want := range map[string]string{
		"i": GlyphWorking + " title i", "ok": GlyphDone + " title ok", "hi": GlyphUp + " title hi",
		"lo": GlyphDownArrow + " title lo", "plain": "title plain", "decide-x": "title decide-x",
	} {
		if got := line(id); !strings.HasPrefix(got, want) {
			t.Errorf("%s: %q, want it to start with %q", id, got, want)
		}
	}
	if got := line("hi"); !strings.Contains(got, "after dep1 +2") {
		t.Errorf("hi: %q, want after dep1 +2", got)
	}
	if got := line("i"); !strings.Contains(got, "after dep1") || strings.Contains(got, "+") {
		t.Errorf("i: %q", got)
	}
	if got := line("decide-x"); !strings.Contains(got, GlyphHuman+" person") || !strings.Contains(got, "decide") || strings.Contains(got, "waiting for you") {
		t.Errorf("decide-x: %q", got)
	}
	if got := line("ok"); !strings.Contains(got, "approved") {
		t.Errorf("ok: %q", got)
	}
}

// views-redesign §4: the side panel lists every dependency, ✓ when done,
// the tasks that wait on this one, and its priority.
func TestPlanSideDependencies(t *testing.T) {
	a := boardRow("a", plan.Done, "coder")
	b := boardRow("b", plan.InProgress, "coder")
	x := boardRow("x", plan.Upcoming, "coder", "b")
	x.Task.After, x.Task.Priority = []string{"a", "b"}, "high"
	y := boardRow("y", plan.Upcoming, "coder", "x")
	y.Task.After = []string{"x"}
	p := boardPlan(a, b, x, y)
	text := linesText(planSide(p, x, shellNow))
	for _, w := range []string{"after    a " + GlyphDone + ", b", "unblocks y", "prio     " + GlyphUp + " high"} {
		if !strings.Contains(text, w) {
			t.Errorf("side lacks %q:\n%s", w, text)
		}
	}
	if text := linesText(planSide(p, a, shellNow)); !strings.Contains(text, "after    "+GlyphDash) || !strings.Contains(text, "unblocks x") || !strings.Contains(text, "prio     normal") {
		t.Errorf("a's side:\n%s", text)
	}
}

// views-redesign §4 (the person's Q5): below the wide tier (the 80-column
// mock-up: no side panel) LATER folds to a count that keeps a high-priority
// task visible, "LATER 3 · 1 ↑ (enter)", and DONE to its count, each a line
// Enter opens; at the wide tier LATER is drawn whole.
func TestPlanBoardNarrowFoldsLater(t *testing.T) {
	hi := boardRow("hi", plan.Upcoming, "coder", "now1")
	hi.Task.Priority = "high"
	lo := boardRow("lo", plan.Upcoming, "coder")
	lo.Task.Priority = "low"
	p := boardPlan(boardRow("now1", plan.InProgress, "coder"), hi, lo, boardRow("w2", plan.Upcoming, "coder", "now1"), boardRow("d", plan.Done, "coder"))
	v := planBoardOf(p, -1, false, false, shellNow, 76, TierNarrow, false, nil)
	if !strings.Contains(v.plain, "LATER 3 "+GlyphNone+" 1 "+GlyphUp+" (enter)") || !strings.Contains(v.plain, "DONE 1 (enter)") {
		t.Fatalf("folded:\n%s", v.plain)
	}
	for _, id := range []string{"hi", "lo", "w2"} {
		if itemLine(v, id) >= 0 {
			t.Fatalf("%s drawn while LATER is folded:\n%s", id, v.plain)
		}
	}
	var folds []string
	for _, it := range v.items {
		if it.fold != "" {
			folds = append(folds, it.fold)
		}
	}
	if strings.Join(folds, " ") != "later done" {
		t.Fatalf("fold items %q", folds)
	}
	open := planBoardOf(p, -1, false, true, shellNow, 76, TierNarrow, false, nil)
	if itemLine(open, "hi") < 0 || itemLine(open, "lo") < 0 {
		t.Fatalf("LATER open:\n%s", open.plain)
	}
	if med := planBoardOf(p, -1, false, false, shellNow, 76, TierMedium, false, nil); itemLine(med, "hi") >= 0 || !strings.Contains(med.plain, "LATER 3 ") {
		t.Fatalf("medium (80 columns) not folded:\n%s", med.plain)
	}
	if wide := planBoard(p, -1, false, shellNow, 140, TierWide, false, nil); itemLine(wide, "hi") < 0 || strings.Contains(wide.plain, "(enter)") {
		t.Fatalf("wide:\n%s", wide.plain)
	}
	// Enter on the folds, in the shell at 76 columns: LATER opens, then DONE.
	m := keys(t, planShellOf(t, p, 76, 30), 'p')
	pg := m.rd.top()
	for i, it := range pg.items {
		if it.fold == "later" {
			pg.sel = i
		}
	}
	m = keys(t, m, kEnter)
	if pg = m.rd.top(); pg.kind != ovPlan || !pg.laterOpen || !strings.Contains(pg.plain, "title hi") {
		t.Fatalf("enter on LATER: %+v\n%s", pg.kind, pg.plain)
	}
	for i, it := range pg.items {
		if it.fold == "done" {
			pg.sel = i
		}
	}
	if pg = keys(t, m, kEnter).rd.top(); !pg.expanded || !strings.Contains(pg.plain, "title d") {
		t.Fatalf("enter on DONE:\n%s", pg.plain)
	}
}

// planShellOf is the shell with plan p loaded, at w×h.
func planShellOf(t *testing.T, p planState, w, h int) Model {
	t.Helper()
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	st.plan = p
	st.plan.in = plan.Inputs{Board: map[string]bus.BoardEntry{}}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}
