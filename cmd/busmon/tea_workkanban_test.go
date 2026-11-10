package main

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// Q1: auto is the Kanban from 130 columns of busmon's pane; list and kanban
// keep one at every width.
func TestWorkKanbanOn(t *testing.T) {
	for _, c := range []struct {
		layout string
		width  int
		want   bool
	}{
		{"auto", 129, false}, {"auto", 130, true}, {"", 200, true}, {"list", 200, false}, {"kanban", 80, true},
	} {
		if got := workKanbanOn(c.layout, c.width); got != c.want {
			t.Errorf("%q at %d: %v", c.layout, c.width, got)
		}
	}
}

// At 130 columns the four columns sit side by side, 31 cells each (the
// person: about 32); narrower they stack two per band, then one.
func TestKanbanGrid(t *testing.T) {
	g := kanbanGrid(4, 1, 5, 128, 20) // a 130-column screen's lower frame, inside its borders
	for i, r := range g {
		if r.w != 31 || r.y != 5 || r.h != 20 || r.x != 1+i*32 {
			t.Fatalf("130: column %d %+v", i, r)
		}
	}
	g = kanbanGrid(4, 0, 0, 78, 20)
	if g[0].w != 38 || g[1].x != 39 || g[2].y != 10 || g[3].h != 10 {
		t.Fatalf("2x2: %+v", g)
	}
	g = kanbanGrid(4, 0, 0, 40, 20)
	if g[0].w != 40 || g[3].y != 15 {
		t.Fatalf("one per band: %+v", g)
	}
}

func kanbanShell(t *testing.T, w, h int) Model {
	t.Helper()
	return keys(t, workShell(t, w, h), '3')
}

// The Kanban draws the four columns from the same model, the For you count
// equal to the list's YOU.
func TestWorkKanbanDraws(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	if m.lists.work.kanban == nil {
		t.Fatal("no Kanban at 130 columns")
	}
	screen := m.grid().plain()
	var heads string
	for _, l := range strings.Split(screen, "\n") {
		if strings.Contains(l, "For you  2") {
			heads = l
		}
	}
	for _, w := range []string{"For you  2", "In progress  2", "Next  2", "Ideas  1"} {
		if !strings.Contains(heads, w) {
			t.Fatalf("%q not on the headings' line %q:\n%s", w, heads, screen)
		}
	}
	for _, w := range []string{"decide " + GlyphNone + " you", "Decide: notifications order", GlyphIdle + " sentinel", "between agents"} {
		if !strings.Contains(screen, w) {
			t.Errorf("%q missing:\n%s", w, screen)
		}
	}
	if m := kanbanShell(t, 129, 40); m.lists.work.kanban != nil {
		t.Fatal("a Kanban at 129 columns")
	}
}

// A title longer than its column is cut with … on its own line, never
// wrapped and never past the column.
func TestWorkKanbanTruncates(t *testing.T) {
	pinLocal(t)
	st := workShared()
	st.plan.rows[2].Task.Title = "Decide: " + strings.Repeat("a very long title ", 6)
	m := keys(t, shellOf(t, st, 130, 40, nil), '3')
	k := m.lists.work.kanban
	ix, iy, iw, ih := m.lists.work.inner()
	r := kanbanGrid(4, ix, iy, iw, ih)[0]
	found := false
	for _, l := range strings.Split(m.grid().plain(), "\n") {
		if r.x+r.w > len([]rune(l)) {
			continue
		}
		cell := string([]rune(l)[r.x : r.x+r.w])
		if strings.Contains(cell, "a very long") {
			found = true
			if !strings.HasSuffix(strings.TrimRight(cell, " "), GlyphEllipsis) {
				t.Fatalf("not cut with …: %q", cell)
			}
		}
	}
	if !found || k == nil {
		t.Fatal("the long title is not drawn")
	}
}

// ←→ move between columns at the same rank; past the last or first column,
// or with no card selected, they step the tabs; the footer says column only
// on a card.
func TestWorkKanbanArrows(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.Select(m.work.You[0].Key)
	var got []string
	for i := 0; i < 3; i++ {
		m = keys(t, m, kRight)
		got = append(got, m.lists.work.Selected())
	}
	want := []string{"agent:coder", "plan:t8", "idea:2026-11-03-deepseek-superagent-ideas"}
	if strings.Join(got, " ") != strings.Join(want, " ") || m.ui.focus != PanelWork {
		t.Fatalf("→: %q (focus %v), want %q", got, m.ui.focus, want)
	}
	if foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint).text(); !strings.Contains(foot, GlyphArrowL+GlyphArrowR+" column") {
		t.Fatalf("footer %q", foot)
	}
	if r := keys(t, m, kRight); r.ui.focus != PanelActivity { // past the last column: the next tab, wrapping
		t.Fatalf("→ past Ideas: focus %v", r.ui.focus)
	}
	m = keys(t, m, kLeft)
	if m.lists.work.Selected() != want[1] || m.ui.focus != PanelWork {
		t.Fatalf("←: %q focus %v", m.lists.work.Selected(), m.ui.focus)
	}
	m.lists.work.Select(m.work.You[1].Key) // rank 1: the next column's rank 1
	m = keys(t, m, kRight)
	if m.lists.work.Selected() != "agent:foureyes" {
		t.Fatalf("rank kept: %q", m.lists.work.Selected())
	}
	m.lists.work.Select(m.work.You[0].Key)
	if r := keys(t, m, kLeft); r.ui.focus != PanelBoard {
		t.Fatalf("← past For you: focus %v", r.ui.focus)
	}
	m.lists.work.ClearSelection()
	if r := keys(t, m, kRight); r.ui.focus != PanelActivity {
		t.Fatalf("→ with no card: focus %v", r.ui.focus)
	}
	if foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint).text(); strings.Contains(foot, " column") {
		t.Fatalf("no card, footer %q", foot)
	}
}

// A click selects a card, a second click opens it, as on the rows.
func TestWorkKanbanClick(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	ix, iy, iw, ih := m.lists.work.inner()
	r := kanbanGrid(4, ix, iy, iw, ih)[2] // Next: its first card's lines are rows 1 and 2
	m = step(t, m, tea.MouseClickMsg{X: r.x + 2, Y: r.y + 2, Button: tea.MouseLeft})
	if m.lists.work.Selected() != "plan:t8" {
		t.Fatalf("click selected %q", m.lists.work.Selected())
	}
	m = step(t, m, tea.MouseClickMsg{X: r.x + 2, Y: r.y + 1, Button: tea.MouseLeft})
	if p := m.rd.top(); p == nil || p.kind != ovPlanTask || p.key != "t8" {
		t.Fatalf("second click: %+v", p)
	}
}

// A column with more cards than fit says how many more, scrolls to keep the
// selected card in view, and the frame draws no scrollbar of its own.
func TestWorkKanbanOverflow(t *testing.T) {
	pinLocal(t)
	st := workShared()
	for i := 0; i < 30; i++ { // more cards than the list has rows: a list would draw a scrollbar
		st.plan.rows = append(st.plan.rows, workPersonTask(i))
	}
	m := keys(t, shellOf(t, st, 130, 30, nil), '3')
	if len(m.lists.work.items) <= m.lists.work.rows() {
		t.Fatalf("fixture: %d items, %d rows", len(m.lists.work.items), m.lists.work.rows())
	}
	screen := m.grid().plain()
	if !strings.Contains(screen, "more") {
		t.Fatalf("no +N more:\n%s", screen)
	}
	last := m.work.You[len(m.work.You)-1]
	m.lists.work.Select(last.Key)
	m.refresh()
	if screen = m.grid().plain(); !strings.Contains(screen, last.Text) {
		t.Fatalf("the selected last card %q is not in view:\n%s", last.Text, screen)
	}
	r := m.lists.work.rect
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		if ch := []rune(strings.Split(screen, "\n")[y])[r.X+r.W-1]; ch != []rune(glyphVertical)[0] {
			t.Fatalf("row %d: the frame's right border reads %q (a scrollbar)", y, ch)
		}
	}
}

func workPersonTask(i int) plan.Row {
	id := "p" + strconv.Itoa(i)
	return plan.Row{Task: plan.Task{ID: id, Title: "Decide: question " + strconv.Itoa(i), Owner: plan.Person}, State: plan.Waiting}
}

// A column shows its heading and two lines a card; when they do not all fit,
// one line is kept for "+N more" (it never spills into the band below).
func TestKanbanFits(t *testing.T) {
	for _, c := range []struct{ h, cards, want int }{{11, 5, 5}, {11, 6, 4}, {10, 4, 4}, {10, 9, 4}, {2, 3, 0}} {
		if got := kanbanFits(c.h, c.cards); got != c.want {
			t.Errorf("h %d, %d cards: %d, want %d", c.h, c.cards, got, c.want)
		}
	}
}

// reviewer-codex: every card reads the same way, its facts on line 1 (the
// priority mark first, then its mark) and its title alone on line 2; a long
// high-priority title keeps its ↑ in a 31-cell column.
func TestWorkKanbanCardLines(t *testing.T) {
	st := workShared()
	long := "requests-plan redesign with a title far longer than any column"
	st.plan.rows[3].Task.Title = long
	st.requests[1].Priority = bus.PriorityHigh // requests-plan, the board task t8 names
	m := keys(t, workShellOf(t, st, 130, 40), '3')
	k := m.lists.work.kanban
	youMark, _ := workGlyph(m.work.You[0])
	marks := []string{youMark, GlyphWorking, GlyphIdle, GlyphIdea} // each column's first card's mark
	titles := []string{m.work.You[0].Text, "compose-at-file", long, "Three ideas from DeepSeek's Super-Agent prototype"}
	for c, col := range k.cols {
		card := col.cards[0]
		l1, l2 := card.lines[0].text(), card.lines[1].text()
		if !strings.HasPrefix(l1[len(card.lines[0][0].Text):], marks[c]+" ") {
			t.Errorf("column %d line 1 %q: its mark %q does not follow the priority slot", c, l1, marks[c])
		}
		if l2 != "  "+Preview(titles[c]) {
			t.Errorf("column %d line 2 %q, want the title %q", c, l2, titles[c])
		}
	}
	card := k.cols[2].cards[0]
	if card.key != "plan:t8" || !strings.HasPrefix(FitLine(card.lines[0], 31).text(), GlyphUp) {
		t.Fatalf("high priority lost in 31 cells: %q", FitLine(card.lines[0], 31).text())
	}
	if !strings.HasSuffix(strings.TrimRight(FitLine(card.lines[1], 31).text(), " "), GlyphEllipsis) {
		t.Fatalf("the long title is not cut: %q", FitLine(card.lines[1], 31).text())
	}
}

// reviewer-codex: an agent with a task and gates shows what it reviews and
// for whom, with the other gates' count, as the list does.
func TestWorkKanbanTaskAndGates(t *testing.T) {
	w := workModel{Team: []teamRow{{Agent: "coder", Word: "working", Plan: "t7",
		Task: &request{Task: "compose-at-file", Tracked: true, State: "accepted"},
		Reviewing: []teamGate{{Who: "master", Subject: "lot-1", Since: shellNow.Add(-5 * time.Minute)},
			{Who: "master", Subject: "lot-2", Since: shellNow.Add(-time.Minute)}}}}}
	card := workKanban(w, workDraw{now: shellNow}).cols[1].cards[0]
	l1, l2 := card.lines[0].text(), card.lines[1].text()
	if !strings.Contains(l1, "reviewing lot-1 for master 5m (+1)") || !strings.Contains(l1, "#t7") || l2 != "  compose-at-file" {
		t.Fatalf("task and gates: %q / %q", l1, l2)
	}
	w.Team[0].Task = nil
	card = workKanban(w, workDraw{now: shellNow}).cols[1].cards[0]
	if l2 := card.lines[1].text(); l2 != "  reviewing lot-1 for master 5m (+1)" {
		t.Fatalf("gates only: %q", l2)
	}
}

// foureyes (P3): a click on a column's heading selects nothing; the free
// agents' card is no selectable item; "+N more" never spills out of a band
// one row high.
func TestWorkKanbanHeadingsAndBands(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.ClearSelection()
	ix, iy, iw, ih := m.lists.work.inner()
	r := kanbanGrid(4, ix, iy, iw, ih)[2]
	if key := m.lists.work.kanban.cardAt(r.x+2, r.y, ix, iy, iw, ih, ""); key != "" {
		t.Fatalf("a heading click hits %q", key)
	}
	for _, it := range m.lists.work.items {
		if it.Key == "" && it.Selectable {
			t.Fatalf("a keyless card is selectable: %q", it.Line.text())
		}
	}
	cards := []kanbanCard{{key: "x"}, {key: "y"}}
	k := kanbanView{cols: []kanbanCol{{title: "a", cards: cards}, {title: "b"}, {title: "c"}, {title: "d", cards: cards}}} // the last band's would land on the frame
	g := newGrid(22, 6)
	k.draw(g, 1, 1, 20, 4, "") // four bands of one row each
	if out := g.plain(); strings.Contains(out, "more") {
		t.Fatalf("+N more drawn in a one-row band:\n%s", out)
	}
}

// foureyes: the priority mark leads line 1 in For you and In progress too.
func TestWorkKanbanPriorityEveryColumn(t *testing.T) {
	high := &request{Task: "x", Tracked: true, State: "accepted", Priority: bus.PriorityHigh}
	w := workModel{
		You:  []workItem{{Verb: verbAnswer, Key: "req:x", Who: "master", Text: "x", Req: high}},
		Team: []teamRow{{Agent: "coder", Word: "working", Task: high}},
		Next: []workItem{{Key: "req:y", Who: "coder", Text: "y", Req: high}},
	}
	k := workKanban(w, workDraw{now: shellNow})
	for c := 0; c < 3; c++ {
		if got := k.cols[c].cards[0].lines[0].text(); !strings.HasPrefix(got, GlyphUp) {
			t.Errorf("column %d: %q has no leading ↑", c, got)
		}
	}
}
