package main

import (
	"strings"
	"testing"

	"github.com/netbja/agent-bus-monitor/plan"
)

// plan-chantier §3: a plan with groups draws one block per group instead of
// NOW, NEXT and LATER.

func inGroup(r plan.Row, g string) plan.Row {
	r.Task.Group = g
	return r
}

// chantierPlan: Leave herdr (a row waiting for the person, one in progress,
// one approved in review, one later, one done), Self-repair watcher (one
// ready, one done), Work tab (every task done), and one task with no group.
func chantierPlan() planState {
	approved := boardRow("h5", plan.InReview, "coder")
	approved.Verdict = plan.Approved
	p := boardPlan(
		inGroup(boardRow("w1", plan.Done, "coder"), "work-tab"),
		inGroup(boardRow("s1", plan.Upcoming, "coder"), "self-repair"),
		inGroup(boardRow("h1", plan.InProgress, "coder"), "herdr-exit"),
		inGroup(boardRow("h2", plan.Waiting, plan.Person), "herdr-exit"),
		inGroup(boardRow("h3", plan.Upcoming, "coder", "h1"), "herdr-exit"),
		inGroup(boardRow("h4", plan.Done, "coder"), "herdr-exit"),
		inGroup(approved, "herdr-exit"),
		inGroup(boardRow("s2", plan.Done, "coder"), "self-repair"),
		boardRow("o1", plan.Upcoming, "coder"),
	)
	p.file.Groups = []plan.Group{{ID: "self-repair", Title: "Self-repair watcher"}, {ID: "work-tab", Title: "Work tab"}, {ID: "herdr-exit", Title: "Leave herdr"}}
	return p
}

// lineWith is the first body line (after the progress and head) starting
// with prefix, and its index; -1 when none.
func lineWith(v planBoardView, prefix string) (string, int) {
	for i, l := range v.lines {
		if t := l.text(); strings.HasPrefix(t, prefix) {
			return t, i
		}
	}
	return "", -1
}

// itemLine is the line a task's row is drawn on, -1 when it is not drawn.
func itemLine(v planBoardView, id string) int {
	for _, it := range v.items {
		if it.task == id {
			return it.line
		}
	}
	return -1
}

// plan-chantier §3, views-redesign §4.2: one block per group with open
// tasks, in order (waiting for the person, then in hand, then the rest,
// Other last), each "▼ <title> <bar> done/total <state>", its rows under it;
// every finished group folded into one line at the end; no NOW, NEXT, LATER.
func TestPlanBoardGroups(t *testing.T) {
	v := planBoard(chantierPlan(), 0, false, shellNow, 120, TierWide, false, nil)
	for _, h := range []string{"NOW  ", "NEXT  ", "LATER  "} {
		if _, i := lineWith(v, GlyphDown+" "+h); i >= 0 {
			t.Fatalf("a grouped plan draws %q", h)
		}
	}
	herdr, hi := lineWith(v, GlyphDown+" Leave herdr")
	self, si := lineWith(v, GlyphDown+" Self-repair watcher")
	other, oi := lineWith(v, GlyphDown+" Other")
	done, di := lineWith(v, GlyphSelect+" 1 group done")
	if !(hi >= 0 && hi < si && si < oi && oi < di) {
		t.Fatalf("order herdr %d, self-repair %d, other %d, done %d:\n%s", hi, si, oi, di, v.plain)
	}
	if !strings.Contains(herdr, "1/5") || !strings.Contains(herdr, "waits for you") {
		t.Fatalf("herdr header %q", herdr)
	}
	if !strings.Contains(self, "1/2") || !strings.Contains(self, "not started") {
		t.Fatalf("self-repair header %q", self)
	}
	if !strings.Contains(other, "0/1") {
		t.Fatalf("other header %q", other)
	}
	if !strings.Contains(done, GlyphNone+" 1 task") || !strings.Contains(done, "(d shows)") || strings.Contains(v.plain, "Work tab") {
		t.Fatalf("done line %q:\n%s", done, v.plain)
	}
	// Rows under their header in the roadmap's order (views-redesign §4):
	// NOW (h1), APPROVED (h5), NEXT (h2, the person's decision), LATER (h3);
	// done rows folded; headers are not items, the fold is the last.
	want := []string{"h1", "h5", "h2", "h3", "s1", "o1", ""}
	if len(v.items) != len(want) {
		t.Fatalf("items %+v", v.items)
	}
	for i, id := range want {
		if v.items[i].task != id {
			t.Fatalf("item %d = %s, want %s (%+v)", i, v.items[i].task, id, v.items)
		}
	}
	if l := itemLine(v, "h1"); l != hi+1 {
		t.Fatalf("h1 on line %d, header on %d", l, hi)
	}
	if l := itemLine(v, "s1"); l != si+1 {
		t.Fatalf("s1 on line %d, header on %d", l, si)
	}
	for _, id := range []string{"h4", "s2", "w1"} {
		if itemLine(v, id) >= 0 {
			t.Fatalf("done row %s drawn while folded", id)
		}
	}
}

// §3: d shows every done row under its group, and each finished group's
// line with its rows instead of the fold.
func TestPlanBoardGroupsShowDone(t *testing.T) {
	v := planBoard(chantierPlan(), 0, true, shellNow, 120, TierWide, false, nil)
	_, hi := lineWith(v, GlyphDown+" Leave herdr")
	_, si := lineWith(v, GlyphDown+" Self-repair watcher")
	_, wi := lineWith(v, GlyphDown+" Work tab")
	if l := itemLine(v, "h4"); l <= hi || l >= si {
		t.Fatalf("h4 on line %d, not under Leave herdr (%d..%d)", l, hi, si)
	}
	if l := itemLine(v, "w1"); l != wi+1 {
		t.Fatalf("w1 on line %d, Work tab on %d", l, wi)
	}
	if _, di := lineWith(v, GlyphSelect+" 1 group done"); di >= 0 {
		t.Fatal("a fold while d shows")
	}
}

// §3: the narrow header is ▼, the title, the count and the state word;
// the wide one fits its width.
func TestPlanBoardGroupHeaderWidths(t *testing.T) {
	v := planBoard(chantierPlan(), 0, false, shellNow, 40, TierNarrow, false, nil)
	if h, _ := lineWith(v, GlyphDown+" Leave herdr"); strings.TrimRight(h, " ") != GlyphDown+" Leave herdr 1/5  waits for you" {
		t.Fatalf("narrow header %q", h)
	}
	v = planBoard(chantierPlan(), 0, false, shellNow, 72, TierWide, false, nil)
	if h, _ := lineWith(v, GlyphDown+" Leave herdr"); !strings.Contains(h, "waits for you") || Width(h) > 72 {
		t.Fatalf("72 cells: %q", h)
	}
}

// §3: a failed read draws today's single list, no groups; a plan without
// groups draws NOW, NEXT and LATER as before.
func TestPlanBoardGroupsUnknownAndUngrouped(t *testing.T) {
	p := chantierPlan()
	for i := range p.rows {
		if p.rows[i].State != plan.Done {
			p.rows[i].State = plan.Unknown
		}
	}
	v := planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	if _, i := lineWith(v, GlyphDown+" Leave herdr"); i >= 0 {
		t.Fatalf("groups drawn on unknown reads:\n%s", v.plain)
	}
	p = chantierPlan()
	p.file.Groups = nil
	v = planBoard(p, 0, false, shellNow, 120, TierWide, false, nil)
	if _, i := lineWith(v, GlyphDown+" NOW  "); i < 0 {
		t.Fatalf("an ungrouped plan lost NOW:\n%s", v.plain)
	}
}

// §3: selection is per task row, headers skipped: item 2 is h2 (the
// roadmap's order, views-redesign §4), and the detail shows h2 (§4.1).
func TestPlanBoardGroupsSelection(t *testing.T) {
	v := planBoard(chantierPlan(), 2, false, shellNow, 120, TierWide, true, nil)
	if v.items[2].task != "h2" || len(v.detail) != planDetailRows || !strings.HasPrefix(v.detail[1].text(), "h2 "+GlyphNone) {
		t.Fatalf("item 2 %+v, detail %q", v.items[2], linesText(v.detail))
	}
}

// §3 (reviewer-codex): Other is last before the finished groups, even when
// every task in it is done (shown with d).
func TestPlanBoardGroupsOtherBeforeFinished(t *testing.T) {
	p := boardPlan(
		inGroup(boardRow("w1", plan.Done, "coder"), "work-tab"),
		inGroup(boardRow("h1", plan.InProgress, "coder"), "herdr-exit"),
		boardRow("o1", plan.Done, "coder"),
	)
	p.file.Groups = []plan.Group{{ID: "work-tab", Title: "Work tab"}, {ID: "herdr-exit", Title: "Leave herdr"}}
	v := planBoard(p, 0, true, shellNow, 120, TierWide, false, nil)
	_, hi := lineWith(v, GlyphDown+" Leave herdr")
	_, oi := lineWith(v, GlyphDown+" Other")
	_, wi := lineWith(v, GlyphDown+" Work tab")
	if !(hi >= 0 && hi < oi && oi < wi) {
		t.Fatalf("herdr %d, other %d, work %d:\n%s", hi, oi, wi, v.plain)
	}
}
