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

// The Kanban's focus levels (busmon-work-focus, the person's brief of
// 2026-11-13): it is never a trap. On the tab, ←→ step the tabs; ↓ goes to
// the column selector, where ←→ choose a column and its heading is drawn
// selected; ↓ goes into the column, whose ↑↓ never leave it; Esc climbs one
// level at a time, back to the tab.
func TestWorkKanbanFocusLevels(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	if m.ui.workLevel != workAtTabs {
		t.Fatalf("3 lands at level %v, want the tab", m.ui.workLevel)
	}
	if r := keys(t, m, kRight); r.ui.focus != lowerTabs[0] { // the tab: → wraps to Activity
		t.Fatalf("→ on Work's tab: focus %v", r.ui.focus)
	}
	if r := keys(t, m, kLeft); r.ui.focus != PanelBoard {
		t.Fatalf("← on Work's tab: focus %v", r.ui.focus)
	}
	foot := func(m Model) string {
		return footerHintsOn(m.mode(), m.ui.focus, m.layout.Tier, 400, m.pageOffers, m.pageHint).text()
	}
	if strings.Contains(foot(m), GlyphArrowL+GlyphArrowR+" column") {
		t.Fatalf("the tab's footer offers columns: %q", foot(m))
	}
	heading := func(m Model, c int) string {
		ix, iy, iw, ih := m.lists.work.inner()
		r := kanbanGrid(4, ix, iy, iw, ih)[c]
		return string([]rune(strings.Split(m.grid().plain(), "\n")[r.y])[r.x : r.x+r.w])
	}

	// Down: the column selector, on For you (nothing selected yet).
	m.lists.work.ClearSelection()
	m = keys(t, m, kDown)
	if m.ui.workLevel != workAtCols || m.ui.workCol != 0 || !strings.Contains(heading(m, 0), GlyphSelect+" For you") {
		t.Fatalf("↓: level %v col %d heading %q", m.ui.workLevel, m.ui.workCol, heading(m, 0))
	}
	if !strings.Contains(foot(m), GlyphArrowL+GlyphArrowR+" column") {
		t.Fatalf("the selector's footer %q", foot(m))
	}
	m = keys(t, m, kRight, kRight) // Next
	if m.ui.workCol != 2 || m.ui.focus != PanelWork || !strings.Contains(heading(m, 2), GlyphSelect+" Next") || strings.Contains(heading(m, 0), GlyphSelect) {
		t.Fatalf("→→: col %d focus %v", m.ui.workCol, m.ui.focus)
	}
	m = keys(t, m, kLeft, kLeft, kLeft) // wraps: Ideas
	if m.ui.workCol != 3 {
		t.Fatalf("←←← wraps: col %d", m.ui.workCol)
	}
	m = keys(t, m, kRight, kRight, kRight) // Next again

	// Into the column: its first card; ↓↑ stay in it.
	m = keys(t, m, kDown)
	if m.ui.workLevel != workAtCards || m.lists.work.Selected() != m.work.Next[0].Key {
		t.Fatalf("↓ into Next: level %v selected %q", m.ui.workLevel, m.lists.work.Selected())
	}
	m = keys(t, m, kDown, kDown, kDown) // past the last card: it stays
	if m.lists.work.Selected() != m.work.Next[len(m.work.Next)-1].Key {
		t.Fatalf("↓ past Next's end: %q, want its last %q", m.lists.work.Selected(), m.work.Next[len(m.work.Next)-1].Key)
	}
	m = keys(t, m, kUp, kUp, kUp, kUp)
	if m.lists.work.Selected() != m.work.Next[0].Key {
		t.Fatalf("↑ past Next's start: %q", m.lists.work.Selected())
	}
	// ←→ in a column: the next column's card at the same rank.
	if m = keys(t, m, kRight); m.lists.work.Selected() != "idea:2026-11-03-deepseek-superagent-ideas" || m.ui.workCol != 3 {
		t.Fatalf("→ from Next: %q col %d", m.lists.work.Selected(), m.ui.workCol)
	}
	m = keys(t, m, kLeft)

	// Esc: the selector, on the card's column; again: the tab, whose ← steps the tabs.
	m = keys(t, m, kEsc)
	if m.ui.workLevel != workAtCols || m.ui.workCol != 2 || m.lists.work.Selected() != m.work.Next[0].Key {
		t.Fatalf("esc: level %v col %d selected %q", m.ui.workLevel, m.ui.workCol, m.lists.work.Selected())
	}
	m = keys(t, m, kDown) // back in, on the card it kept
	if m.ui.workLevel != workAtCards || m.lists.work.Selected() != m.work.Next[0].Key {
		t.Fatalf("↓ again: level %v selected %q", m.ui.workLevel, m.lists.work.Selected())
	}
	m = keys(t, m, kEsc, kEsc)
	if m.ui.workLevel != workAtTabs || m.ui.focus != PanelWork {
		t.Fatalf("esc esc: level %v focus %v", m.ui.workLevel, m.ui.focus)
	}
	if m = keys(t, m, kLeft); m.ui.focus != PanelBoard {
		t.Fatalf("← after esc esc: focus %v", m.ui.focus)
	}
	// ↑ from the selector climbs too; the digits work at every level.
	m = keys(t, m, '3', kDown, kUp)
	if m.ui.workLevel != workAtTabs {
		t.Fatalf("↑ on the selector: level %v", m.ui.workLevel)
	}
	if r := keys(t, m, kDown, kDown, '1'); r.ui.focus != PanelActivity {
		t.Fatalf("1 inside a column: focus %v", r.ui.focus)
	}
}

// The level shows: the selected card is drawn so only inside its column,
// the tab's pill is faded once the focus is below it.
func TestWorkKanbanFocusShows(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.Select(m.work.Next[0].Key)
	m.refresh()
	pill := func(m Model) bool { // the Work tab's pill is not faded
		for _, s := range m.lists.work.title {
			if s.Tab && strings.Contains(s.Text, "Work") {
				return s.Fade == 0
			}
		}
		t.Fatal("no Work tab in the title")
		return false
	}
	if !pill(m) || m.lists.work.kanban.level != workAtTabs {
		t.Fatal("at the tab: the pill faded")
	}
	m = keys(t, m, kDown)
	if pill(m) || m.lists.work.kanban.level != workAtCols || m.lists.work.kanban.col != 2 {
		t.Fatalf("at the selector: pill %v level %v col %d", pill(m), m.lists.work.kanban.level, m.lists.work.kanban.col)
	}
	m = keys(t, m, kDown)
	if pill(m) || m.lists.work.kanban.level != workAtCards {
		t.Fatal("in the column: the pill full")
	}
}

// The selected card is drawn selected only inside its column: at the tab and
// at the selector the selection is kept, not shown.
func TestWorkKanbanCardDrawnOnlyInsideItsColumn(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.Select("plan:t8") // Next's first card: its second line is the heading's row + 2
	ix, iy, iw, ih := m.lists.work.inner()
	r := kanbanGrid(4, ix, iy, iw, ih)[2]
	selected := func(m Model) bool { return m.grid().at(r.x+3, r.y+2).st.Selected }
	for lv, want := range map[workLevel]bool{workAtTabs: false, workAtCols: false, workAtCards: true} {
		m.ui.workLevel, m.ui.workCol = lv, 2
		m.refresh()
		if got := selected(m); got != want {
			t.Errorf("level %v: card drawn selected %v, want %v", lv, got, want)
		}
	}
}

// → from Board lands on Work's tab, so the next → steps on to Activity, the
// focus never caught in the columns (the trap the person met).
func TestWorkKanbanReachedByArrowIsItsTab(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m = keys(t, m, kDown, kDown, '2') // inside a column, then Board
	if m = keys(t, m, kRight); m.ui.focus != PanelWork || m.ui.workLevel != workAtTabs {
		t.Fatalf("→ from Board: focus %v level %v", m.ui.focus, m.ui.workLevel)
	}
	if m = keys(t, m, kRight); m.ui.focus != PanelActivity {
		t.Fatalf("→ again: focus %v", m.ui.focus)
	}
	m = keys(t, m, '3', kDown, kDown, kTab) // Tab cycles the panels: back on Work by Tab, its tab too
	for i := 0; i < 8 && m.ui.focus != PanelWork; i++ {
		m = keys(t, m, kTab)
	}
	if m.ui.focus != PanelWork || m.ui.workLevel != workAtTabs {
		t.Fatalf("Work by Tab: focus %v level %v", m.ui.focus, m.ui.workLevel)
	}
}

// The selector goes into its own column, not the selection's: with Next's
// card selected and For you chosen, ↓ takes For you's first card.
func TestWorkKanbanSelectorEntersItsColumn(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.Select("plan:t8")
	m = keys(t, m, kDown, kLeft, kLeft) // the selector on Next, then For you
	if m.ui.workCol != 0 {
		t.Fatalf("col %d", m.ui.workCol)
	}
	if m = keys(t, m, kDown); m.lists.work.Selected() != m.work.You[0].Key || m.ui.workLevel != workAtCards {
		t.Fatalf("↓ into For you: %q level %v", m.lists.work.Selected(), m.ui.workLevel)
	}
}

// A click on a card puts the focus in its column (Esc then on that column's
// heading); a click on the tab bar at the tab.
func TestWorkKanbanClickLevels(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	ix, iy, iw, ih := m.lists.work.inner()
	r := kanbanGrid(4, ix, iy, iw, ih)[2]
	m = step(t, m, tea.MouseClickMsg{X: r.x + 2, Y: r.y + 2, Button: tea.MouseLeft})
	if m.ui.workLevel != workAtCards || m.lists.work.Selected() != "plan:t8" {
		t.Fatalf("card click: level %v selected %q", m.ui.workLevel, m.lists.work.Selected())
	}
	if e := keys(t, m, kEsc); e.ui.workLevel != workAtCols || e.ui.workCol != 2 {
		t.Fatalf("esc after the click: level %v col %d, want Next's heading", e.ui.workLevel, e.ui.workCol)
	}
	fr := m.layout.rect(PanelWork)
	m = step(t, m, tea.MouseClickMsg{X: fr.X + 4, Y: fr.Y, Button: tea.MouseLeft})
	if m.ui.workLevel != workAtTabs {
		t.Fatalf("tab bar click: level %v focus %v", m.ui.workLevel, m.ui.focus)
	}
}

// The column helpers alone.
func TestKanbanColumnHelpers(t *testing.T) {
	card := func(key string) kanbanCard { return kanbanCard{key: key, lines: []Line{{{Text: key}}}} }
	k := kanbanView{cols: []kanbanCol{{title: "For you  2", cards: []kanbanCard{card("a0"), {}, card("a1")}}, {title: "Next  0"}}}
	if c, r := k.cardPlace("a1"); c != 0 || r != 1 {
		t.Fatalf("place a1: %d %d", c, r)
	}
	if c, r := k.cardPlace("nope"); c != -1 || r != -1 {
		t.Fatalf("place nope: %d %d", c, r)
	}
	if c, _ := k.cardPlace(""); c != -1 {
		t.Fatal("an empty key placed")
	}
	if k.cardOf(0, 1) != "a1" || k.cardOf(0, 2) != "" || k.cardOf(1, 0) != "" || k.cardOf(5, 0) != "" || k.cardCount(0) != 2 {
		t.Fatal("cardOf / cardCount")
	}
	if k.colName(0) != "For you" || k.colName(1) != "Next" {
		t.Fatalf("names %q %q", k.colName(0), k.colName(1))
	}
}

// An empty column: the selector stays on it and says so.
func TestWorkKanbanEmptyColumn(t *testing.T) {
	pinLocal(t)
	m := kanbanShell(t, 130, 40)
	m.lists.work.kanban.cols[1].cards = nil
	m.ui.workLevel, m.ui.workCol = workAtCols, 1
	if !m.workKanbanKey(ActDown) || m.ui.workLevel != workAtCols || !strings.Contains(m.ui.flash, "nothing in In progress") {
		t.Fatalf("level %v flash %q", m.ui.workLevel, m.ui.flash)
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
