package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/plan"
)

// barRoles are the roles of the bar's cells on line i, in order.
func barRoles(v planBoardView, i int) []Role {
	var cells []Role
	for _, s := range v.lines[i] {
		for range strings.Count(s.Text, GlyphThinBar) {
			cells = append(cells, s.Role)
		}
	}
	return cells
}

// views-redesign §4.1 point 4: a group's bar is in the colour of its state:
// orange when a task waits for the person, blue when one is in progress or
// in review, the text colour when nothing has started; the approved cells
// keep theirs, the rest is the track.
func TestPlanGroupBarColourByState(t *testing.T) {
	v := planBoard(chantierPlan(), 0, false, shellNow, 120, TierWide, false, nil)
	_, hi := lineWith(v, "Leave herdr") // h2 waits for the person, h1 in progress
	_, si := lineWith(v, "Self-repair watcher")
	_, reviewRole := planLook(plan.InReview)
	if c := barRoles(v, hi); len(c) != 20 || c[0] != RoleAttention || c[3] != RoleAttention || c[4] != reviewRole || c[8] != RoleMuted {
		t.Fatalf("a group waiting for the person: %v", c)
	}
	if c := barRoles(v, si); len(c) != 20 || c[0] != RoleText || c[9] != RoleText || c[10] != RoleMuted { // s1 upcoming, s2 done
		t.Fatalf("a group not started: %v", c)
	}
	p := boardPlan(
		inGroup(boardRow("a", plan.Done, "coder"), "g"),
		inGroup(boardRow("b", plan.InProgress, "coder"), "g"),
	)
	p.file.Groups = []plan.Group{{ID: "g", Title: "G"}}
	v = planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	_, gi := lineWith(v, "G ")
	if c := barRoles(v, gi); c[0] != RoleAccent || c[19] != RoleMuted {
		t.Fatalf("a group in progress: %v", c)
	}
	p.rows[1].State = plan.InReview
	v = planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	if c := barRoles(v, gi); c[0] != RoleAccent {
		t.Fatalf("a group in review: %v", c)
	}
}

// §4.1 point 3: a finished group is one grey line, no bar, and the finished
// groups follow one another with no blank line between them.
func TestPlanFinishedGroupsOneLineEach(t *testing.T) {
	p := boardPlan(
		inGroup(boardRow("h1", plan.InProgress, "coder"), "herdr"),
		inGroup(boardRow("w1", plan.Done, "coder"), "work"),
		inGroup(boardRow("q1", plan.Done, "coder"), "quota"),
		inGroup(boardRow("q2", plan.Done, "coder"), "quota"),
	)
	p.file.Groups = []plan.Group{{ID: "herdr", Title: "Leave herdr"}, {ID: "work", Title: "Work tab"}, {ID: "quota", Title: "Quota"}}
	v := planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	work, wi := lineWith(v, GlyphDone+" Work tab")
	quota, qi := lineWith(v, GlyphDone+" Quota")
	if wi < 0 || qi != wi+1 {
		t.Fatalf("finished groups on lines %d and %d, want consecutive:\n%s", wi, qi, v.plain)
	}
	if strings.TrimRight(work, " ") != GlyphDone+" Work tab  1/1 done" || strings.TrimRight(quota, " ") != GlyphDone+" Quota  2/2 done" {
		t.Fatalf("lines %q %q", work, quota)
	}
	for _, i := range []int{wi, qi} {
		for _, s := range v.lines[i] {
			if strings.Contains(s.Text, GlyphThinBar) || (strings.TrimSpace(s.Text) != "" && s.Role != RoleMuted) {
				t.Fatalf("not one grey line: %+v", v.lines[i])
			}
		}
	}
	if v.lines[wi-1] != nil {
		t.Fatalf("no blank line before the finished groups: %q", v.lines[wi-1].text())
	}
	if _, di := lineWith(v, "DONE  "); di != qi+2 || v.lines[qi+1] != nil {
		t.Fatalf("DONE after a blank line: done %d, last finished %d", di, qi)
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

// §4.1 point 2: the task's detail is three rows: a rule naming the task with
// enter's act at its right, then state, for you, owner, prio, review and PR
// (what matters to the person first), then after, unblocks and reason; short, the last field of a row is cut.
func TestPlanDetailRows(t *testing.T) {
	p := chantierPlan()
	var h1 plan.Row
	for _, r := range p.rows {
		if r.Task.ID == "h1" {
			h1 = r
		}
	}
	d := planDetail(p, h1, 120)
	if len(d) != planDetailRows {
		t.Fatalf("%d rows", len(d))
	}
	rule := strings.TrimRight(d[0].text(), " ")
	if !strings.HasPrefix(rule, glyphHorizontal+glyphHorizontal+" Task h1 ") || !strings.HasSuffix(rule, " enter task page") || Width(d[0].text()) != 120 {
		t.Fatalf("rule %q", rule)
	}
	sep := " " + GlyphNone + " "
	if got := strings.TrimRight(d[1].text(), " "); got != "state in progress"+sep+"for you nothing"+sep+"owner coder"+sep+"prio normal"+sep+"review "+GlyphDash+sep+"PR "+GlyphDash {
		t.Fatalf("row 1 %q", got)
	}
	if got := strings.TrimRight(d[2].text(), " "); got != "after "+GlyphDash+sep+"unblocks "+GlyphDash+sep+"reason "+GlyphDash {
		t.Fatalf("row 2 %q", got)
	}
	for _, l := range planDetail(p, h1, 40) {
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
		out = append(out, strings.TrimRight(string([]rune(lines[y])[r.X+1:r.X+r.W-1]), " "))
	}
	return out
}

// §4.1 point 2, the tab: no side frame; the detail at the frame's bottom,
// following j; a row gets the frame's width.
func TestPlanTabDetailBelowTheList(t *testing.T) {
	m := keys(t, planShellOf(t, chantierPlan(), 160, 44), '4')
	r := m.layout.rect(PanelPlan)
	screen := m.grid().plain()
	if strings.Contains(screen, "╭─ Task") {
		t.Fatalf("a side frame:\n%s", screen)
	}
	d := detailAt(screen, r)
	if !strings.Contains(d[0], "Task h1") || !strings.Contains(d[1], "state in progress") || !strings.Contains(d[2], "unblocks") {
		t.Fatalf("detail %q\n%s", d, screen)
	}
	m = keys(t, m, 'j')
	if d := detailAt(m.grid().plain(), r); !strings.Contains(d[0], "Task h5") {
		t.Fatalf("after j %q", d)
	}
	if w, _ := planTabWidths(r.W-2-listGutter, m.layout.Tier); w != r.W-2-listGutter {
		t.Fatalf("the list keeps %d of %d cells", w, r.W-2-listGutter)
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
	if d := detailAt(m.grid().plain(), r); !strings.Contains(d[0], "Task t29") {
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
	if d := detailAt(screen, r); !strings.Contains(d[0], "Task t00") {
		t.Fatalf("detail %q\n%s", d, screen)
	}
	m = keys(t, m, 'G')
	screen = m.grid().plain()
	if d := detailAt(screen, r); !strings.Contains(d[0], "Task t39") {
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
