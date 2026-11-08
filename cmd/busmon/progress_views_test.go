package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"
)

// progress-line §2.3: the task in hand is the first in progress or in
// review, else waiting for the person, answered or blocked; next is the
// first upcoming one.
func TestPlanInHand(t *testing.T) {
	p := boardPlan(boardRow("up", plan.Upcoming, "coder"), boardRow("w", plan.Waiting, plan.Person),
		boardRow("r", plan.InReview, "coder"), boardRow("i", plan.InProgress, "coder"))
	now, next, ok := planInHand(p)
	if !ok || now == nil || now.Task.ID != "r" || next == nil || next.Task.ID != "up" {
		t.Fatalf("in hand %v, next %v", now, next)
	}
	p = boardPlan(boardRow("b", plan.Blocked, "coder"), boardRow("d", plan.Done, "coder"))
	if now, next, _ := planInHand(p); now == nil || now.Task.ID != "b" || next != nil {
		t.Fatalf("blocked only: %v %v", now, next)
	}
	if now, next, _ := planInHand(boardPlan(boardRow("d", plan.Done, "coder"))); now != nil || next != nil {
		t.Fatalf("all done: %v %v", now, next)
	}
	if _, _, ok := planInHand(planState{}); ok {
		t.Fatal("no plan read as loaded")
	}
}

// §2.3 the header: "N of M done", the bar, now: and next:, next dropped
// first, then now; narrow keeps "N/M P%"; all done says so.
func TestPlanProgressHeader(t *testing.T) {
	p := boardPlan(boardRow("d", plan.Done, "coder"), boardRow("i", plan.InProgress, "coder"),
		boardRow("up", plan.Upcoming, "master"), boardRow("w", plan.Waiting, plan.Person))
	wide := planProgress(p, 140, TierWide).text()
	for _, want := range []string{"v0.29", "1 of 4 done", GlyphThinBar, "25%", "now: title i (coder)", "next: title up"} {
		if !strings.Contains(wide, want) {
			t.Errorf("wide header %q: no %q", wide, want)
		}
	}
	mid := planProgress(p, 80, TierMedium).text()
	if !strings.Contains(mid, "now: title i") || strings.Contains(mid, "next:") || Width(mid) > 80 {
		t.Errorf("80 cells: next goes first: %q", mid)
	}
	tight := planProgress(p, 45, TierMedium).text()
	if strings.Contains(tight, "now:") || strings.Contains(tight, "next:") || !strings.Contains(tight, "1 of 4 done") || Width(tight) > 45 {
		t.Errorf("45 cells: now goes next: %q", tight)
	}
	if narrow := planProgress(p, 40, TierNarrow).text(); narrow != "1/4 25%" {
		t.Errorf("narrow %q", narrow)
	}
	done := planProgress(boardPlan(boardRow("d", plan.Done, "coder")), 140, TierWide).text()
	if !strings.Contains(done, "every task is done") || strings.Contains(done, "now:") {
		t.Errorf("all done %q", done)
	}
	none := planProgress(boardPlan(boardRow("up", plan.Upcoming, "coder")), 140, TierWide).text()
	if strings.Contains(none, "now:") || !strings.Contains(none, "next: title up") {
		t.Errorf("nothing in hand %q", none)
	}
}

// §2.4: the task in hand's title is bold in the accent colour; no other row.
func TestPlanInHandHighlighted(t *testing.T) {
	p := boardPlan(boardRow("i", plan.InProgress, "coder"), boardRow("r", plan.InReview, "coder"), boardRow("up", plan.Upcoming, "coder"))
	title := func(id string) Seg {
		for _, r := range p.rows {
			if r.Task.ID == id {
				for _, s := range planBoardRow(p, r, false, shellNow, 120) {
					if strings.Contains(s.Text, "title "+id) {
						return s
					}
				}
			}
		}
		t.Fatalf("no row %s", id)
		return Seg{}
	}
	if s := title("i"); s.Role != RoleAccent || !s.Bold {
		t.Errorf("the task in hand: %+v", s)
	}
	for _, id := range []string{"r", "up"} {
		if s := title(id); s.Role == RoleAccent || s.Bold {
			t.Errorf("%s highlighted too: %+v", id, s)
		}
	}
}

// §2.1: at the wide width the priority is a word in a 4-cell column; under
// reqKindWidth the one-cell mark stays.
func TestRequestPriorityColumn(t *testing.T) {
	at := shellNow.Add(-time.Minute)
	for _, c := range []struct {
		width     int
		level     string
		want      string
		role      Role
		wantShort bool
	}{
		{120, "high", "high", RoleAttention, false},
		{120, "low", "low", RoleMuted, false},
		{120, "", "", RoleText, false},
		{90, "high", GlyphUp, RoleAttention, true},
		{90, "low", GlyphDownArrow, RoleMuted, true},
	} {
		lay := reqLayoutFor(c.width)
		row := lay.row(S(RoleMuted, GlyphIdle), lay.priority(c.level), "master", "coder", "", kindRequest, "a task", at, shellNow)
		if row.Width() != c.width {
			t.Errorf("%d %q: %d cells", c.width, c.level, row.Width())
		}
		found := c.want == ""
		for _, s := range row {
			if c.want != "" && strings.TrimSpace(s.Text) == c.want {
				found = s.Role == c.role
			}
		}
		if !found {
			t.Errorf("%d %q: no %q in %v: %q", c.width, c.level, c.want, c.role, row.text())
		}
		// the kind starts at the same cell whatever the level
		if i := strings.Index(row.text(), kindRequest[:3]); c.width == 120 && i != strings.Index(lay.row(S(RoleMuted, GlyphIdle), lay.priority("high"), "master", "coder", "", kindRequest, "a task", at, shellNow).text(), kindRequest[:3]) {
			t.Errorf("%q: the kind moved", c.level)
		}
	}
}

// §2.2: a tracked request a plan task names shows "plan #<id>" and the plan
// state, muted; an unlinked one shows nothing; too narrow, it goes first.
func TestRequestPlanLink(t *testing.T) {
	now := shellNow
	rq := func(task string) request {
		return request{Task: task, Tracked: true, ID: task + "-id", Thread: task + "-id", From: "master", Target: "coder",
			State: "accepted", Created: now.Add(-time.Hour), Updated: now.Add(-time.Hour), Accepted: now.Add(-time.Hour)}
	}
	p := boardPlan(plan.Row{Task: plan.Task{ID: "t4", Title: "the thread", Owner: "coder", Board: "idle-animation"}, State: plan.InProgress})
	in := reqInput{now: now, self: "charlie", reqs: []request{rq("idle-animation")}, plans: planLinks(p)}
	rowOf := func(in reqInput, width int) string {
		for _, it := range requestRows(requestGroups(in), reqOpen{}, now, width) {
			if strings.Contains(it.Line.text(), "idle-anim") {
				return it.Line.text()
			}
		}
		t.Fatalf("no row at %d", width)
		return ""
	}
	if got := rowOf(in, 140); !strings.Contains(got, "plan #t4 in progress") {
		t.Errorf("linked: %q", got)
	}
	if got := rowOf(in, 60); strings.Contains(got, "plan #") || !strings.Contains(got, "idle-anim") {
		t.Errorf("narrow: the link goes first, never the subject: %q", got)
	}
	in.plans = planLinks(planState{})
	if got := rowOf(in, 140); strings.Contains(got, "plan #") {
		t.Errorf("no plan: %q", got)
	}
	in.plans = planLinks(boardPlan(plan.Row{Task: plan.Task{ID: "t9", Title: "other", Board: "other-task"}, State: plan.Upcoming}))
	if got := rowOf(in, 140); strings.Contains(got, "plan #") {
		t.Errorf("unlinked: %q", got)
	}
}

// §2.2: the request's page names its plan task.
func TestRequestPagePlanLine(t *testing.T) {
	p := boardPlan(plan.Row{Task: plan.Task{ID: "t4", Title: "the thread", Board: "idle-animation"}, State: plan.InReview})
	l, ok := planLinkLine(planLinks(p), "idle-animation")
	if !ok || !strings.Contains(l.text(), "#t4") || !strings.Contains(l.text(), "the thread") || !strings.Contains(l.text(), "in review") {
		t.Fatalf("page line %q %v", l.text(), ok)
	}
	if _, ok := planLinkLine(planLinks(p), "nothing"); ok {
		t.Fatal("a line for an unlinked request")
	}
}

// §2.2 on screen: the Requests row and the request's page name the plan
// task, from the snapshot's plan.
func TestRequestPlanLinkOnScreen(t *testing.T) {
	m := reqShell(t, 140, 40, nil)
	m.snap.planState = boardPlan(plan.Row{Task: plan.Task{ID: "t1", Title: "group a", Owner: "coder", Board: "busmon-group-a"}, State: plan.InProgress})
	m.refresh()
	row := ""
	for _, it := range m.lists.requests.items {
		if it.Key == "req:busmon-group-a" {
			row = it.Line.text()
		}
	}
	if !strings.Contains(row, "plan #t1 in progress") {
		t.Fatalf("the row: %q", row)
	}
	m.lists.requests.Select("req:busmon-group-a")
	if m = keys(t, m, kEnter); !m.rd.open() || m.rd.top().kind != ovRequest {
		t.Fatal("no request page")
	}
	page := ""
	for _, l := range m.rd.top().body {
		page += l.text() + "\n"
	}
	if !strings.Contains(page, "plan  #t1 group a (in progress)") {
		t.Fatalf("the page:\n%s", page)
	}
}

// reviewer-codex: an answered task (master to record) is not in hand: §2.3
// names in progress or in review, else waiting for the person, else blocked.
func TestPlanInHandSkipsAnswered(t *testing.T) {
	p := boardPlan(boardRow("a", plan.Answered, plan.Person), boardRow("up", plan.Upcoming, "coder"))
	if now, next, _ := planInHand(p); now != nil || next == nil || next.Task.ID != "up" {
		t.Fatalf("answered read as in hand: %v, next %v", now, next)
	}
	if got := planProgress(p, 140, TierWide).text(); strings.Contains(got, "now:") {
		t.Fatalf("header %q", got)
	}
	p = boardPlan(boardRow("a", plan.Answered, plan.Person), boardRow("b", plan.Blocked, "coder"))
	if now, _, _ := planInHand(p); now == nil || now.Task.ID != "b" {
		t.Fatalf("blocked after answered: %v", now)
	}
}

// foureyes P3: a header derived from a kept value after a failed read says
// so with '?', at every width.
func TestPlanProgressPartial(t *testing.T) {
	p := boardPlan(boardRow("d", plan.Done, "coder"), boardRow("i", plan.InProgress, "coder"))
	p.partial = true
	if got := planProgress(p, 140, TierWide).text(); !strings.Contains(got, "1 of 2? done") {
		t.Errorf("wide %q", got)
	}
	if got := planProgress(p, 40, TierNarrow).text(); got != "1/2? 50%" {
		t.Errorf("narrow %q", got)
	}
}

// §2.3's order: waiting for the person comes before blocked, whatever the
// file order.
func TestPlanInHandWaitingBeforeBlocked(t *testing.T) {
	p := boardPlan(boardRow("b", plan.Blocked, "coder"), boardRow("w", plan.Waiting, plan.Person))
	if now, _, _ := planInHand(p); now == nil || now.Task.ID != "w" {
		t.Fatalf("in hand %v, want the waiting one", now)
	}
}
