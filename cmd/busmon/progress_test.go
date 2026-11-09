package main

import (
	"strings"
	"testing"
	"time"

	"github.com/netbja/agent-bus-monitor/plan"
)

// doneReq is a done tracked request of target that took d, done ago.
func doneReq(task, target string, d, ago time.Duration) request {
	end := shellNow.Add(-ago)
	return request{Task: task, Tracked: true, Target: target, Owner: target, State: "done", Accepted: end.Add(-d), Responded: end}
}

// inHandReq is an accepted tracked request of target, started ago.
func inHandReq(task, target string, ago time.Duration) request {
	at := shellNow.Add(-ago)
	return request{Task: task, Tracked: true, ID: task + "-id", Thread: task + "-id", From: "master", Target: target, Owner: target,
		State: "accepted", Created: at, Updated: at, Accepted: at}
}

// progress-line §3.2: the owner's usual length is the median accepted ->
// done of its last 10 done requests still on the board.
func TestOwnerPaces(t *testing.T) {
	var reqs []request
	for i, m := range []int{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110} { // 11: the oldest (10m, done 11h ago) is out
		reqs = append(reqs, doneReq("c"+string(rune('a'+i)), "coder", time.Duration(m)*time.Minute, time.Duration(11-i)*time.Hour))
	}
	reqs = append(reqs,
		doneReq("r1", "foureyes", 5*time.Minute, time.Hour),
		request{Task: "open", Tracked: true, Target: "coder", State: "accepted", Accepted: shellNow.Add(-time.Hour)},                       // not done
		request{Task: "nostart", Tracked: true, Target: "coder", State: "done", Responded: shellNow},                                       // no acceptance
		request{Task: "board", Target: "coder", State: "done", Accepted: shellNow.Add(-2 * time.Hour), Responded: shellNow},                // not a tracked request
		request{Task: "backwards", Tracked: true, Target: "coder", State: "done", Accepted: shellNow, Responded: shellNow.Add(-time.Hour)}, // done before accepted
	)
	p := ownerPaces(reqs)
	if c := p["coder"]; c.n != 10 || c.usual != 65*time.Minute { // 20..110: the median of 10 is (60+70)/2
		t.Errorf("coder %+v, want 10 done, 65m", c)
	}
	if f := p["foureyes"]; f.n != 1 || f.usual != 5*time.Minute {
		t.Errorf("foureyes %+v", f)
	}
	if _, ok := p["nobody"]; ok {
		t.Error("an owner with nothing done has a pace")
	}
}

// §3.1 the line: started, the time used against the usual length, the end;
// "estimate" under 5 done, "no estimate yet" with none; past the usual
// length it says so, the bar held at 95 %.
func TestProgressLine(t *testing.T) {
	pinLocal(t)
	start := shellNow.Add(-30 * time.Minute)
	r := inHandReq("x", "coder", 30*time.Minute)
	for _, c := range []struct {
		name string
		p    pace
		want []string
		not  []string
	}{
		{"sure", pace{usual: time.Hour, n: 6}, []string{"started " + start.Local().Format("15:04"), "~50%", "ends ~" + start.Add(time.Hour).Local().Format("15:04")}, []string{"estimate"}},
		{"thin", pace{usual: time.Hour, n: 3}, []string{"started", "estimate ~" + start.Add(time.Hour).Local().Format("15:04")}, []string{"%", "ends"}},
		{"none", pace{}, []string{"started", "no estimate yet"}, []string{"%", "ends", GlyphThinBar}},
		{"longer", pace{usual: 20 * time.Minute, n: 6}, []string{"longer than usual (20m)"}, []string{"%", "ends"}},
	} {
		l, ok := progressLine(r, c.p, shellNow)
		got := l.text()
		if !ok {
			t.Fatalf("%s: no line", c.name)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q has no %q", c.name, got, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(got, w) {
				t.Errorf("%s: %q has %q", c.name, got, w)
			}
		}
		filled := 0
		for _, s := range l {
			if s.Role == RoleDone {
				filled += strings.Count(s.Text, GlyphThinBar)
			}
			if s.Role != RoleDone && s.Role != RoleDim && s.Role != RoleMuted {
				t.Errorf("%s: %q in %v: not discreet", c.name, s.Text, s.Role)
			}
		}
		if bar := strings.Count(got, GlyphThinBar); c.name != "none" && bar != progressBarW {
			t.Errorf("%s: bar %d cells", c.name, bar)
		}
		switch c.name {
		case "sure":
			if filled != 5 {
				t.Errorf("sure: %d filled, want 5", filled)
			}
		case "thin", "longer": // no share: the bar is muted (longer: held at 95 %, muted)
			if filled != 0 {
				t.Errorf("%s: %d filled cells in the done colour", c.name, filled)
			}
		}
	}
	// a start on another day names the day
	old := inHandReq("y", "coder", 30*time.Hour)
	if l, _ := progressLine(old, pace{usual: time.Hour, n: 6}, shellNow); !strings.Contains(l.text(), "started "+old.Accepted.Local().Format("Mon 15:04")) {
		t.Errorf("another day: %q", l.text())
	}
	// not accepted: no line
	if _, ok := progressLine(request{Task: "z", Tracked: true, State: "requested"}, pace{usual: time.Hour, n: 6}, shellNow); ok {
		t.Error("a line for a request not started")
	}
}

// §3.1 in the Plan: under each NOW task in progress whose request is
// accepted, one line that is not a row; in review, or with no request, none.
func TestPlanBoardProgressLine(t *testing.T) {
	pinLocal(t)
	row := func(id string, st plan.State, board string) plan.Row {
		return plan.Row{Task: plan.Task{ID: id, Title: "title " + id, Owner: "coder", Board: board}, State: st}
	}
	p := boardPlan(row("i", plan.InProgress, "task-i"), row("r", plan.InReview, "task-r"), row("n", plan.InProgress, ""))
	reqs := []request{inHandReq("task-i", "coder", 10*time.Minute), inHandReq("task-r", "coder", 10*time.Minute), doneReq("old", "coder", time.Hour, 2*time.Hour)}
	v := planBoard(p, -1, false, shellNow, 120, TierWide, false, reqs)
	lineOf := func(id string) int {
		for _, it := range v.items {
			if it.task == id {
				return it.line
			}
		}
		t.Fatalf("no row %s", id)
		return -1
	}
	under := func(id string) string {
		if i := lineOf(id) + 1; i < len(v.lines) {
			return v.lines[i].text()
		}
		return ""
	}
	if got := under("i"); !strings.Contains(got, "started") || !strings.Contains(got, "estimate") {
		t.Errorf("under the task in progress: %q", got)
	}
	for _, id := range []string{"r", "n"} {
		if strings.Contains(under(id), "started") {
			t.Errorf("a line under %s: %q", id, under(id))
		}
	}
	for _, it := range v.items {
		if strings.Contains(v.lines[it.line].text(), "started") {
			t.Errorf("the line is a selectable row: %q", v.lines[it.line].text())
		}
	}
}

// §3.1 in Requests: under an accepted tracked request alone in its row, one
// line that is not selectable; none under a requested one or a folded group.
func TestRequestsProgressLine(t *testing.T) {
	pinLocal(t)
	now := shellNow
	reqs := []request{inHandReq("solo", "coder", 10*time.Minute), doneReq("old", "coder", time.Hour, 2*time.Hour)}
	waiting := inHandReq("later", "qa", time.Minute)
	waiting.State, waiting.Accepted = "requested", time.Time{}
	twinA, twinB := inHandReq("twin-a", "sentinel", 5*time.Minute), inHandReq("twin-b", "sentinel", 6*time.Minute)
	reqs = append(reqs, waiting, twinA, twinB)
	items := requestRows(requestGroups(reqInput{now: now, self: "charlie", reqs: reqs}), reqOpen{}, now, 140)
	for i, it := range items {
		text := it.Line.text()
		if strings.Contains(text, "solo") {
			if i+1 >= len(items) || !strings.Contains(items[i+1].Line.text(), "started") || items[i+1].Selectable || items[i+1].Key != "" {
				t.Fatalf("under solo: %+v", items[min(i+1, len(items)-1)])
			}
		}
		if (strings.Contains(text, "later") || strings.Contains(text, "twin")) && i+1 < len(items) && strings.Contains(items[i+1].Line.text(), "started") {
			t.Errorf("a line under %q", text)
		}
	}
}

// The cap: 58 of a usual 60 minutes reads ~95 %, never ~96 %; a fifth done
// request makes the end sure.
func TestProgressLineCapAndThreshold(t *testing.T) {
	pinLocal(t)
	r := inHandReq("x", "coder", 58*time.Minute)
	if l, _ := progressLine(r, pace{usual: time.Hour, n: 6}, shellNow); !strings.Contains(l.text(), "~95%") {
		t.Errorf("capped: %q", l.text())
	}
	if l, _ := progressLine(r, pace{usual: time.Hour, n: paceSure}, shellNow); strings.Contains(l.text(), "estimate") || !strings.Contains(l.text(), "ends ~") {
		t.Errorf("%d done is sure: %q", paceSure, l.text())
	}
	if l, _ := progressLine(r, pace{usual: time.Hour, n: paceSure - 1}, shellNow); !strings.Contains(l.text(), "estimate") {
		t.Errorf("%d done is thin: %q", paceSure-1, l.text())
	}
}

// foureyes P3: a thin estimate already past, from another day, names its day:
// a bare "estimate ~10:00" would read as today.
func TestProgressLineOverdueEstimateNamesTheDay(t *testing.T) {
	pinLocal(t)
	old := inHandReq("y", "coder", 30*time.Hour)
	end := old.Accepted.Add(time.Hour)
	l, _ := progressLine(old, pace{usual: time.Hour, n: 3}, shellNow)
	if want := "estimate ~" + end.Local().Format("Mon 15:04"); !strings.Contains(l.text(), want) {
		t.Errorf("overdue estimate: %q, want %q", l.text(), want)
	}
	today := inHandReq("z", "coder", 2*time.Hour) // past, but today: the clock alone
	if want := "estimate ~" + today.Accepted.Add(time.Hour).Local().Format("15:04"); !strings.Contains(thinLineText(today), want) {
		t.Errorf("overdue today: %q, want %q", thinLineText(today), want)
	}
}

// thinLineText is the line of r against a thin pace of an hour.
func thinLineText(r request) string {
	l, _ := progressLine(r, pace{usual: time.Hour, n: 3}, shellNow)
	return l.text()
}

// foureyes P3: a request is with its target before its board owner.
func TestReqOwnerTargetFirst(t *testing.T) {
	if got := reqOwner(request{Target: "coder", Owner: "master"}); got != "coder" {
		t.Errorf("target and owner: %q", got)
	}
	if got := reqOwner(request{Owner: "master"}); got != "master" {
		t.Errorf("owner only: %q", got)
	}
}

// foureyes P3: only an accepted request has a line; one accepted then
// blocked keeps its acceptance time but has none, in Requests and in the Plan.
func TestProgressLineOnlyWhileAccepted(t *testing.T) {
	pinLocal(t)
	stuck := inHandReq("stuck", "coder", 10*time.Minute)
	stuck.State = "blocked"
	items := requestRows(requestGroups(reqInput{now: shellNow, self: "charlie", reqs: []request{stuck}}), reqOpen{}, shellNow, 140)
	seen := false
	for i, it := range items {
		if strings.Contains(it.Line.text(), "stuck") {
			seen = true
			if i+1 < len(items) && strings.Contains(items[i+1].Line.text(), "started") {
				t.Errorf("a line under a blocked request: %q", items[i+1].Line.text())
			}
		}
	}
	if !seen {
		t.Fatal("no row for the blocked request")
	}
	row := plan.Row{Task: plan.Task{ID: "b", Title: "title b", Owner: "coder", Board: "stuck"}, State: plan.InProgress}
	if l, ok := planProgressLine(row, []request{stuck}, nil, shellNow); ok {
		t.Errorf("a plan line for a blocked request: %q", l.text())
	}
	stuck.State = "accepted"
	if _, ok := planProgressLine(row, []request{stuck}, nil, shellNow); !ok {
		t.Error("no plan line once accepted")
	}
}
