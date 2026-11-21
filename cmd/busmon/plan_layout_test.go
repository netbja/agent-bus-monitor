package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/plan"
)

// barRoles are the roles of the full cells of the bar on line i, in order.
func barRoles(v planBoardView, i int) []Role {
	var cells []Role
	for _, s := range v.lines[i] {
		for range strings.Count(s.Text, GlyphBarFull) {
			cells = append(cells, s.Role)
		}
	}
	return cells
}

// wordRole is the role of the segment holding word on line i, -1 when none.
func wordRole(v planBoardView, i int, word string) Role {
	for _, s := range v.lines[i] {
		if strings.Contains(s.Text, word) {
			return s.Role
		}
	}
	return -1
}

// views-redesign §4.2: a group's bar and state word are in its state's
// colour: orange waits for the person, red blocked, green every open task
// approved, blue in progress (or in review), the text colour not started.
func TestPlanGroupStateColour(t *testing.T) {
	approved := func(id string) plan.Row {
		r := inGroup(boardRow(id, plan.InReview, "coder"), "g")
		r.Verdict = plan.Approved
		return r
	}
	for _, c := range []struct {
		name string
		rows []plan.Row
		word string
		role Role
	}{
		{"waits", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), inGroup(boardRow("w", plan.Waiting, plan.Person), "g"), inGroup(boardRow("b", plan.Blocked, "coder"), "g")}, "waits for you", RoleAttention},
		{"blocked", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), inGroup(boardRow("b", plan.Blocked, "coder"), "g"), inGroup(boardRow("i", plan.InProgress, "coder"), "g")}, "blocked", RoleError},
		{"approved", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), approved("x"), approved("y")}, "approved", RoleOK},
		{"in progress", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), approved("x"), inGroup(boardRow("u", plan.Upcoming, "coder"), "g")}, "in progress", RoleAccent},
		{"in review", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), inGroup(boardRow("r", plan.InReview, "coder"), "g")}, "in progress", RoleAccent},
		{"not started", []plan.Row{inGroup(boardRow("a", plan.Done, "coder"), "g"), inGroup(boardRow("u", plan.Upcoming, "coder"), "g")}, "not started", RoleText},
	} {
		p := boardPlan(c.rows...)
		p.file.Groups = []plan.Group{{ID: "g", Title: "G"}}
		v := planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
		_, gi := lineWith(v, GlyphDown+" G ")
		if gi < 0 {
			t.Fatalf("%s: no group line:\n%s", c.name, v.plain)
		}
		if got := wordRole(v, gi, c.word); got != c.role {
			t.Errorf("%s: word %q role %v, want %v: %q", c.name, c.word, got, c.role, v.lines[gi].text())
		}
		if cells := barRoles(v, gi); len(cells) == 0 || cells[0] != c.role {
			t.Errorf("%s: bar %v, want %v", c.name, cells, c.role)
		}
	}
}

// §4.2: the groups with open tasks are expanded, with no blank line, and
// every finished group folds into one line, an item d or Enter opens.
func TestPlanFinishedGroupsFoldIntoOneLine(t *testing.T) {
	p := boardPlan(
		inGroup(boardRow("h1", plan.InProgress, "coder"), "herdr"),
		inGroup(boardRow("w1", plan.Done, "coder"), "work"),
		inGroup(boardRow("q1", plan.Done, "coder"), "quota"),
		inGroup(boardRow("q2", plan.Done, "coder"), "quota"),
	)
	p.file.Groups = []plan.Group{{ID: "herdr", Title: "Leave herdr"}, {ID: "work", Title: "Work tab"}, {ID: "quota", Title: "Quota"}}
	v := planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	for i, l := range v.lines {
		if l == nil {
			t.Fatalf("a blank line at %d:\n%s", i, v.plain)
		}
	}
	line, fi := lineWith(v, GlyphSelect+" 2 groups done "+GlyphNone+" 3 tasks")
	if fi < 0 || !strings.Contains(line, "(d shows)") || strings.Contains(v.plain, "Work tab") || strings.Contains(v.plain, "Quota") {
		t.Fatalf("finished groups not folded:\n%s", v.plain)
	}
	if last := v.items[len(v.items)-1]; last.fold != "done" || last.line != fi {
		t.Fatalf("the fold is no item: %+v", v.items)
	}
	shown := planBoard(p, 0, true, shellNow, 120, TierWide, false, nil)
	if _, wi := lineWith(shown, GlyphDown+" Work tab"); wi < 0 || itemLine(shown, "w1") != wi+1 {
		t.Fatalf("d shows each finished group with its rows:\n%s", shown.plain)
	}
}

// §4.2: one header line, the plan, a bar, done/total, what is left and what
// is for the person (what waits for them and their own open tasks), then a
// rule.
func TestPlanHeaderLine(t *testing.T) {
	p := boardPlan(boardRow("a", plan.Done, "coder"), boardRow("w", plan.Waiting, "coder"), boardRow("mine", plan.Upcoming, plan.Person), boardRow("u", plan.Upcoming, "coder"))
	p.file.Plan = "v0.31"
	v := planBoard(p, 0, false, shellNow, 100, TierWide, false, nil)
	head := strings.TrimRight(v.lines[0].text(), " ")
	if !strings.HasPrefix(head, "v0.31  "+GlyphBarFull) || !strings.Contains(head, GlyphBarEmpty) || !strings.HasSuffix(head, "1/4   3 left "+GlyphNone+" 2 for you") || Width(head) > 100 {
		t.Fatalf("header %q", head)
	}
	if strings.Trim(v.lines[1].text(), glyphHorizontal) != "" {
		t.Fatalf("no rule under the header: %q", v.lines[1].text())
	}
	p.partial = true
	if head := planBoard(p, 0, false, shellNow, 140, TierWide, false, nil).lines[0].text(); !strings.Contains(head, "1/4?") || !strings.Contains(head, "a read failed") {
		t.Fatalf("partial %q", head)
	}
}

// §4.2: a row's columns are aligned (title, owner, state, age), its state
// word in its colour; a task waiting on another reads "after <id>" with
// the pause mark; the title takes the width the others leave.
func TestPlanRowColumns(t *testing.T) {
	appr := inGroup(boardRow("readme", plan.InReview, "docs"), "g")
	appr.Verdict, appr.Task.Title, appr.LastFact = plan.Approved, "Rewrite README and operations guide", shellNow.Add(-10*time.Hour).UnixMilli()
	mig := inGroup(boardRow("migrate", plan.Upcoming, plan.Person), "g")
	mig.WaitingOn, mig.Task.Title = []string{"readme"}, "Migrate the other projects"
	p := boardPlan(appr, mig)
	p.file.Groups = []plan.Group{{ID: "g", Title: "Docs and release"}}
	v := planBoard(p, -1, false, shellNow, 100, TierWide, false, nil)
	a, b := v.lines[itemLine(v, "readme")].text(), v.lines[itemLine(v, "migrate")].text()
	if !strings.HasPrefix(a, "  "+GlyphDone+" Rewrite README") || !strings.HasPrefix(b, "  "+GlyphPause+" Migrate the other") {
		t.Fatalf("rows:\n%s\n%s", a, b)
	}
	if strings.Index(a, "docs") != strings.Index(b, "person") || strings.Index(a, "approved") != strings.Index(b, "after readme") || !strings.Contains(a, "10h") {
		t.Fatalf("columns not aligned:\n%s\n%s", a, b)
	}
	if r := wordRole(v, itemLine(v, "readme"), "approved"); r != RoleOK {
		t.Errorf("approved in %v", r)
	}
	if r := wordRole(v, itemLine(v, "migrate"), "after readme"); r != RoleMuted {
		t.Errorf("after in %v", r)
	}
	if w, role := planStateWord(boardRow("x", plan.Waiting, plan.Person)); w != "decide" || role != RoleAttention { // the person's own decision: orange
		t.Errorf("the person's decision: %q %v", w, role)
	}
	for word, role := range map[plan.State]Role{plan.InProgress: RoleAccent, plan.Blocked: RoleError, plan.Done: RoleMuted, plan.Waiting: RoleAttention} {
		if _, got := planStateWord(boardRow("x", word, "coder")); got != role {
			t.Errorf("%v: %v, want %v", word, got, role)
		}
	}
}

// factsText is the facts as "key value" lines.
func factsText(fs []planFact) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.key + " " + f.val + "\n")
	}
	return b.String()
}

// §4.2: the strip is a rule, then the task's facts on one line (only those
// it has), then what the person may do and the keys.
func TestPlanStrip(t *testing.T) {
	r := boardRow("readme", plan.InReview, "docs")
	r.Verdict, r.PR = plan.Approved, 351
	m := boardRow("migrate", plan.Upcoming, plan.Person, "readme")
	m.Task.After = []string{"readme"}
	p := boardPlan(r, m)
	d := planStrip(p, r, shellNow, 120)
	if len(d) != planDetailRows || strings.Trim(d[0].text(), glyphHorizontal) != "" {
		t.Fatalf("strip %q", linesText(d))
	}
	sep := " " + GlyphNone + " "
	if got := strings.TrimRight(d[1].text(), " "); got != "readme"+sep+"in review"+sep+"docs"+sep+"approved"+sep+"PR 351"+sep+"unblocks migrate" {
		t.Fatalf("facts %q", got)
	}
	if got := strings.TrimRight(d[2].text(), " "); got != "for you: nothing        enter: task page   + - priority" {
		t.Fatalf("keys %q", got)
	}
	for _, l := range planStrip(p, r, shellNow, 40) {
		if Width(l.text()) > 40 {
			t.Fatalf("wider than 40: %q", l.text())
		}
	}
}

// detailAt is the text of the frame's three rows above its bottom border.
func detailAt(screen string, r Rect) []string {
	lines := strings.Split(screen, "\n")
	var out []string
	for y := r.Y + r.H - 1 - planDetailRows; y < r.Y+r.H-1; y++ {
		out = append(out, strings.TrimSpace(string([]rune(lines[y])[r.X+1:r.X+r.W-1])))
	}
	return out
}

// §4.2, the tab: the strip at the frame's bottom, following j, at 80
// columns too; the list takes the frame's width.
func TestPlanTabDetailBelowTheList(t *testing.T) {
	for _, w := range []int{160, 80} {
		m := keys(t, planShellOf(t, chantierPlan(), w, 44), '4')
		r := m.layout.rect(PanelPlan)
		d := detailAt(m.grid().plain(), r)
		if !strings.HasPrefix(d[1], "h1 "+GlyphNone+" in progress") || !strings.HasPrefix(d[2], "for you:") {
			t.Fatalf("%d: strip %q\n%s", w, d, m.grid().plain())
		}
		m = keys(t, m, 'j')
		if d := detailAt(m.grid().plain(), r); !strings.HasPrefix(d[1], "h5 ") {
			t.Fatalf("%d: after j %q", w, d)
		}
	}
}

// The tab's selected task stays in view above the detail: the list's rows
// end where the detail begins.
func TestPlanTabSelectionAboveTheDetail(t *testing.T) {
	var rows []plan.Row
	for i := 0; i < 30; i++ {
		rows = append(rows, boardRow(fmt.Sprintf("t%02d", i), plan.Upcoming, "coder"))
	}
	m := keys(t, planShellOf(t, boardPlan(rows...), 160, 34), '4')
	m = keys(t, m, 'G')
	r := m.layout.rect(PanelPlan)
	lines := strings.Split(m.grid().plain(), "\n")
	sel := -1
	for y := r.Y; y < r.Y+r.H; y++ {
		if strings.Contains(lines[y], GlyphSelect) {
			sel = y
		}
	}
	if sel < 0 || sel >= r.Y+r.H-1-planDetailRows {
		t.Fatalf("selection on row %d, detail from %d:\n%s", sel, r.Y+r.H-1-planDetailRows, m.grid().plain())
	}
	if d := detailAt(m.grid().plain(), r); !strings.HasPrefix(d[1], "t29 ") {
		t.Fatalf("detail %q", d)
	}
}

// §4.1 point 2, the drawer: the same three rows at its frame's bottom, no
// side frame; j moves the selection and the detail, the selection above it.
func TestPlanDrawerDetailBelowTheList(t *testing.T) {
	var rows []plan.Row
	for i := 0; i < 40; i++ {
		rows = append(rows, boardRow(fmt.Sprintf("t%02d", i), plan.Upcoming, "coder"))
	}
	m := keys(t, planShellOf(t, boardPlan(rows...), 160, 40), 'p')
	r := m.readerRect()
	screen := m.grid().plain()
	if strings.Contains(screen, "╭─ Task") {
		t.Fatalf("a side frame:\n%s", screen)
	}
	if d := detailAt(screen, r); !strings.HasPrefix(d[1], "t00 ") {
		t.Fatalf("detail %q\n%s", d, screen)
	}
	m = keys(t, m, 'G')
	screen = m.grid().plain()
	if d := detailAt(screen, r); !strings.HasPrefix(d[1], "t39 ") {
		t.Fatalf("after G %q\n%s", d, screen)
	}
	lines := strings.Split(screen, "\n")
	sel := -1
	for y := r.Y; y < r.Y+r.H; y++ {
		if strings.Contains(lines[y], GlyphSelect) {
			sel = y
		}
	}
	if sel < 0 || sel >= r.Y+r.H-1-planDetailRows {
		t.Fatalf("selection on row %d, detail from %d:\n%s", sel, r.Y+r.H-1-planDetailRows, screen)
	}
}

// §4.1 point 1: in the drawer, For you's rows are its own: its items (at
// least the "nothing" line) and the plan pointer, never more than the top
// row it had; closed, the layout is the shell's own.
func TestShellDrawerForYouRows(t *testing.T) {
	c := shellCounts{ForYou: 3, ForYouExtra: 1, Agents: 12, ForYouWidth: 50}
	l := shellLayoutFor(160, 44, c, PanelForYou, PanelActivity)
	d := shellDrawer(l, c)
	if d.ForYou != (Rect{0, l.ForYou.Y, 160, 3 + 1 + 2}) || d.ForYouRows != 4 {
		t.Fatalf("For you %+v rows %d", d.ForYou, d.ForYouRows)
	}
	if d.Lower != l.Lower || d.Agents != l.Agents || d.Footer != l.Footer {
		t.Fatalf("the rest moved: %+v", d)
	}
	c.ForYou = 0
	if d := shellDrawer(l, c); d.ForYouRows != 2 { // the "nothing waits" line and the pointer
		t.Fatalf("empty: %d rows", d.ForYouRows)
	}
	c.ForYou = 60
	if d := shellDrawer(l, c); d.ForYou.H != l.ForYou.H || d.ForYouRows != l.ForYouRows {
		t.Fatalf("capped: %+v, the top row %+v", d.ForYou, l.ForYou)
	}
	if m := shellDrawer(shellLayoutFor(100, 40, c, PanelForYou, PanelActivity), c); m.Tier != TierMedium || m.ForYou.W != 100 {
		t.Fatalf("medium changed: %+v", m)
	}
}

// The tab's lines that are not tasks (a group's header, a finished group,
// DONE) end inside the frame: their padding is not cut into a "…" (it was
// hidden under the side panel before §4.1).
func TestPlanTabLinesEndInsideTheFrame(t *testing.T) {
	m := keys(t, planShellOf(t, chantierPlan(), 160, 44), '4')
	r := m.layout.rect(PanelPlan)
	lines := strings.Split(m.grid().plain(), "\n")
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		row := []rune(lines[y])
		if row[r.X+r.W-2] == []rune(GlyphEllipsis)[0] {
			t.Errorf("row %d cut: %q", y, string(row[r.X:r.X+r.W]))
		}
	}
}

// §4.1 point 2 (foureyes): a click on the drawer's detail rows is no click
// on the body hidden under them: the selection stays and no page opens; a
// click on the last body row above them still selects its task.
func TestPlanDrawerClickOnTheDetail(t *testing.T) {
	var rows []plan.Row
	for i := 0; i < 40; i++ {
		rows = append(rows, boardRow(fmt.Sprintf("t%02d", i), plan.Upcoming, "coder"))
	}
	m := keys(t, planShellOf(t, boardPlan(rows...), 160, 40), 'p')
	r, p := m.readerRect(), m.rd.top()
	body := m.pageRows(p)
	if len(p.body) <= body {
		t.Fatalf("fixture: body %d lines in %d rows", len(p.body), body)
	}
	for i := 0; i < planDetailRows; i++ {
		y := r.Y + 1 + body + i
		m = step(t, m, tea.MouseClickMsg{X: r.X + 10, Y: y, Button: tea.MouseLeft})
		if top := m.rd.top(); top.kind != ovPlan || top.sel != 0 || len(m.rd.pages) != 1 {
			t.Fatalf("a click on detail row %d: page %v, sel %d, %d pages", i, top.kind, top.sel, len(m.rd.pages))
		}
	}
	m = step(t, m, tea.MouseClickMsg{X: r.X + 10, Y: r.Y + body, Button: tea.MouseLeft}) // the last body row
	top := m.rd.top()
	if top.kind != ovPlan || top.sel == 0 || top.items[top.sel].line != top.top+body-1 {
		t.Fatalf("the last body row: sel %d (line %d), top %d", top.sel, top.items[top.sel].line, top.top)
	}
}

// The kept rows and the items share the frame's inside: in a frame shorter
// than its header and the kept rows, the kept rows still end above the
// bottom border, right under the items (foureyes' nit).
func TestListBottomRectInsideAShortFrame(t *testing.T) {
	for h := 2; h <= 8; h++ {
		l := listState{rect: Rect{0, 0, 40, h}, header: Line{{Text: "head"}}}
		l.SetBottom(planDetailRows)
		_, iy, _, ih := l.inner()
		br := l.BottomRect()
		if br.H > 0 && (br.Y != iy+ih || br.Y+br.H > h-1) {
			t.Errorf("height %d: items %d+%d, kept %+v", h, iy, ih, br)
		}
	}
}

// shortPlan is a plan that fits in a few rows: one group in progress, one
// finished.
func shortPlan() planState {
	p := boardPlan(inGroup(boardRow("a", plan.InProgress, "coder"), "g"), inGroup(boardRow("b", plan.Done, "coder"), "h"))
	p.file.Groups = []plan.Group{{ID: "g", Title: "Open"}, {ID: "h", Title: "Finished"}}
	return p
}

// longPlan is a plan longer than any frame here.
func longPlan() planState {
	var rows []plan.Row
	for i := 0; i < 60; i++ {
		rows = append(rows, boardRow(fmt.Sprintf("t%02d", i), plan.Upcoming, "coder"))
	}
	return boardPlan(rows...)
}

// §4.2 (a): the Plan tab shrinks to its content and Activity takes the
// height left under it, named alone, shown and not in the focus order (it
// would become the active tab); a plan longer than the zone keeps it all.
func TestPlanTabShrinksActivityBelow(t *testing.T) {
	for _, w := range []int{140, 100} {
		m := keys(t, planShellOf(t, shortPlan(), w, 40), '4')
		l, pl := m.layout, &m.lists.plan
		if l.Lower.H != len(pl.items)+pl.bottom+2 || l.BelowPanel != PanelActivity || l.Below.Y != l.Lower.Y+l.Lower.H || l.Below.Y+l.Below.H != l.Input.Y {
			t.Fatalf("%d: plan %+v below %+v (%v), %d items", w, l.Lower, l.Below, l.BelowPanel, len(pl.items))
		}
		if m.lists.activity.rect != l.Below || slices.Contains(l.Visible, PanelActivity) {
			t.Fatalf("%d: activity at %+v, visible %v", w, m.lists.activity.rect, l.Visible)
		}
		lines := strings.Split(m.grid().plain(), "\n")
		if top := lines[l.Below.Y]; !strings.Contains(top, "Activity") || strings.Contains(top, "2 Board") {
			t.Fatalf("%d: the frame under the plan %q", w, top)
		}
	}
	if m := keys(t, planShellOf(t, longPlan(), 140, 40), '4'); m.layout.Below.H != 0 {
		t.Fatalf("a long plan shared its zone: %+v", m.layout.Below)
	}
}

// §4.2 (b): the drawer shrinks to its content and the active tab shows
// under it; with the Plan tab active, or a long plan, it keeps the zone.
func TestPlanDrawerShrinksActiveTabBelow(t *testing.T) {
	m := keys(t, planShellOf(t, shortPlan(), 140, 40), 'p')
	r, p := m.readerRect(), m.rd.top()
	if r.H != len(p.body)+len(p.detail)+2 || m.layout.Lower.Y != r.Y+r.H || m.layout.Lower.Y+m.layout.Lower.H != m.layout.Input.Y {
		t.Fatalf("drawer %+v (body %d, detail %d), lower %+v", r, len(p.body), len(p.detail), m.layout.Lower)
	}
	if lines := strings.Split(m.grid().plain(), "\n"); !strings.Contains(lines[m.layout.Lower.Y], "Activity") {
		t.Fatalf("no active tab under the drawer:\n%s", m.grid().plain())
	}
	m = keys(t, keys(t, planShellOf(t, shortPlan(), 140, 40), '4'), 'p')
	if r := m.readerRect(); r.Y+r.H != m.layout.Footer.Y {
		t.Fatalf("the Plan tab active: drawer %+v", r)
	}
	m = keys(t, planShellOf(t, longPlan(), 140, 40), 'p')
	if r := m.readerRect(); r.Y+r.H != m.layout.Footer.Y {
		t.Fatalf("a long plan: drawer %+v", r)
	}
}

// The tab drops the board's mark cells, not its first segment: an upcoming
// row (a blank glyph, merged with the mark by FitLine) keeps its title.
func TestPlanTabUpcomingRowKeepsItsTitle(t *testing.T) {
	m := keys(t, planShellOf(t, chantierPlan(), 140, 44), '4')
	if s := m.grid().plain(); !strings.Contains(s, "title s1") || !strings.Contains(s, "title o1") {
		t.Fatalf("upcoming titles lost:\n%s", s)
	}
}
