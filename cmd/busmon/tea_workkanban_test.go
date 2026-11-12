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

// ←→ move between columns at the same rank and wrap around them (the person,
// 2026-11-11): → on Ideas goes to For you, ← on For you to Ideas, and they
// never change tab on the Kanban; the tabs keep their own keys. With no card
// selected, → selects the first column's first card, ← the last column's.
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
	if m = keys(t, m, kRight); m.ui.focus != PanelWork || m.lists.work.Selected() != m.work.You[0].Key { // past Ideas: For you
		t.Fatalf("→ on Ideas: focus %v selected %q, want For you's %q", m.ui.focus, m.lists.work.Selected(), m.work.You[0].Key)
	}
	m = keys(t, m, kLeft, kLeft) // back over the edge to Ideas, then Next
	if m.lists.work.Selected() != want[1] || m.ui.focus != PanelWork {
		t.Fatalf("←: %q focus %v", m.lists.work.Selected(), m.ui.focus)
	}
	m.lists.work.Select(m.work.You[1].Key) // rank 1: the next column's rank 1
	m = keys(t, m, kRight)
	if m.lists.work.Selected() != "agent:foureyes" {
		t.Fatalf("rank kept: %q", m.lists.work.Selected())
	}
	m.lists.work.Select(m.work.You[0].Key)
	if m = keys(t, m, kLeft); m.ui.focus != PanelWork || m.lists.work.Selected() != want[2] { // past For you: Ideas
		t.Fatalf("← on For you: focus %v selected %q, want Ideas' %q", m.ui.focus, m.lists.work.Selected(), want[2])
	}
	m.lists.work.ClearSelection()
	if m = keys(t, m, kRight); m.ui.focus != PanelWork || m.lists.work.Selected() != m.work.You[0].Key {
		t.Fatalf("→ with no card: focus %v selected %q", m.ui.focus, m.lists.work.Selected())
	}
	m.lists.work.ClearSelection()
	if m = keys(t, m, kLeft); m.ui.focus != PanelWork || m.lists.work.Selected() != want[2] {
		t.Fatalf("← with no card: focus %v selected %q", m.ui.focus, m.lists.work.Selected())
	}
	if foot := footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint).text(); !strings.Contains(foot, GlyphArrowL+GlyphArrowR+" column") {
		t.Fatalf("no card, footer %q", foot)
	}
	if r := keys(t, m, '1'); r.ui.focus != PanelActivity { // the tabs keep their own keys
		t.Fatalf("1 on the Kanban: focus %v", r.ui.focus)
	}
}

// The steps alone: a wrap at both edges, empty columns skipped, a lone
// non-empty column keeping its card, an empty Kanban selecting nothing.
func TestKanbanStepWraps(t *testing.T) {
	card := func(key string) kanbanCard { return kanbanCard{key: key, lines: []Line{{{Text: key}}}} }
	k := kanbanView{cols: []kanbanCol{{cards: []kanbanCard{card("a0"), card("a1")}}, {}, {cards: []kanbanCard{card("c0")}}, {}}}
	for _, c := range []struct {
		sel  string
		next bool
		want string
	}{
		{"a1", true, "c0"}, {"c0", true, "a0"}, {"a0", false, "c0"}, {"c0", false, "a0"},
		{"", true, "a0"}, {"", false, "c0"}, {"gone", true, "a0"},
	} {
		if got := k.step(c.sel, c.next); got != c.want {
			t.Errorf("step(%q, next %v) = %q, want %q", c.sel, c.next, got, c.want)
		}
	}
	lone := kanbanView{cols: []kanbanCol{{}, {cards: []kanbanCard{card("b0"), card("b1")}}, {}}}
	if got := lone.step("b1", true); got != "b1" {
		t.Errorf("a lone column: %q, want b1 kept", got)
	}
	if got := (&kanbanView{cols: []kanbanCol{{}, {}}}).step("", true); got != "" {
		t.Errorf("an empty Kanban: %q", got)
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

// A column shows its heading and each card's lines (two, three with a
// gate); when they do not all fit, one line is kept for "+N more" (it never
// spills into the band below).
func TestKanbanFits(t *testing.T) {
	two, three := kanbanCard{lines: make([]Line, 2)}, kanbanCard{lines: make([]Line, 3)}
	rep := func(c kanbanCard, n int) []kanbanCard {
		out := make([]kanbanCard, n)
		for i := range out {
			out[i] = c
		}
		return out
	}
	for _, c := range []struct {
		h     int
		cards []kanbanCard
		want  int
	}{
		{11, rep(two, 5), 5}, {11, rep(two, 6), 4}, {10, rep(two, 4), 4}, {10, rep(two, 9), 4}, {2, rep(two, 3), 0},
		{8, []kanbanCard{three, two, two}, 3}, {8, []kanbanCard{three, two, two, two}, 2}, {3, []kanbanCard{three}, 0},
	} {
		if got := kanbanFits(c.h, c.cards); got != c.want {
			t.Errorf("h %d, %d cards: %d, want %d", c.h, len(c.cards), got, c.want)
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

// reviewer-codex: an agent with a task and gates shows what it reviews,
// for whom, since when and how many more, visible at 31 cells: with a task
// on a third line (the task stays the title), without one on its two lines.
func TestWorkKanbanTaskAndGates(t *testing.T) {
	w := workModel{Team: []teamRow{{Agent: "coder", Word: "working", Plan: "t7",
		Task: &request{Task: "compose-at-file", Tracked: true, State: "accepted"},
		Reviewing: []teamGate{{Who: "master", Subject: "lot-1", Since: shellNow.Add(-5 * time.Minute)},
			{Who: "master", Subject: "lot-2", Since: shellNow.Add(-time.Minute)}}}}}
	fit := func(l Line) string { return FitLine(l, 31).text() }
	card := workKanban(w, workDraw{now: shellNow}).cols[1].cards[0]
	if len(card.lines) != 3 || !strings.Contains(fit(card.lines[0]), "#t7") || fit(card.lines[1]) != FitLine(Line{{Text: "  compose-at-file"}}, 31).text() ||
		!strings.Contains(fit(card.lines[2]), "for master 5m (+1)") || !strings.Contains(fit(card.lines[2]), "lot-1") {
		t.Fatalf("task and gates at 31 cells: %q", []string{fit(card.lines[0]), fit(card.lines[1]), fit(card.lines[2])})
	}
	w.Team[0].Task = nil
	card = workKanban(w, workDraw{now: shellNow}).cols[1].cards[0]
	if len(card.lines) != 2 || !strings.Contains(fit(card.lines[0]), "for master 5m (+1)") || !strings.Contains(fit(card.lines[1]), "reviewing lot-1") {
		t.Fatalf("gates only at 31 cells: %q", []string{fit(card.lines[0]), fit(card.lines[1])})
	}
}

// A three-line card takes its three rows: a click on its third line selects
// it, and the card under it starts one row lower.
func TestWorkKanbanThreeLineCardClicks(t *testing.T) {
	gate := []teamGate{{Who: "master", Subject: "lot-1", Since: shellNow.Add(-5 * time.Minute)}}
	w := workModel{Team: []teamRow{
		{Agent: "coder", Word: "working", Task: &request{Task: "a", Tracked: true, State: "accepted"}, Reviewing: gate},
		{Agent: "docs", Word: "working", Message: "b"}}}
	k := workKanban(w, workDraw{now: shellNow})
	r := kanbanGrid(4, 0, 0, 128, 20)[1]
	for row, want := range map[int]string{r.y + 3: "agent:coder", r.y + 4: "agent:docs", r.y + 5: "agent:docs"} {
		if got := k.cardAt(r.x+1, row, 0, 0, 128, 20, ""); got != want {
			t.Errorf("row %d: %q, want %q", row-r.y, got, want)
		}
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
	k.draw(g, 1, 1, 20, 4, "", 0) // four bands of one row each
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

// master: on the rendered screen, at 31 and 32 cells a column, the card of an
// agent with a task and gates keeps for whom, the age and (+N) whole; only a
// long subject is cut.
func TestWorkKanbanGateRenderedAt31And32(t *testing.T) {
	w := workModel{Team: []teamRow{{Agent: "coder", Word: "working", Plan: "t7",
		Task: &request{Task: "compose-at-file", Tracked: true, State: "accepted"},
		Reviewing: []teamGate{{Who: "master", Subject: "a review subject far too long for one column", Since: shellNow.Add(-5 * time.Minute)},
			{Who: "master", Subject: "lot-2", Since: shellNow.Add(-time.Minute)}}}}}
	k := workKanban(w, workDraw{now: shellNow})
	for _, inner := range []int{128, 131} { // 31 and 32 cells a column
		g := newGrid(inner+2, 12)
		k.draw(g, 1, 1, inner, 10, "", 0)
		r := kanbanGrid(4, 1, 1, inner, 10)[1]
		var rows []string
		for _, l := range strings.Split(g.plain(), "\n") {
			if rs := []rune(l); len(rs) >= r.x+r.w {
				rows = append(rows, string(rs[r.x:r.x+r.w]))
			}
		}
		col := strings.Join(rows, "\n")
		if want := map[int]int{128: 31, 131: 32}[inner]; r.w != want {
			t.Fatalf("column width %d, want %d", r.w, want)
		}
		for _, want := range []string{"#t7", "compose-at-file", GlyphGate + " for master 5m (+1) " + GlyphNone + " a rev"} {
			if !strings.Contains(col, want) {
				t.Errorf("%d cells: %q not on screen:\n%s", r.w, want, col)
			}
		}
		if !strings.Contains(col, GlyphEllipsis) {
			t.Errorf("%d cells: the long subject is not cut:\n%s", r.w, col)
		}
	}
}

// foureyes (step 3): a selected card taller than its band. The scroll stops
// at the card: it never runs past it, and the draw neither panics nor
// spills out of the band.
func TestKanbanScrollStopsAtATallCard(t *testing.T) {
	two := func(k string) kanbanCard { return kanbanCard{key: k, lines: []Line{{{Text: k}}, {{Text: k}}}} }
	tall := kanbanCard{key: "gate", lines: []Line{{{Text: "gate"}}, {{Text: "title"}}, {{Text: "◆ for master"}}}}
	k := kanbanView{cols: []kanbanCol{{title: "In progress", cards: []kanbanCard{two("a"), two("b"), tall}}}}
	rects := []kanbanRect{{x: 1, y: 1, w: 30, h: 3}} // heading, one row, "+N more": no room for three lines
	col, rank, first := k.place("gate", rects)
	if col != 0 || rank != 2 || first[0] != rank {
		t.Fatalf("place: col %d rank %d first %v, want the scroll to stop at the selected card (2)", col, rank, first)
	}
	g := newGrid(32, 5)
	k.draw(g, 1, 1, 30, 3, "gate", 0)
	if out := g.plain(); strings.Contains(strings.Split(out, "\n")[4], "gate") {
		t.Fatalf("drawn past the band:\n%s", out)
	}
}

// The person (2026-11-11): a discreet rule between the columns, the same
// length for every column, inset one row from the band's top and bottom so
// it touches neither the heading's row nor the frame, in the rules' own
// colour (RoleLine). The same at 130 columns and wider, and per band when
// the columns stack.
func TestKanbanColumnRules(t *testing.T) {
	cards := []kanbanCard{{key: "a", lines: []Line{{{Text: "a"}}, {{Text: "a"}}}}}
	k := kanbanView{cols: []kanbanCol{{title: "For you", cards: cards}, {title: "In progress"}, {title: "Next", cards: cards}, {title: "Ideas"}}}
	for _, c := range []struct {
		name  string
		w, h  int
		bands int
	}{{"130", 128, 20, 1}, {"200", 198, 30, 1}, {"stacked", 60, 20, 2}} {
		g := newGrid(c.w+2, c.h+2)
		k.draw(g, 1, 1, c.w, c.h, "", 0)
		rects := kanbanGrid(4, 1, 1, c.w, c.h)
		seps := 0
		for i, r := range rects {
			if i%(4/c.bands) == 0 { // a band's first column has no rule on its left
				continue
			}
			seps++
			x := r.x - 1
			for y := r.y; y < r.y+r.h; y++ {
				cell := g.at(x, y)
				inside := y > r.y && y < r.y+r.h-1
				if inside && (cell.text != glyphVertical || cell.st.Role != RoleLine) {
					t.Errorf("%s: column %d row %d: %q role %v, want the rule", c.name, i, y-r.y, cell.text, cell.st.Role)
				}
				if !inside && cell.text != " " {
					t.Errorf("%s: column %d row %d: %q, want the inset left blank", c.name, i, y-r.y, cell.text)
				}
			}
		}
		if want := 4 - c.bands; seps != want {
			t.Errorf("%s: %d rules, want %d", c.name, seps, want)
		}
	}
}
