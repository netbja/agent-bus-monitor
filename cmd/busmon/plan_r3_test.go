package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/netbja/agent-bus-monitor/bus"
	"github.com/netbja/agent-bus-monitor/plan"
)

// The person's "Re: Plan tab, r3" (items 1, 2, 3 and 5).

// planShellWith is planShellOf with the shared state set by set first.
func planShellWith(t *testing.T, p planState, w, h int, set func(st *shared)) Model {
	t.Helper()
	st := &shared{agents: map[string]*agentState{}, person: "charlie"}
	st.plan = p
	st.plan.in = plan.Inputs{Board: map[string]bus.BoardEntry{}}
	if set != nil {
		set(st)
	}
	m := newShell(shellConfig{project: "demo", self: "charlie", st: st})
	m = step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	return step(t, m, pollMsg{snap: takeSnapshot(st, shellNow), at: shellNow})
}

// releasesFixture: two past minor releases (one patched), their plan files
// (v0.27 has none), two decided ideas and one still to discuss.
func releasesFixture(st *shared) {
	st.releases = releaseData{
		read: true,
		tags: []releaseTag{{Name: "v0.28.1", Date: "2026-11-10"}, {Name: "v0.28.0", Date: "2026-11-08"}, {Name: "v0.27.0", Date: "2026-11-01"}},
		plans: map[string]plan.File{"v0.28": {Plan: "v0.28", Goal: "the inbox, again",
			Groups: []plan.Group{{ID: "ui", Title: "busmon"}},
			Tasks:  []plan.Task{{ID: "inbox-row", Title: "the inbox row", Owner: "coder", Group: "ui"}, {ID: "inbox-keys", Title: "the inbox keys", Owner: "coder", Group: "ui"}}}},
	}
	st.ideas = []bus.Idea{
		{ID: "i1", IdeaDef: bus.IdeaDef{Title: "a quiet update"}, State: bus.IdeaDecided},
		{ID: "i2", IdeaDef: bus.IdeaDef{Title: "project switch"}, State: bus.IdeaDecided},
		{ID: "i3", IdeaDef: bus.IdeaDef{Title: "still open"}, State: bus.IdeaToDiscuss},
	}
}

// Item 1: the Plan takes the lower zone's full height; no Activity frame is
// drawn under it, however short the plan.
func TestPlanTabFullHeight(t *testing.T) {
	for _, w := range []int{160, 100} {
		m := keys(t, planShellOf(t, shortPlan(), w, 40), '4')
		l := m.layout
		if l.Lower.Y+l.Lower.H != l.Input.Y {
			t.Fatalf("%d: the Plan ends at %d, the input line at %d", w, l.Lower.Y+l.Lower.H, l.Input.Y)
		}
		lines := strings.Split(m.grid().plain(), "\n")
		for y := l.Lower.Y + 1; y < l.Lower.Y+l.Lower.H; y++ {
			if strings.Contains(lines[y], "Activity") {
				t.Fatalf("%d: Activity under the Plan on row %d:\n%s", w, y, m.grid().plain())
			}
		}
		if slices.Contains(l.Visible, PanelActivity) {
			t.Fatalf("%d: Activity still in the focus order %v", w, l.Visible)
		}
	}
}

// Item 1: what arrived while Activity was not shown is counted on its tab,
// "1 Activity · 3 new"; showing Activity reads them.
func TestTabBarActivityNew(t *testing.T) {
	m := keys(t, planShellOf(t, shortPlan(), 160, 40), '4')
	tabRow := func(m Model) string { return strings.Split(m.grid().plain(), "\n")[m.layout.Lower.Y] }
	if strings.Contains(tabRow(m), "new") {
		t.Fatalf("nothing new yet: %q", tabRow(m))
	}
	var evs []bus.Event
	for i := range 3 {
		evs = append(evs, bus.Event{Kind: "report", Agent: "coder", ID: fmt.Sprintf("%d-%d", shellNow.UnixMilli(), i), Message: "news"})
	}
	m = step(t, m, tailMsg{events: evs})
	if !strings.Contains(tabRow(m), "1 Activity "+GlyphNone+" 3 new") {
		t.Fatalf("the tab bar %q", tabRow(m))
	}
	m = keys(t, m, '1', '4')
	if strings.Contains(tabRow(m), "new") {
		t.Fatalf("read on Activity, still new: %q", tabRow(m))
	}
}

// Item 2: one blank row between the tab bar and the version line.
func TestPlanTabBlankUnderTabBar(t *testing.T) {
	for _, w := range []int{160, 100} {
		m := keys(t, planShellOf(t, shortPlan(), w, 40), '4')
		r := m.layout.rect(PanelPlan)
		lines := strings.Split(m.grid().plain(), "\n")
		inner := func(y int) string { return strings.TrimSpace(string([]rune(lines[y])[r.X+1 : r.X+r.W-1])) }
		if inner(r.Y+1) != "" || !strings.HasPrefix(inner(r.Y+2), "v0.29") {
			t.Fatalf("%d: rows %q, %q", w, inner(r.Y+1), inner(r.Y+2))
		}
	}
}

// Item 3: the keys are said once in the Plan frame, in the strip under its
// rule; the frame's bottom border carries none.
func TestPlanTabHintsOnce(t *testing.T) {
	for _, w := range []int{160, 100} {
		m := keys(t, planShellOf(t, shortPlan(), w, 40), '4')
		r := m.layout.rect(PanelPlan)
		lines := strings.Split(m.grid().plain(), "\n")
		frame := strings.Join(lines[r.Y:r.Y+r.H], "\n")
		if n := strings.Count(frame, "priority"); n != 1 {
			t.Fatalf("%d: priority said %d times:\n%s", w, n, frame)
		}
		if strings.Contains(lines[r.Y+r.H-1], "priority") {
			t.Fatalf("%d: hints on the bottom border %q", w, lines[r.Y+r.H-1])
		}
		d := detailAt(m.grid().plain(), r)
		if !strings.Contains(d[0], glyphHorizontal) || !strings.Contains(d[2], "d done") || !strings.Contains(d[2], "enter") {
			t.Fatalf("%d: strip %q", w, d)
		}
	}
	// A fold selected: no task, the rule and the keys stay.
	m := keys(t, planShellOf(t, shortPlan(), 160, 40), '4', 'G')
	if d := detailAt(m.grid().plain(), m.layout.rect(PanelPlan)); !strings.Contains(d[2], "priority") {
		t.Fatalf("a fold selected: strip %q", d)
	}
}

// Item 5: from 130 columns a Releases column right of the Plan: next with
// its decided ideas, the current plan highlighted, then the past releases
// with their date and their plan's tasks; below 130 it is hidden.
func TestPlanReleasesColumn(t *testing.T) {
	m := keys(t, planShellWith(t, shortPlan(), 160, 40, releasesFixture), '4')
	l := m.layout
	if l.Side.W == 0 || l.Side.X != l.Lower.X+l.Lower.W || l.Side.Y != l.Lower.Y || l.Side.H != l.Lower.H {
		t.Fatalf("side %+v lower %+v", l.Side, l.Lower)
	}
	if i := slices.Index(l.Visible, PanelReleases); i < 0 || l.Visible[i-1] != PanelPlan {
		t.Fatalf("visible %v", l.Visible)
	}
	text := sideText(m)
	for _, want := range []string{"Releases", "next", "2 decided", "a quiet update", "project switch", "v0.29", "1/2", "v0.28.1", "2026-11-10", "v0.28.0", "2 tasks done", "v0.27.0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("no %q in\n%s", want, text)
		}
	}
	if strings.Contains(text, "still open") {
		t.Fatalf("an idea to discuss is listed:\n%s", text)
	}
	order := []string{"next", "v0.29", "v0.28.1", "v0.28.0", "v0.27.0"}
	for i := 1; i < len(order); i++ {
		if strings.Index(text, order[i-1]) > strings.Index(text, order[i]) {
			t.Fatalf("%s after %s:\n%s", order[i-1], order[i], text)
		}
	}
	cur := m.lists.releases.items[slices.IndexFunc(m.lists.releases.items, func(it ListItem) bool { return it.Key == releaseCurrent })]
	if cur.Line[0].Role != RoleAccent || !cur.Line[0].Bold {
		t.Fatalf("the current release is not highlighted: %+v", cur.Line)
	}
	if m := keys(t, planShellWith(t, shortPlan(), 129, 40, releasesFixture), '4'); m.layout.Side.W != 0 || slices.Contains(m.layout.Visible, PanelReleases) {
		t.Fatalf("129 columns: side %+v", m.layout.Side)
	}
}

// sideText is the Releases column's cells, row by row.
func sideText(m Model) string {
	s := m.layout.Side
	lines := strings.Split(m.grid().plain(), "\n")
	var out []string
	for y := s.Y; y < s.Y+s.H; y++ {
		out = append(out, string([]rune(lines[y])[s.X:s.X+s.W]))
	}
	return strings.Join(out, "\n")
}

// Item 5: V shows the column below 130 columns and hides it from 130; the
// Plan tab only, and never at the narrow tier.
func TestPlanReleasesToggle(t *testing.T) {
	m := keys(t, planShellWith(t, shortPlan(), 110, 40, releasesFixture), '4', 'V')
	if m.layout.Side.W == 0 || !strings.Contains(sideText(m), "v0.28.0") {
		t.Fatalf("V at 110: side %+v", m.layout.Side)
	}
	m = keys(t, m, 'V')
	if m.layout.Side.W != 0 {
		t.Fatalf("V again: side %+v", m.layout.Side)
	}
	m = keys(t, planShellWith(t, shortPlan(), 160, 40, releasesFixture), '4', 'V')
	if m.layout.Side.W != 0 {
		t.Fatalf("V at 160: side %+v", m.layout.Side)
	}
	m = keys(t, planShellWith(t, shortPlan(), 160, 40, releasesFixture), '2')
	if m.layout.Side.W != 0 {
		t.Fatalf("on the Board: side %+v", m.layout.Side)
	}
}

// Item 5: Enter on a release opens its plan, read only: a past one from
// its plan file, the next one its decided ideas; + changes nothing.
func TestPlanReleasesOpen(t *testing.T) {
	m := keys(t, planShellWith(t, shortPlan(), 160, 40, releasesFixture), '4', tea.Key{Code: tea.KeyTab})
	if m.ui.focus != PanelReleases {
		t.Fatalf("tab from the Plan: focus %v", m.ui.focus)
	}
	for m.lists.releases.Selected() != releaseTagKey+"v0.28.0" {
		before := m.lists.releases.Selected()
		m = keys(t, m, 'j')
		if m.lists.releases.Selected() == before {
			t.Fatalf("v0.28.0 not reached, at %q", before)
		}
	}
	m = keys(t, m, tea.Key{Code: tea.KeyEnter})
	p := m.rd.top()
	if p == nil || p.kind != ovRelease {
		t.Fatalf("enter opened %+v", p)
	}
	body := plainLines(p.body)
	for _, want := range []string{"the inbox, again", "busmon", "inbox-row", "the inbox keys"} {
		if !strings.Contains(body, want) {
			t.Fatalf("no %q in\n%s", want, body)
		}
	}
	if !strings.Contains(p.title, "v0.28.0") || !strings.Contains(p.title, "read only") {
		t.Fatalf("title %q", p.title)
	}
	m = keys(t, m, '+')
	if m.rd.top().kind != ovRelease || !strings.Contains(m.ui.flash, "only a tracked request") { // refused, as on any page but a request's
		t.Fatalf("+ on a release page: %+v %q", m.rd.top().kind, m.ui.flash)
	}
	// v0.27.0 has no plan file: the page says so.
	m = keys(t, m, tea.Key{Code: tea.KeyEscape}, 'j', tea.Key{Code: tea.KeyEnter})
	if body := plainLines(m.rd.top().body); !strings.Contains(body, "no plan file") {
		t.Fatalf("v0.27.0:\n%s", body)
	}
	// next: its decided ideas.
	m = keys(t, m, tea.Key{Code: tea.KeyEscape}, 'g', tea.Key{Code: tea.KeyEnter})
	if body := plainLines(m.rd.top().body); !strings.Contains(body, "a quiet update") || strings.Contains(body, "still open") {
		t.Fatalf("next:\n%s", body)
	}
}

// plainLines is lines' text, one per row.
func plainLines(lines []Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.text())
		b.WriteByte('\n')
	}
	return b.String()
}

// Item 5's source: the checkout's release tags, newest first, with their
// date, and docs/plans/*.json by plan name; a file that does not parse is
// left out, never an error.
func TestReadReleases(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = dir, append(os.Environ(), env...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	id := []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid"}
	at := func(d string) []string {
		return append(slices.Clone(id), "GIT_COMMITTER_DATE="+d+"T12:00:00Z", "GIT_AUTHOR_DATE="+d+"T12:00:00Z")
	}
	run(nil, "init", "-q")
	if err := os.MkdirAll(filepath.Join(dir, "docs", "plans"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, "docs", "plans", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("v0.1.json", `{"plan":"v0.1","goal":"first","tasks":[{"id":"a","title":"A","owner":"coder"},{"id":"b","title":"B","owner":"coder","extra":1}]}`)
	write("broken.json", `{"plan":`)
	run(at("2026-01-02"), "add", ".")
	run(at("2026-01-02"), "commit", "-q", "-m", "one")
	run(at("2026-01-02"), "tag", "-a", "-m", "v0.1.0", "v0.1.0")
	run(at("2026-02-03"), "commit", "-q", "--allow-empty", "-m", "two")
	run(at("2026-02-03"), "tag", "-a", "-m", "v0.2.0", "v0.2.0")
	run(at("2026-02-03"), "tag", "not-a-release")
	d, err := readReleases(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []releaseTag{{Name: "v0.2.0", Date: "2026-02-03"}, {Name: "v0.1.0", Date: "2026-01-02"}}; !slices.Equal(d.tags, want) {
		t.Fatalf("tags %+v", d.tags)
	}
	if f, ok := d.plans["v0.1"]; !ok || len(f.Tasks) != 2 || f.Goal != "first" || len(d.plans) != 1 {
		t.Fatalf("plans %+v", d.plans)
	}
}
